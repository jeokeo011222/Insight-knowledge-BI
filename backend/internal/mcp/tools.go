package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// tools.go defines what an external agent can do through this bridge.
//
// The set is deliberately small and layered, in the order a caller should use
// it: find a dataset → read its schema and semantic model → plan → run. Two
// tools are read paths (semantic_query / run_sql) and neither accepts a
// mutation; there is no "write data" or "create chart" tool on purpose, because
// an MCP server that can change the BI estate needs a confirmation UI we do not
// have yet.
//
// dry_plan exists because the expensive mistake here is not a wrong answer, it
// is running a query that was never going to be valid. Planning returns the SQL
// and, crucially, the filters the semantic layer injected — which is the part
// a caller cannot predict.

// NewServer builds the BI bridge.
func NewServer(c *Client, version string) *Server {
	return &Server{
		Name:    "superset-bi",
		Version: version,
		Tools: []Tool{
			listDatasetsTool(c),
			describeDatasetTool(c),
			dryPlanTool(c),
			semanticQueryTool(c),
			runSQLTool(c),
		},
	}
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

// ------------------------------------------------------------ list_datasets --

func listDatasetsTool(c *Client) Tool {
	return Tool{
		Name: "list_datasets",
		Description: "List the datasets the caller can read. A dataset is the unit everything else " +
			"hangs off: it maps one warehouse table (or a saved SQL view) to the columns and metrics " +
			"the semantic layer knows about. Returns id, table name, schema and description.",
		InputSchema: schema(map[string]any{
			"search":    prop("string", "Optional substring of the table name."),
			"page":      prop("integer", "Page number, 1-based. Default 1."),
			"page_size": prop("integer", "Rows per page, max 100. Default 50."),
		}),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				Search   string `json:"search"`
				Page     int    `json:"page"`
				PageSize int    `json:"page_size"`
			}
			if err := unmarshalArgs(raw, &in); err != nil {
				return nil, err
			}
			if in.PageSize <= 0 || in.PageSize > 100 {
				in.PageSize = 50
			}
			if in.Page <= 0 {
				in.Page = 1
			}
			q := url.Values{}
			q.Set("page", strconv.Itoa(in.Page))
			q.Set("page_size", strconv.Itoa(in.PageSize))
			if strings.TrimSpace(in.Search) != "" {
				q.Set("q", strings.TrimSpace(in.Search))
			}
			body, err := c.GetEnvelope(ctx, "/dataset/", q)
			if err != nil {
				return nil, err
			}
			var page struct {
				Count  int              `json:"count"`
				Result []map[string]any `json:"result"`
			}
			if err := json.Unmarshal(body, &page); err != nil {
				return nil, fmt.Errorf("unexpected dataset list payload: %w", err)
			}
			out := make([]map[string]any, 0, len(page.Result))
			for _, d := range page.Result {
				out = append(out, map[string]any{
					"id":          d["id"],
					"table_name":  d["table_name"],
					"schema":      d["schema"],
					"database":    databaseName(d),
					"kind":        d["kind"],
					"description": d["description"],
				})
			}
			return map[string]any{"count": page.Count, "page": in.Page, "datasets": out}, nil
		},
	}
}

