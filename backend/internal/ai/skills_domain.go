package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"superset/internal/ai/registry"
	"superset/internal/models"
	"superset/internal/semantic"
)

// semanticReader exposes the semantic model (MDL-lite, benchmark W-05) so the
// Agent can align a business term to a declared field instead of guessing.
//
// History: this used to read only semantic_layers, a Superset table that is
// empty in this deployment — so the tool always answered "not configured" while
// a real semantic model sat in a different table. It now reads the published
// model first and keeps semantic_layers as a secondary source for deployments
// that populate it with external semantic assets.
func semanticReader(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			DatasetID int      `json:"dataset_id"`
			Model     string   `json:"model"`
			Codes     []string `json:"codes"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if s.DB == nil {
			return nil, errors.New("metadata store is unavailable")
		}

		result := map[string]any{"dataset_id": in.DatasetID}

		// 1. The semantic model this deployment actually maintains.
		if in.DatasetID > 0 {
			sem := semantic.New(s.DB, s.Log)
			modelsList, err := sem.ListModels(ctx, in.DatasetID)
			if err == nil {
				names := make([]string, 0, len(modelsList))
				for _, m := range modelsList {
					if m.Status == "published" || m.Name == in.Model {
						names = append(names, m.Name)
					}
				}
				var view *semantic.ModelView
				if in.Model != "" {
					view, err = sem.Resolve(ctx, in.DatasetID, in.Model)
				} else if len(names) > 0 {
					view, err = sem.Resolve(ctx, in.DatasetID, names[0])
				}
				if err == nil && view != nil && view.Manifest != nil {
					fields := make([]map[string]any, 0, len(view.Manifest.Fields))
					for _, f := range view.Manifest.Fields {
						if f.Hidden {
							continue
						}
						fields = append(fields, map[string]any{
							"name": f.Name, "kind": f.Kind, "expression": f.Expression,
							"type": f.Type, "description": f.Description, "unit": f.Unit,
							"enum_values": f.Enum, "pii_level": f.PIILevel,
						})
					}
					result["model"] = map[string]any{
						"name": view.Model.Name, "description": view.Model.Description,
						"primary_key": view.Model.PrimaryKey, "version": view.Model.Version,
						"manifest_hash": view.Model.ManifestHash,
					}
					result["fields"] = fields
					result["relationships"] = view.Manifest.Joins
					result["default_filters"] = view.Manifest.Filters
					result["configured"] = true
					if len(view.Manifest.Filters) > 0 {
						result["filter_note"] = "这些默认过滤条件已由 semantic_query 自动应用；用 query_executor 写自由 SQL 时必须自己加上，否则口径不一致"
					}
				}
			}
		}

		// 2. External semantic assets, if any were registered.
		q := s.DB.WithContext(ctx).Model(&models.SemanticLayer{})
		if len(in.Codes) > 0 {
			q = q.Where("name IN ?", in.Codes)
		}
		var layers []models.SemanticLayer
		if err := q.Order("name").Limit(200).Find(&layers).Error; err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(layers))
		for _, l := range layers {
			out = append(out, map[string]any{
				"uuid":          l.UUID,
				"name":          l.Name,
				"description":   l.Description,
				"type":          l.Type,
				"configuration": json.RawMessage(l.Configuration),
			})
		}
		result["semantic_layers"] = out

		if _, ok := result["configured"]; !ok {
			result["configured"] = len(out) > 0
		}
		if result["configured"] == false {
			// Saying "empty array" lets the model conclude the warehouse has no
			// semantic layer and start inventing joins. Say what is actually true:
			// nothing is configured, so fall back to the dataset schema.
			result["note"] = "该数据集没有已发布的语义模型，也没有外部语义资产。请改用 schema_reader 与 metric_dictionary 获取口径，不要假设存在模型/关系定义。"
		}
		return &registry.ToolResult{
			Output:     result,
			SideEffect: registry.Read,
		}, nil
	}
}

const domainSystemPrompt = `你是资深财务与业务分析师。只输出一个 JSON 对象，不要任何解释文字，也不要代码块围栏。所有数值必须严格来自提供的查询结果，严禁编造；若结果中缺少某项，将该字段置为 null 并在 markdown 中说明原因。`

// parseArgs decodes raw tool arguments into a generic map.
func parseArgs(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func argInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func argStr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func argInts(m map[string]any, key string) []int {
	arr, _ := m[key].([]any)
	out := make([]int, 0, len(arr))
	for _, v := range arr {
		switch n := v.(type) {
		case float64:
			out = append(out, int(n))
		case int:
			out = append(out, n)
		}
	}
	return out
}

// resolveDatasetID picks the target dataset from args (dataset_id, then the
// first of dataset_ids, then the run context).
func resolveDatasetID(args map[string]any, actx registry.AIContext) int {
	if id := argInt(args, "dataset_id"); id > 0 {
		return id
	}
	if ids := argInts(args, "dataset_ids"); len(ids) > 0 {
		return ids[0]
	}
	if actx.DatasetID != nil {
		return *actx.DatasetID
	}
	return 0
}

// authorSQL asks the model to write a single read-only SELECT for a goal and
// returns its parsed JSON reply (sql/confidence/explanation).
func (s *Service) authorSQL(ctx context.Context, userID int, rt *registry.ToolRuntime, datasetID int, goal string) (map[string]any, int, error) {
	if rt == nil || rt.InvokeTool == nil {
		return nil, 0, errors.New("tool runtime is unavailable")
	}
	schema := fetchSchema(ctx, rt, datasetID, goal)
	prompt := fmt.Sprintf("数据集 schema（紧凑文本）：\n%s\n\n分析目标：%s\n\n"+
		`请生成一条只读 SELECT（务必带 LIMIT），只返回 JSON：{"sql":"...","confidence":0.0到1.0的小数,"explanation":"一句话口径说明"}`,
		schema, goal)
	content, tokens, err := s.complete(ctx, userID, nl2sqlSystemPrompt, prompt)
	if err != nil {
		return nil, 0, err
	}
	out := map[string]any{}
	if raw := extractJSONObject(content); raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out, tokens, nil
}

// runReadSQL executes a validated statement through the query_executor tool so
// it goes through the permission guard, RLS and auditing.
func (s *Service) runReadSQL(ctx context.Context, rt *registry.ToolRuntime, datasetID int, sql string) (*registry.ToolResult, error) {
	if rt == nil || rt.InvokeTool == nil {
		return nil, errors.New("tool runtime is unavailable")
	}
	rawArgs, _ := json.Marshal(map[string]any{"sql": sql, "dataset_id": datasetID})
	return rt.InvokeTool(ctx, "query_executor", rawArgs)
}

// domainSpec describes one professional-analysis skill: how to phrase the取数
// goal and the JSON shape the model must fill from the query result.
type domainSpec struct {
	name        string
	goal        func(args map[string]any) string
	outputShape string
}

// domainExecutor builds a skill executor that authors SQL, runs it and lets the
// model compose the domain-specific structured report grounded on the rows.
func domainExecutor(s *Service, spec domainSpec) registry.SkillExecutor {
	return func(ctx context.Context, in *registry.Invocation, rt *registry.ToolRuntime) (*registry.Result, error) {
		args := parseArgs(in.Args)
		datasetID := resolveDatasetID(args, in.Context)
		if datasetID <= 0 {
			return nil, fmt.Errorf("%s: dataset_id is required", spec.name)
		}

		goal := spec.goal(args)
		authored, tokens, err := s.authorSQL(ctx, in.UserID, rt, datasetID, goal)
		if err != nil {
			return nil, err
		}
		sql, _ := authored["sql"].(string)
		if sql == "" {
			return nil, fmt.Errorf("%s: could not author a SQL statement", spec.name)
		}

		tr, err := s.runReadSQL(ctx, rt, datasetID, sql)
		if err != nil {
			return nil, err
		}
		sample, _ := json.Marshal(s.sampleRows(tr.Output))

		// P1-5 归因下钻：结构分析的增加值由程序按行计算（权威、无幻觉），
		// 作为「贡献度分解」注入提示并放入 artifacts。
		contribution := []map[string]any(nil)
		if spec.name == "structure" {
			contribution = contributionRows(tr.Output, argStr(args, "dimension"), argStr(args, "metric"))
		}
		contributionNote := ""
		if len(contribution) > 0 {
			b, _ := json.Marshal(contribution)
			contributionNote = "\n\n程序计算的贡献度分解（权威数据，报告中的占比/贡献结论必须与其一致）：\n" + string(b)
		}

		prompt := fmt.Sprintf("分析目标：%s\n已执行 SQL：%s\n查询结果（JSON）：\n%s%s\n\n"+
			`请完成上述专业分析，只返回 JSON：{"markdown":"中文 Markdown 报告","data":%s}`,
			goal, sql, string(sample), contributionNote, spec.outputShape)
		content, t2, err := s.complete(ctx, in.UserID, domainSystemPrompt, prompt)
		if err != nil {
			return nil, err
		}

		data := map[string]any{}
		md := content
		if obj := extractJSONObject(content); obj != "" {
			parsed := map[string]any{}
			if json.Unmarshal([]byte(obj), &parsed) == nil {
				if m, ok := parsed["markdown"].(string); ok && m != "" {
					md = m
				}
				if d, ok := parsed["data"].(map[string]any); ok {
					data = d
				}
			}
		}
		data["markdown"] = md

		artifacts := map[string]any{"sql": sql}
		if ps, ok := data["page_spec"]; ok {
			artifacts["page_spec"] = ps
		}
		if len(contribution) > 0 {
			artifacts["contribution"] = contribution
		}
		if spec, ok := chartSpecForDomain(spec.name, datasetID, args); ok {
			artifacts["chart_spec"] = spec
		}
		return &registry.Result{
			Output:     data,
			Markdown:   md,
			Artifacts:  artifacts,
			TokenUsage: tokens + t2,
		}, nil
	}
}

// contributionRows computes each group's absolute value, share of total and
// contribution rank straight from the query rows (P1-5). Rows whose dimension
// or metric column is absent (aliased/renamed) yield nil and the feature
// silently degrades to the model-authored report.
func contributionRows(output any, dim, metric string) []map[string]any {
	if dim == "" || metric == "" {
		return nil
	}
	m, ok := output.(map[string]any)
	if !ok {
		return nil
	}
	rows, ok := m["rows"].([]map[string]any)
	if !ok || len(rows) == 0 {
		return nil
	}
	var total float64
	vals := make([]struct {
		name  string
		value float64
	}, 0, len(rows))
	for _, row := range rows {
		name := asStringish(row[dim])
		if name == "" {
			continue
		}
		v := asFloatish(row[metric])
		if v == nil {
			continue
		}
		vals = append(vals, struct {
			name  string
			value float64
		}{name, *v})
		total += *v
	}
	if len(vals) == 0 || total == 0 {
		return nil
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i].value > vals[j].value })
	out := make([]map[string]any, 0, len(vals))
	var cum float64
	for _, v := range vals {
		cum += v.value
		out = append(out, map[string]any{
			"name":         v.name,
			"value":        v.value,
			"share":        round2(v.value / total * 100),
			"cum_share":    round2(cum / total * 100),
			"contribution": round2(v.value / total),
		})
	}
	return out
}

// asStringish coerces a dimension cell to its display string.
func asStringish(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", n)
	}
}

// asFloatish coerces a metric cell to a finite float, nil when not numeric.
func asFloatish(v any) *float64 {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return nil
		}
		f = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return nil
		}
		f = parsed
	default:
		return nil
	}
	return &f
}

// round2 keeps percentages readable.
func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

// chartSpecForDomain maps a domain analysis onto a one-click chart spec so its
// answer can land on a dashboard without leaving the conversation.
func chartSpecForDomain(name string, datasetID int, args map[string]any) (map[string]any, bool) {
	if datasetID <= 0 {
		return nil, false
	}
	var viz string
	switch name {
	case "trend":
		viz = "line"
	case "structure":
		viz = "pie"
	case "yoy_mom":
		viz = "bar"
	default:
		return nil, false // dupont is a composite report, not a single chart
	}
	metric := argStr(args, "metric")
	dim := argStr(args, "dimension")
	spec := map[string]any{
		"dataset_id": datasetID, "viz_type": viz,
		"slice_name": fmt.Sprintf("%s · %s", argStr(args, "period"), metric),
	}
	if metric != "" {
		spec["metrics"] = []string{metric}
	}
	if dim != "" {
		spec["groupby"] = []string{dim}
	}
	return spec, true
}

// domainSpecs holds the P3 professional-analysis paradigms. Each is an
// "metric preset + calculation logic + page_spec" fixed shape.
func domainSpecs() []domainSpec {
	return []domainSpec{
		{
			name: "dupont",
			goal: func(a map[string]any) string {
				ids := argInts(a, "dataset_ids")
				return fmt.Sprintf("杜邦分析（period=%s，数据集=%v）：取净利润、营业收入、总资产、股东权益（必要时含期初期末均值），用于计算 ROE = 净利润率 × 总资产周转率 × 权益乘数。",
					argStr(a, "period"), ids)
			},
			outputShape: `{"roe":数值,"factors":{"net_margin":数值,"asset_turnover":数值,"equity_multiplier":数值},"page_spec":{"viz_type":"...","layout":[{"type":"...","title":"..."}]}}`,
		},
		{
			name: "trend",
			goal: func(a map[string]any) string {
				return fmt.Sprintf("趋势分析（metric=%s，time_column=%s，window=%s）：按时间粒度聚合该指标形成序列，用于计算同比/环比与拐点。",
					argStr(a, "metric"), argStr(a, "time_column"), argStr(a, "window"))
			},
			outputShape: `{"series":[{"period":"...","value":数值}],"yoy":[{"period":"...","yoy":数值}],"mom":[{"period":"...","mom":数值}],"turning_points":["..."],"page_spec":{"viz_type":"...","layout":[{"type":"...","title":"..."}]}}`,
		},
		{
			name: "structure",
			goal: func(a map[string]any) string {
				top := argInt(a, "top_n")
				if top <= 0 {
					top = 10
				}
				return fmt.Sprintf("结构分析（metric=%s，dimension=%s，top_n=%d）：按维度聚合该指标并降序排列，用于占比与帕累托分析。",
					argStr(a, "metric"), argStr(a, "dimension"), top)
			},
			outputShape: `{"total":数值,"share":[{"name":"...","value":数值,"share":数值}],"pareto":{"top_n":数值,"cum_share":数值},"page_spec":{"viz_type":"...","layout":[{"type":"...","title":"..."}]}}`,
		},
		{
			name: "yoy_mom",
			goal: func(a map[string]any) string {
				return fmt.Sprintf("同环比分析（metric=%s，period=%s）：取本期、上一期与去年同期（若可得）的指标值，用于计算同比(YoY)与环比(MoM)并做差异瀑布分解。",
					argStr(a, "metric"), argStr(a, "period"))
			},
			outputShape: `{"current":数值,"prev":数值,"yoy":数值,"mom":数值,"waterfall":[{"name":"...","value":数值}],"page_spec":{"viz_type":"...","layout":[{"type":"...","title":"..."}]}}`,
		},
	}
}

// domainExecutors returns the P3 skill executors keyed by skill name.
func domainExecutors(s *Service) map[string]registry.SkillExecutor {
	out := map[string]registry.SkillExecutor{}
	for _, spec := range domainSpecs() {
		out[spec.name] = domainExecutor(s, spec)
	}
	return out
}

// metricComputeExecutor computes semantic-layer metrics: it reads the metric
// definitions, authors SQL, runs it, and returns values plus a 口径对齐 proposal
// so the user can confirm metric→field mappings before trusting the numbers.
func metricComputeExecutor(s *Service) registry.SkillExecutor {
	return func(ctx context.Context, in *registry.Invocation, rt *registry.ToolRuntime) (*registry.Result, error) {
		var args struct {
			MetricCodes []string `json:"metric_codes"`
			DatasetID   int      `json:"dataset_id"`
			Filters     []any    `json:"filters"`
			TimeGrain   string   `json:"time_grain"`
		}
		if err := json.Unmarshal(in.Args, &args); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if args.DatasetID == 0 && in.Context.DatasetID != nil {
			args.DatasetID = *in.Context.DatasetID
		}
		if args.DatasetID <= 0 {
			return nil, errors.New("metric_compute: dataset_id is required")
		}
		if len(args.MetricCodes) == 0 {
			return nil, errors.New("metric_compute: metric_codes is required")
		}

		defs := "（未找到语义指标定义）"
		if rt != nil && rt.InvokeTool != nil {
			rawArgs, _ := json.Marshal(map[string]any{"dataset_id": args.DatasetID, "codes": args.MetricCodes})
			if tr, err := rt.InvokeTool(ctx, "semantic_reader", rawArgs); err == nil && tr != nil {
				if b, e := json.Marshal(tr.Output); e == nil {
					defs = string(b)
				}
			}
		}

		goal := fmt.Sprintf("按语义层口径计算指标 %v（time_grain=%s，filters=%v）。语义层定义：%s",
			args.MetricCodes, args.TimeGrain, args.Filters, defs)
		authored, tokens, err := s.authorSQL(ctx, in.UserID, rt, args.DatasetID, goal)
		if err != nil {
			return nil, err
		}
		sql, _ := authored["sql"].(string)
		if sql == "" {
			return nil, errors.New("metric_compute: could not author a SQL statement")
		}

		tr, err := s.runReadSQL(ctx, rt, args.DatasetID, sql)
		if err != nil {
			return nil, err
		}
		sample, _ := json.Marshal(s.sampleRows(tr.Output))

		prompt := fmt.Sprintf("需要计算的指标：%v\n语义层定义：%s\n已执行 SQL：%s\n查询结果（JSON）：\n%s\n\n"+
			`请只返回 JSON：{"metrics":{"指标编码":数值},"alignment":[{"metric":"指标编码","proposed_expression":"建议的 SQL 口径","confidence":0.0到1.0,"note":"对齐说明"}]}`,
			args.MetricCodes, defs, sql, string(sample))
		content, t2, err := s.complete(ctx, in.UserID, domainSystemPrompt, prompt)
		if err != nil {
			return nil, err
		}

		out := map[string]any{
			"metrics": map[string]any{},
			"sql":     []string{sql},
		}
		if raw := extractJSONObject(content); raw != "" {
			parsed := map[string]any{}
			if json.Unmarshal([]byte(raw), &parsed) == nil {
				if m, ok := parsed["metrics"]; ok {
					out["metrics"] = m
				}
				if a, ok := parsed["alignment"]; ok {
					out["alignment"] = a
				}
			}
		}

		md := renderMetricMarkdown(out, args.MetricCodes)
		return &registry.Result{
			Output:     out,
			Markdown:   md,
			Artifacts:  map[string]any{"sql": []string{sql}},
			TokenUsage: tokens + t2,
		}, nil
	}
}

// renderMetricMarkdown turns the computed metrics + alignment proposal into a
// short Markdown summary for the user.
func renderMetricMarkdown(out map[string]any, codes []string) string {
	var b strings.Builder
	b.WriteString("**指标计算结果**\n\n| 指标 | 值 |\n|---|---|\n")
	if m, ok := out["metrics"].(map[string]any); ok {
		for _, code := range codes {
			fmt.Fprintf(&b, "| %s | %v |\n", code, m[code])
		}
	}
	if a, ok := out["alignment"].([]any); ok && len(a) > 0 {
		b.WriteString("\n**口径对齐提案（请确认后再采信）**\n\n")
		for _, item := range a {
			if m, ok := item.(map[string]any); ok {
				fmt.Fprintf(&b, "- `%v` → `%v`（置信度 %v）：%v\n", m["metric"], m["proposed_expression"], m["confidence"], m["note"])
			}
		}
	}
	return b.String()
}
