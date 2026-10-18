package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"superset/internal/ai/registry"
	"superset/internal/models"
	"superset/internal/queries"
	"superset/internal/semantic"
)

// tools_semantic.go implements W-06: a structured query path over the semantic
// model.
//
// Why this exists next to query_executor: free SQL asks the model to get the
// grain, the filter placement and the ratio denominators right in one shot, and
// a weak model fails all three independently. A structured call states the
// intent (measures + dimensions + time + filters) and lets the builder — which
// already knows this engine's quoting, grouping and limits — write the SQL. The
// generated SQL is returned so the answer stays auditable.
//
// The two paths are complementary, not redundant: anything the structured call
// cannot express falls back to query_executor under the W-02 guard.

// semanticQueryInput is the wire shape. It mirrors Wren's
// `wren cube query --measures --dimensions --time-dimension`.
type semanticQueryInput struct {
	DatasetID  int      `json:"dataset_id"`
	Model      string   `json:"model"`
	Metrics    []string `json:"metrics"`
	Dimensions []string `json:"dimensions"`
	Time       *struct {
		Field string `json:"field"`
		Range string `json:"range"`
		Grain string `json:"grain"`
	} `json:"time"`
	Filters []struct {
		Field string `json:"field"`
		Op    string `json:"op"`
		Value any    `json:"value"`
	} `json:"filters"`
	Limit int `json:"limit"`
	Order []struct {
		Field string `json:"field"`
		Desc  bool   `json:"desc"`
	} `json:"order"`
}

// SemanticQueryFieldError is returned when the request names a field the model
// does not declare. The available list is the whole point: it lets the model
// correct itself in one step instead of guessing twice.
type SemanticQueryFieldError struct {
	Code      string   `json:"status"`
	Message   string   `json:"message"`
	Model     string   `json:"model"`
	Unknown   []string `json:"unknown"`
	Available []string `json:"available_fields"`
	Hint      string   `json:"hint"`
}

func (e *SemanticQueryFieldError) Error() string {
	blob, err := json.Marshal(e)
	if err != nil {
		return e.Message
	}
	return string(blob)
}

var grainAliases = map[string]string{
	"hour": "PT1H", "day": "P1D", "date": "P1D", "week": "P1W",
	"month": "P1M", "quarter": "P3M", "year": "P1Y", "minute": "PT1M",
	"d": "P1D", "w": "P1W", "m": "P1M", "y": "P1Y",
}

func semanticQuery(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in semanticQueryInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.DatasetID <= 0 {
			return nil, fmt.Errorf("dataset_id is required")
		}
		if s.Queries == nil {
			return nil, fmt.Errorf("query engine is unavailable")
		}
		sem := semantic.New(s.DB, s.Log)
		view, err := sem.Resolve(ctx, in.DatasetID, in.Model)
		if err != nil {
			// Tell the model what it *can* ask for, not just that it was wrong.
			models2, lerr := sem.ListModels(ctx, in.DatasetID)
			names := make([]string, 0, len(models2))
			if lerr == nil {
				for _, m := range models2 {
					if m.Status == "published" {
						names = append(names, m.Name)
					}
				}
			}
			payload := map[string]any{
				"status":           "rejected_by_semantic_layer",
				"code":             "MODEL_NOT_FOUND",
				"message":          err.Error(),
				"available_models": names,
				"fallback":         "若该数据集尚未配置语义模型，请改用 schema_reader + query_executor",
				"dataset_id":       in.DatasetID,
			}
			blob, _ := json.Marshal(payload)
			return nil, fmt.Errorf("%s", blob)
		}

		qo, applied, err := semanticQueryObject(view, &in)
		if err != nil {
			return nil, err
		}

		built, _, err := queries.BuildDatasetSQL(ctx, s.DB, in.DatasetID, qo, queries.BuildOptions{
			DefaultRowLimit: s.Config.AIQueryMaxRows,
			MaxRow:          s.Config.AIQueryMaxRows,
		})
		if err != nil {
			return nil, err
		}
		if err := s.guardDatasetSQL(ctx, in.DatasetID, built.SQL); err != nil {
			return nil, err
		}

		res, err := s.Queries.ExecuteReadOnlySQL(ctx, registry.UserIDFrom(ctx), built.SQL, queries.ReadOnlyOptions{
			DatasetID:     in.DatasetID,
			RowLimit:      s.Config.AIQueryMaxRows,
			ExemptMasking: isAdminFrom(ctx),
		})
		if err != nil {
			return nil, err
		}
		out := map[string]any{
			"model":           view.Model.Name,
			"manifest_hash":   view.Model.ManifestHash,
			"sql":             res.SQL,
			"generated_sql":   built.SQL,
			"rows":            res.Data,
			"colnames":        res.ColNames,
			"rowcount":        res.RowCount,
			"truncated":       res.Truncated,
			"query_id":        res.QueryID,
			"applied_filters": applied,
			"query_mode":      "semantic",
		}
		if len(res.MaskedColumns) > 0 {
			out["masked_columns"] = res.MaskedColumns
			out["masking_note"] = "这些列的值已按合规规则脱敏，可用于计数/分组，不可用于识别个人"
		}
		return &registry.ToolResult{
			Output:     out,
			SideEffect: registry.Read,
			DurationMS: res.DurationMS,
		}, nil
	}
}