func databaseName(d map[string]any) string {
	db, ok := d["database"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := db["database_name"].(string)
	return name
}

// -------------------------------------------------------- describe_dataset --

func describeDatasetTool(c *Client) Tool {
	return Tool{
		Name: "describe_dataset",
		Description: "Describe one dataset: columns (with type, whether they are masked or hidden from " +
			"the agent), metrics, and every semantic model with its fields, relationships and " +
			"validation issues. Call this before writing any query. Columns with agent_visible=false " +
			"are excluded from the answer and are rejected by the SQL guard.",
		InputSchema: schema(map[string]any{
			"dataset_id":     prop("integer", "Dataset id from list_datasets."),
			"table_name":     prop("string", "Table name, as an alternative to dataset_id."),
			"include_fields": prop("boolean", "Include semantic model fields (default true)."),
		}),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				DatasetID     int    `json:"dataset_id"`
				TableName     string `json:"table_name"`
				IncludeFields *bool  `json:"include_fields"`
			}
			if err := unmarshalArgs(raw, &in); err != nil {
				return nil, err
			}
			includeFields := in.IncludeFields == nil || *in.IncludeFields
			id, err := resolveDataset(ctx, c, in.DatasetID, in.TableName)
			if err != nil {
				return nil, err
			}
			body, err := c.Get(ctx, "/dataset/"+strconv.Itoa(id), nil)
			if err != nil {
				return nil, err
			}
			var ds map[string]any
			if err := json.Unmarshal(body, &ds); err != nil {
				return nil, fmt.Errorf("unexpected dataset payload: %w", err)
			}
			if _, ok := ds["columns"]; !ok {
				if inner, ok := ds["result"].(map[string]any); ok {
					ds = inner
				}
			}
			out := map[string]any{
				"dataset": map[string]any{
					"id": ds["id"], "table_name": ds["table_name"], "schema": ds["schema"],
					"database": databaseName(ds), "kind": ds["kind"],
					"description": ds["description"], "main_dttm_col": ds["main_dttm_col"],
					"sql": ds["sql"],
				},
				"columns": visibleColumns(ds["columns"]),
				"metrics": simplifyMetrics(ds["metrics"]),
			}
			models, err := semanticModels(ctx, c, id, includeFields)
			if err != nil {
				return nil, err
			}
			out["semantic_models"] = models
			if len(models) == 0 {
				out["hint"] = "该数据集还没有语义模型：可以用 run_sql 直接查，但口径（默认过滤、指标定义）不会被自动应用"
			}
			return out, nil
		},
	}
}

// visibleColumns drops agent-hidden columns: a caller that cannot name them in
// SQL must not see them in the description either, otherwise the two paths
// disagree about what exists.
func visibleColumns(raw any) []map[string]any {
	list, _ := raw.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		col, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if visible, ok := col["agent_visible"].(bool); ok && !visible {
			continue
		}
		out = append(out, map[string]any{
			"name": col["column_name"], "type": col["type"],
			"is_dttm": col["is_dttm"], "groupby": col["groupby"],
			"masked": col["masked"], "pii_level": col["pii_level"],
			"description": col["description"], "verbose_name": col["verbose_name"],
			"expression": col["expression"],
		})
	}
	return out
}

func simplifyMetrics(raw any) []map[string]any {
	list, _ := raw.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"name": m["metric_name"], "expression": m["expression"],
			"type": m["metric_type"], "description": m["description"],
			"currency": m["currency"],
		})
	}
	return out
}

func semanticModels(ctx context.Context, c *Client, datasetID int, includeFields bool) ([]map[string]any, error) {
	body, err := c.Get(ctx, "/dataset/"+strconv.Itoa(datasetID)+"/semantic/models", nil)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("unexpected semantic model payload: %w", err)
	}
	if len(rows) == 0 {
		// Some deployments envelope lists even when empty; tolerate both shapes
		// rather than reporting "broken payload" for a dataset with no model.
		var wrapped struct {
			Result []map[string]any `json:"result"`
		}
		if json.Unmarshal(body, &wrapped) == nil {
			rows = wrapped.Result
		}
	}
	out := make([]map[string]any, 0, len(rows))
	for i, row := range rows {
		entry := map[string]any{
			"name": row["name"], "status": row["status"], "version": row["version"],
			"primary_key": row["primary_key"], "description": row["description"],
			"manifest_hash": row["manifest_hash"],
		}
		// Field detail needs one more call per model. Datasets normally have a
		// single model; the cap keeps a pathological case from fanning out.
		if includeFields && i < 3 {
			id, _ := row["id"].(float64)
			if id > 0 {
				if detail, derr := c.Get(ctx, "/semantic/model/"+strconv.Itoa(int(id)), nil); derr == nil {
					var view map[string]any
					if json.Unmarshal(detail, &view) == nil {
						entry["publishable"] = view["publishable"]
						entry["fields"] = semanticFields(view["manifest"])
						entry["relationships"] = view["relationships"]
						entry["issues"] = issuesOf(view["issues"])
					}
				}
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// semanticFields flattens the compiled manifest into the two things a caller
// needs: what may be asked for, and what it resolves to.
func semanticFields(manifest any) []map[string]any {
	m, ok := manifest.(map[string]any)
	if !ok {
		return nil
	}
	raw, _ := m["fields"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		f, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if hidden, ok := f["hidden"].(bool); ok && hidden {
			continue
		}
		out = append(out, map[string]any{
			"name": f["name"], "kind": f["kind"], "expression": f["expression"],
			"type": f["type"], "unit": f["unit"], "description": f["description"],
			"enum_values": f["enum_values"], "synonyms": f["synonyms"],
			"is_dttm": f["is_dttm"], "default_agg": f["default_agg"],
		})
	}
	return out
}

// issuesOf keeps only the fields that change a decision (severity + code + the
// offending field); the full hint text is for the operator UI.
func issuesOf(raw any) []map[string]any {
	list, _ := raw.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		it, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"severity": it["severity"], "code": it["code"],
			"field": it["field"], "message": it["message"],
		})
	}
	return out
}

