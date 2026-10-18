package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"superset/internal/ai/registry"
	"superset/internal/queries"
	"superset/internal/security"
)

// wireExecutors attaches the concrete executors to the read tools and skills
// registered in builtin.go. Write tools stay unwired until P4.
func wireExecutors(s *Service) {
	if t, ok := s.Tools.Get("query_executor"); ok {
		t.Execute = queryExecutor(s)
	}
	if t, ok := s.Tools.Get("semantic_query"); ok {
		t.Execute = semanticQuery(s)
	}
	if t, ok := s.Tools.Get("schema_reader"); ok {
		t.Execute = schemaReader(s)
	}
	if t, ok := s.Tools.Get("semantic_reader"); ok {
		t.Execute = semanticReader(s)
	}
	if t, ok := s.Tools.Get("metric_dictionary"); ok {
		t.Execute = metricDictionaryExecutor(s)
	}
	if t, ok := s.Tools.Get("chart_reader"); ok {
		t.Execute = chartReader(s)
	}
	if t, ok := s.Tools.Get("dashboard_reader"); ok {
		t.Execute = dashboardReader(s)
	}
	if t, ok := s.Tools.Get("result_sampler"); ok {
		t.Execute = resultSampler(s)
	}
	if t, ok := s.Tools.Get("meta_search"); ok {
		t.Execute = metaSearch(s)
	}
	if sk, ok := s.Skills.Get("nl2sql"); ok {
		sk.Executor = nl2SQLExecutor(s)
	}
	if sk, ok := s.Skills.Get("sql_explain"); ok {
		sk.Executor = sqlExplainExecutor(s)
	}
	if sk, ok := s.Skills.Get("insight"); ok {
		sk.Executor = insightExecutor(s)
	}
	if sk, ok := s.Skills.Get("chart_design"); ok {
		sk.Executor = chartDesignExecutor(s)
	}
	if sk, ok := s.Skills.Get("metric_compute"); ok {
		sk.Executor = metricComputeExecutor(s)
	}
	for name, exec := range domainExecutors(s) {
		if sk, ok := s.Skills.Get(name); ok {
			sk.Executor = exec
		}
	}
}