// semanticQueryObject translates the structured request into the builder's
// query object. Every reject path returns a machine-readable error listing the
// valid choices.
func semanticQueryObject(view *semantic.ModelView, in *semanticQueryInput) (*queries.QueryObject, []map[string]any, error) {
	byName := map[string]semantic.FieldSpec{}
	available := make([]string, 0, len(view.Manifest.Fields))
	for _, f := range view.Manifest.Fields {
		if f.Hidden {
			// Hidden fields are addressable by relationships, not by callers.
			continue
		}
		byName[f.Name] = f
		available = append(available, f.Name)
	}
	sort.Strings(available)

	qo := &queries.QueryObject{Extras: map[string]any{}}
	var unknown []string
	applied := []map[string]any{}

	for _, name := range in.Metrics {
		f, ok := byName[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		if f.Kind != models.FieldKindMetric {
			// A column used as a measure is a common mistake; count it rather
			// than silently producing a non-aggregated select.
			agg := firstNonEmptyStr(f.DefaultAgg, "SUM")
			if !isNumericType(f.Type) {
				agg = "COUNT"
			}
			qo.Metrics = append(qo.Metrics, queries.AdhocMetric{
				SQLExpression: fmt.Sprintf("%s(%s)", agg, f.Expression),
				Label:         name, HasCustomLabel: true,
			})
			applied = append(applied, map[string]any{
				"field": name, "note": fmt.Sprintf("%s 不是 metric 字段，按 %s(%s) 聚合", name, agg, f.Expression),
			})
			continue
		}
		qo.Metrics = append(qo.Metrics, queries.AdhocMetric{
			SQLExpression: f.Expression, Label: name, HasCustomLabel: true,
		})
	}

	if len(in.Metrics) == 0 {
		// No measure means "how many": a bare row count is what every analyst
		// means by "按 X 分组" without a measure.
		qo.Metrics = append(qo.Metrics, queries.AdhocMetric{
			SQLExpression: "COUNT(*)", Label: "count", HasCustomLabel: true,
		})
	}

	for _, name := range in.Dimensions {
		f, ok := byName[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		col := queries.AdhocColumn{ColumnName: f.Expression, Label: name, HasCustomLabel: true}
		if f.Kind == models.FieldKindDimension {
			// Expressions cannot go through ColumnName; the builder quotes it as
			// an identifier and the query would fail.
			col = queries.AdhocColumn{SQLExpression: f.Expression, Label: name, HasCustomLabel: true}
		}
		qo.Groupby = append(qo.Groupby, col)
	}

	if in.Time != nil {
		f, ok := byName[in.Time.Field]
		if !ok {
			unknown = append(unknown, in.Time.Field)
		} else {
			// The time field must be the dataset's main temporal column for the
			// builder to apply the range; if it is not, keep it as a group-by
			// dimension and say so instead of dropping the range silently.
			if f.IsDttm {
				grain := strings.ToLower(strings.TrimSpace(in.Time.Grain))
				if norm, ok := grainAliases[grain]; ok {
					qo.Granularity = norm
					qo.IsTimeseries = true
				} else if grain != "" {
					qo.Granularity = grain
					qo.IsTimeseries = true
				}
				if from, to, ok := parseSemanticRange(in.Time.Range, time.Now()); ok {
					qo.FromDttm, qo.ToDttm = from, to
					applied = append(applied, map[string]any{
						"time_range": in.Time.Range, "from": from, "to": to,
					})
				}
			} else {
				applied = append(applied, map[string]any{
					"field": in.Time.Field,
					"note":  "该字段不是时间列，已作为分组维度而非时间轴",
				})
				qo.Groupby = append(qo.Groupby, queries.AdhocColumn{ColumnName: f.Expression, Label: f.Name, HasCustomLabel: true})
			}
		}
	}

	for _, f := range in.Filters {
		field, ok := byName[f.Field]
		if !ok {
			unknown = append(unknown, f.Field)
			continue
		}
		op := normalizeSemanticOp(f.Op)
		qo.Filters = append(qo.Filters, queries.Filter{Col: field.Expression, Op: op, Val: f.Value})
		applied = append(applied, map[string]any{
			"field": field.Name, "op": op, "value": f.Value, "source": "request",
		})
	}

	// Default filters come last so they cannot be overridden by a request that
	// forgets them: "ignore refunded orders" is not a suggestion. The manifest
	// already carries them compiled, so there is nothing to re-read here.
	var defaults []semantic.FilterSpec
	if view.Manifest != nil {
		defaults = view.Manifest.Filters
	}
	for _, df := range defaults {
		expr := df.Field
		if f, ok := byName[df.Field]; ok {
			expr = f.Expression
		}
		qo.Filters = append(qo.Filters, queries.Filter{Col: expr, Op: normalizeSemanticOp(df.Op), Val: df.Value})
		applied = append(applied, map[string]any{
			"field": df.Field, "op": df.Op, "value": df.Value,
			"source": "semantic_rule", "reason": df.Reason,
		})
	}

	if len(unknown) > 0 {
		return nil, nil, &SemanticQueryFieldError{
			Code:      "rejected_by_semantic_layer",
			Message:   fmt.Sprintf("模型 %q 没有声明这些字段", view.Model.Name),
			Model:     view.Model.Name,
			Unknown:   dedupeStrings(unknown),
			Available: available,
			Hint:      "只能使用 available_fields 里的名字；需要新字段请先在语义模型里声明",
		}
	}

	if in.Limit > 0 {
		qo.RowLimit = in.Limit
	}
	for _, o := range in.Order {
		if _, ok := byName[o.Field]; !ok {
			continue
		}
		qo.Orderby = append(qo.Orderby, [2]any{o.Field, !o.Desc})
	}
	return qo, applied, nil
}

// parseSemanticRange turns a human range name into builder bounds.
func parseSemanticRange(raw string, now time.Time) (string, string, bool) {
	r := strings.ToLower(strings.TrimSpace(raw))
	if r == "" {
		return "", "", false
	}
	if strings.Contains(r, " : ") {
		from, to := queries.SplitTimeRange(raw)
		return from, to, from != "" || to != ""
	}
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch r {
	case "today":
		return day(today), "", true
	case "yesterday":
		return day(today.AddDate(0, 0, -1)), day(today), true
	case "last_7_days", "last7days":
		return day(today.AddDate(0, 0, -7)), "", true
	case "last_30_days", "last30days":
		return day(today.AddDate(0, 0, -30)), "", true
	case "last_90_days", "last90days":
		return day(today.AddDate(0, 0, -90)), "", true
	case "last_12_months", "last12months", "last_year":
		return day(today.AddDate(-1, 0, 0)), "", true
	case "this_year":
		return day(time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())), "", true
	case "last_month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return day(first.AddDate(0, -1, 0)), day(first), true
	case "last_quarter":
		q := (int(now.Month())-1)/3*3 + 1
		start := time.Date(now.Year(), time.Month(q), 1, 0, 0, 0, 0, now.Location())
		return day(start.AddDate(0, -3, 0)), day(start), true
	}
	// Bare dates: let the builder's own parser have the last word.
	from, to := queries.SplitTimeRange(raw)
	return from, to, from != "" || to != ""
}

func normalizeSemanticOp(op string) string {
	switch strings.ToLower(strings.TrimSpace(op)) {
	case "eq", "=", "==":
		return "="
	case "ne", "!=", "<>", "not_eq":
		return "!="
	case "gt":
		return ">"
	case "gte", ">=":
		return ">="
	case "lt":
		return "<"
	case "lte", "<=":
		return "<="
	case "in":
		return "IN"
	case "not_in", "notin":
		return "NOT IN"
	case "like":
		return "LIKE"
	case "ilike":
		return "ILIKE"
	case "is_null", "null":
		return "IS NULL"
	case "is_not_null", "not_null":
		return "IS NOT NULL"
	}
	return op
}

func isNumericType(t string) bool {
	low := strings.ToLower(t)
	for _, marker := range []string{"int", "numeric", "decimal", "float", "double", "real", "number"} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