func resolveDataset(ctx context.Context, c *Client, id int, tableName string) (int, error) {
	if id > 0 {
		return id, nil
	}
	tableName = strings.TrimSpace(tableName)
	if tableName == "" {
		return 0, fmt.Errorf("either dataset_id or table_name is required")
	}
	q := url.Values{}
	q.Set("q", tableName)
	q.Set("page_size", "20")
	body, err := c.GetEnvelope(ctx, "/dataset/", q)
	if err != nil {
		return 0, err
	}
	var page struct {
		Result []map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return 0, err
	}
	for _, d := range page.Result {
		if name, _ := d["table_name"].(string); strings.EqualFold(name, tableName) {
			if fid, ok := d["id"].(float64); ok {
				return int(fid), nil
			}
		}
	}
	if len(page.Result) == 1 {
		if fid, ok := page.Result[0]["id"].(float64); ok {
			return int(fid), nil
		}
	}
	return 0, fmt.Errorf("no dataset named %q; call list_datasets to see the available ones", tableName)
}

// ------------------------------------------------------------- query tools --

// semanticQueryArgs is the tool-facing shape; it is forwarded verbatim to
// POST /dataset/{id}/semantic/query, which owns the validation. Duplicating the
// validation here would create a second opinion about what a valid field is.
func semanticQueryArgs() map[string]any {
	return map[string]any{
		"dataset_id": prop("integer", "Dataset id from list_datasets."),
		"model":      prop("string", "Semantic model name. Omit to use the published model."),
		"metrics":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Measure field names. Omit for row count."},
		"dimensions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Group-by field names."},
		"time": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"field": prop("string", "Time field name."),
				"range": prop("string", "today | yesterday | last_7_days | last_30_days | last_90_days | last_12_months | this_year | last_month | last_quarter | '<start> : <end>'"),
				"grain": prop("string", "hour | day | week | month | quarter | year"),
			},
			"additionalProperties": false,
		},
		"filters": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": prop("string", "Field name."),
					"op":    prop("string", "eq | ne | gt | gte | lt | lte | in | not_in | like | ilike | is_null | is_not_null"),
					"value": map[string]any{"description": "Comparison value; array for in/not_in."},
				},
				"required":             []string{"field", "op"},
				"additionalProperties": false,
			},
		},
		"order": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"field": prop("string", "Field name."),
					"desc":  prop("boolean", "Descending order."),
				},
				"required":             []string{"field"},
				"additionalProperties": false,
			},
		},
		"limit": prop("integer", "Row limit; the server cap still applies."),
	}
}

func semanticQueryTool(c *Client) Tool {
	return Tool{
		Name: "semantic_query",
		Description: "Run a structured query over a dataset's semantic model: metrics, dimensions, a " +
			"time range and filters. No SQL. Business rules that the model declares (for example " +
			"\"exclude soft-deleted rows\") are added automatically and reported in applied_filters. " +
			"Use dry_plan first when unsure.",
		InputSchema: schema(semanticQueryArgs(), "dataset_id"),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			id, body, err := queryBody(raw)
			if err != nil {
				return nil, err
			}
			return c.Post(ctx, "/dataset/"+strconv.Itoa(id)+"/semantic/query", body)
		},
	}
}