// queryExecutor runs a validated read-only statement through the shared query
// engine, so取数 inherits RBAC, RLS, row limits and auditing.
func queryExecutor(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			SQL       string `json:"sql"`
			DatasetID int    `json:"dataset_id"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.DatasetID <= 0 {
			return nil, errors.New("dataset_id is required")
		}
		if s.Queries == nil {
			return nil, errors.New("query engine is unavailable")
		}
		// Canonical form first, then guard, then execute: what was approved is
		// exactly what runs, and the SQL the user sees is reproducible.
		statement, err := s.prepareAgentSQL(ctx, in.DatasetID, in.SQL)
		if err != nil {
			return nil, err
		}

		res, err := s.Queries.ExecuteReadOnlySQL(ctx, registry.UserIDFrom(ctx), statement, queries.ReadOnlyOptions{
			DatasetID: in.DatasetID,
			RowLimit:  s.Config.AIQueryMaxRows,
			// Only the compliance officer (admin) sees unmasked values; everyone
			// else gets the same masks the chart path applies.
			ExemptMasking: isAdminFrom(ctx),
		})
		if err != nil {
			return nil, err
		}
		out := map[string]any{
			// sql is the statement the engine actually ran (the executor wraps it
			// in a row-limit subquery); statement is the canonical form the model
			// authored, which is what a replay must re-execute.
			"sql":       res.SQL,
			"statement": statement,
			"rows":      res.Data,
			"colnames":  res.ColNames,
			"rowcount":  res.RowCount,
			"truncated": res.Truncated,
			"query_id":  res.QueryID,
		}
		if len(res.MaskedColumns) > 0 {
			// Tell the model the truth so it can say "值已脱敏" instead of
			// treating "138****0000" as a data quality problem.
			out["masked_columns"] = res.MaskedColumns
			out["masking_note"] = "这些列的值已按合规规则脱敏，可用于计数/分组，不可用于识别个人"
		}
		if !res.SessionReadOnly {
			out["read_only_session"] = false
		}
		return &registry.ToolResult{
			Output:     out,
			SideEffect: registry.Read,
			DurationMS: res.DurationMS,
		}, nil
	}
}

// isAdminFrom reports whether the acting user is an admin (compliance officer),
// the only role exempt from column masking.
func isAdminFrom(ctx context.Context) bool {
	perms, _ := security.PermissionsFrom(ctx)
	return perms != nil && perms.Admin()
}

// schemaReader returns a dataset's columns and metrics (the取数 schema the
// Agent needs before it can author SQL).
//
// Two exposure rules apply here, and they are the first line of W-01:
//   - a column with agent_visible=false is not returned at all, so the model
//     cannot name it in SQL (the guard also rejects it if it tries);
//   - a masked column is returned with `masked: true` and a note, so the model
//     knows values will arrive as 138****0000 and does not treat that as a data
//     quality problem.
//
// The `compact` field is the retrieval-friendly text form (schema_context.go):
// small datasets get everything, wide ones get the columns relevant to the
// question.
func schemaReader(s *Service) registry.ToolExecutor {
	return func(ctx context.Context, args json.RawMessage) (*registry.ToolResult, error) {
		var in struct {
			DatasetID  int    `json:"dataset_id"`
			DatabaseID int    `json:"database_id"`
			Schema     string `json:"schema"`
			Table      string `json:"table"`
			Question   string `json:"question"`
		}
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
		if in.DatasetID <= 0 {
			return nil, errors.New("dataset_id is required")
		}

		ds, err := loadDatasetSchema(ctx, s, in.DatasetID)
		if err != nil {
			return nil, err
		}

		cols := make([]map[string]any, 0, len(ds.Columns))
		for _, c := range ds.Columns {
			entry := map[string]any{
				"name":         c.ColumnName,
				"verbose_name": c.VerboseName,
				"type":         c.Type,
				"is_dttm":      c.IsDttm,
				"groupby":      c.Groupby,
				"filterable":   c.Filterable,
				"description":  c.Description,
			}
			if rule := queries.EffectiveMaskRule(c); rule.Type != "" {
				entry["masked"] = true
				entry["mask_note"] = "值将被脱敏（仅可用于计数/分组，不可用于明细或识别个人）"
			}
			cols = append(cols, entry)
		}
		mts := make([]map[string]any, 0, len(ds.Metrics))
		for _, m := range ds.Metrics {
			mts = append(mts, map[string]any{
				"name":         m.MetricName,
				"verbose_name": m.VerboseName,
				"type":         m.MetricType,
				"expression":   m.Expression,
				"description":  m.Description,
			})
		}

		compact, sliced := ds.compactFor(in.Question, schemaCompactThreshold(s))
		out := map[string]any{
			"dataset_id":          ds.Table.ID,
			"table_name":          ds.Table.TableNameStr,
			"schema":              ds.Table.Schema,
			"database_id":         ds.Table.DatabaseID,
			"main_dttm_col":       ds.Table.MainDttmCol,
			"columns":             cols,
			"metrics":             mts,
			"compact":             compact,
			"hidden_column_count": ds.HiddenCount,
		}
		if sliced {
			out["compact_sliced"] = true
			out["compact_note"] = "列较多，compact 只包含与问题最相关的列；如需更多列请用具体的 question 再查"
		}
		if len(cols) == 0 {
			// Distinguish "no columns synced yet" from "columns exist but the
			// query failed": the model must ask for a sync, not guess names.
			out["warning"] = "数据集尚无已同步的可对 Agent 可见的列，请先在数据集页执行「同步列」并检查 Agent 可见性"
		}
		return &registry.ToolResult{Output: out, SideEffect: registry.Read}, nil
	}
}

// schemaCompactThreshold resolves the retrieval threshold from config.
func schemaCompactThreshold(s *Service) int {
	if s.Config != nil && s.Config.AISchemaCompactThreshold > 0 {
		return s.Config.AISchemaCompactThreshold
	}
	return DefaultSchemaCompactThreshold
}