func dryPlanTool(c *Client) Tool {
	args := semanticQueryArgs()
	args["sql"] = prop("string", "SQL to validate against the dataset (alternative to the structured form). Nothing is executed, but the answer is the guard's, i.e. it is not a plan of what SQL would return.")
	return Tool{
		Name: "dry_plan",
		Description: "Plan without executing. With metrics/dimensions/time/filters it returns the SQL the " +
			"semantic layer would run plus every filter it injected; with sql it returns the guard's verdict " +
			"(table/column whitelist, banned functions, wildcard projection). Nothing is sent to the warehouse.",
		InputSchema: schema(args, "dataset_id"),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			id, body, err := queryBody(raw)
			if err != nil {
				return nil, err
			}
			sqlText, _ := body["sql"].(string)
			if strings.TrimSpace(sqlText) != "" {
				verdict, err := c.Post(ctx, "/dataset/"+strconv.Itoa(id)+"/query/validate", map[string]any{"sql": sqlText})
				if err != nil {
					return nil, err
				}
				var out map[string]any
				if err := json.Unmarshal(verdict, &out); err != nil {
					return nil, err
				}
				out["note"] = "仅做了守卫校验，未执行；表/列白名单、函数黑名单与通配符投影均已检查"
				return out, nil
			}
			body["dry_run"] = true
			plan, err := c.Post(ctx, "/dataset/"+strconv.Itoa(id)+"/semantic/query", body)
			if err != nil {
				return nil, err
			}
			var out map[string]any
			if err := json.Unmarshal(plan, &out); err != nil {
				return nil, err
			}
			out["note"] = "未执行；applied_filters 里的每一条都是语义层自动加的约束"
			return out, nil
		},
	}
}

func runSQLTool(c *Client) Tool {
	return Tool{
		Name: "run_sql",
		Description: "Run read-only SQL against one dataset. The statement is checked against the dataset's " +
			"table/column whitelist and banned-function list, runs in a read-only session, and is subject to " +
			"row-level security, column masking and a row cap. Prefer semantic_query when the question fits it: " +
			"free SQL does not get the model's default filters for free.",
		InputSchema: schema(map[string]any{
			"dataset_id": prop("integer", "Dataset id from list_datasets."),
			"table_name": prop("string", "Table name, as an alternative to dataset_id."),
			"sql":        prop("string", "A single read-only SELECT statement."),
			"limit":      prop("integer", "Row limit; the server cap still applies."),
		}, "sql"),
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				DatasetID int    `json:"dataset_id"`
				TableName string `json:"table_name"`
				SQL       string `json:"sql"`
				Limit     int    `json:"limit"`
			}
			if err := unmarshalArgs(raw, &in); err != nil {
				return nil, err
			}
			id, err := resolveDataset(ctx, c, in.DatasetID, in.TableName)
			if err != nil {
				return nil, err
			}
			body := map[string]any{"sql": in.SQL}
			if in.Limit > 0 {
				body["limit"] = in.Limit
			}
			return c.Post(ctx, "/dataset/"+strconv.Itoa(id)+"/query", body)
		},
	}
}

// queryBody reads dataset_id off the arguments and returns the rest untouched.
func queryBody(raw json.RawMessage) (int, map[string]any, error) {
	var body map[string]any
	if err := unmarshalArgs(raw, &body); err != nil {
		return 0, nil, err
	}
	id, err := numberToInt(body["dataset_id"])
	if err != nil || id <= 0 {
		return 0, nil, fmt.Errorf("dataset_id is required")
	}
	return id, body, nil
}

func numberToInt(v any) (int, error) {
	switch n := v.(type) {
	case float64:
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		return int(i), err
	case int:
		return n, nil
	}
	return 0, fmt.Errorf("not a number")
}

func unmarshalArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}
