package queries

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	apperrors "superset/internal/pkg/errors"
)

// DefaultRowLimit mirrors superset's config ROW_LIMIT (10k rows per query).
const DefaultRowLimit = 10000

// DTTMColumnAlias is the alias superset uses for the time grain column.
const DTTMColumnAlias = "__timestamp"

// Filter is a single simple filter clause ("col op val").
type Filter struct {
	Col string `json:"col"`
	Op  string `json:"op"`
	Val any    `json:"val"`
}

// AdhocMetric mirrors superset.utils.core.AdhocMetric.
type AdhocMetric struct {
	Expression     string         `json:"expression,omitempty"`
	SQLExpression  string         `json:"sqlExpression,omitempty"`
	Column         map[string]any `json:"column,omitempty"`
	Aggregate      string         `json:"aggregate,omitempty"`
	Label          string         `json:"label,omitempty"`
	HasCustomLabel bool           `json:"hasCustomLabel,omitempty"`
	OptionName     string         `json:"optionName,omitempty"`
}

// AdhocColumn mirrors superset.utils.core.AdhocColumn.
type AdhocColumn struct {
	ColumnName     string `json:"column_name,omitempty"`
	Label          string `json:"label,omitempty"`
	SQLExpression  string `json:"sqlExpression,omitempty"`
	HasCustomLabel bool   `json:"hasCustomLabel,omitempty"`
	ExpressionType string `json:"expressionType,omitempty"`
}

// QueryObject mirrors superset/common/query_object.py:QueryObject. Only the
// fields the SQL builder needs are typed; unknown client fields are ignored.
type QueryObject struct {
	DatabaseID          int              `json:"database_id,omitempty"`
	Schema              string           `json:"schema,omitempty"`
	SQL                 string           `json:"sql,omitempty"`
	Catalog             string           `json:"catalog,omitempty"`
	Metrics             []any            `json:"metrics,omitempty"`
	Groupby             []any            `json:"groupby,omitempty"`
	Columns             []any            `json:"columns,omitempty"`
	FromDttm            string           `json:"from_dttm,omitempty"`
	ToDttm              string           `json:"to_dttm,omitempty"`
	Granularity         string           `json:"granularity,omitempty"`
	Filters             []Filter         `json:"filters,omitempty"`
	Extras              map[string]any   `json:"extras,omitempty"`
	RowLimit            int              `json:"row_limit,omitempty"`
	RowOffset           int              `json:"row_offset,omitempty"`
	SeriesLimit         int              `json:"series_limit,omitempty"`
	Orderby             [][2]any         `json:"orderby,omitempty"`
	PostProcessing      []map[string]any `json:"post_processing,omitempty"`
	IsTimeseries        bool             `json:"is_timeseries,omitempty"`
	ApplySeriesLimit    bool             `json:"apply_series_limit,omitempty"`
	TimeShift           string           `json:"time_shift,omitempty"`
	AnnotationLayers    []any            `json:"annotation_layers,omitempty"`
	IsRowCountApprox    bool             `json:"is_rowcount,omitempty"`
	InstantaneousMethod string           `json:"instantaneous_method,omitempty"`
}

// QueryContext mirrors superset/common/query_context.py:QueryContext.
type QueryContext struct {
	Datasource         map[string]any `json:"datasource"`
	Queries            []QueryObject  `json:"queries"`
	FormData           map[string]any `json:"form_data"`
	ResultFormat       string         `json:"result_format,omitempty"`
	ResultType         string         `json:"result_type,omitempty"`
	Force              bool           `json:"force,omitempty"`
	CustomCacheTimeout *int           `json:"custom_cache_timeout,omitempty"`

	// Raw keeps the client payload so the response can echo it back verbatim.
	Raw json.RawMessage `json:"-"`
}

// ParseQueryContext decodes a client supplied query context. It is deliberately
// forgiving: older clients only send form_data while newer ones always send
// "queries". When no query objects are present one is synthesised from
// form_data so that both shapes work.
func ParseQueryContext(raw json.RawMessage) (*QueryContext, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, apperrors.BadRequest("Query context is required")
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, apperrors.BadRequest("Invalid query context: %s", err.Error())
	}

	qc := &QueryContext{
		Datasource:         parseDatasourceRef(root["datasource"]),
		FormData:           asStringMap(root["form_data"]),
		ResultFormat:       firstString(root["result_format"]),
		ResultType:         firstString(root["result_type"]),
		Force:              asBool(root["force"]),
		CustomCacheTimeout: asIntPtr(root["custom_cache_timeout"]),
		Raw:                raw,
	}
	if qc.FormData == nil {
		qc.FormData = map[string]any{}
	}
	if qc.Datasource == nil {
		qc.Datasource = parseDatasourceRef(qc.FormData["datasource"])
	}

	if list, ok := root["queries"].([]any); ok {
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			qo, err := parseQueryObject(m)
			if err != nil {
				return nil, err
			}
			qc.Queries = append(qc.Queries, *qo)
		}
	}

	if len(qc.Queries) == 0 {
		qc.Queries = []QueryObject{*queryObjectFromFormData(qc.FormData)}
	}
	if qc.ResultFormat == "" {
		qc.ResultFormat = "json"
	}
	if qc.ResultType == "" {
		qc.ResultType = "full"
	}
	return qc, nil
}

// Echo renders the context back to the client in the superset wire format.
func (qc *QueryContext) Echo() map[string]any {
	if qc == nil {
		return map[string]any{}
	}
	out := map[string]any{
		"datasource":    qc.Datasource,
		"queries":       qc.Queries,
		"form_data":     qc.FormData,
		"result_format": qc.ResultFormat,
		"result_type":   qc.ResultType,
		"force":         qc.Force,
	}
	if qc.CustomCacheTimeout != nil {
		out["custom_cache_timeout"] = *qc.CustomCacheTimeout
	}
	return out
}

// DatasourceID returns the datasource id and type from the context.
func (qc *QueryContext) DatasourceID() (int, string) {
	if qc == nil {
		return 0, ""
	}
	return datasourceID(qc.Datasource)
}

// parseQueryObject maps the loose JSON object onto the typed struct.
func parseQueryObject(m map[string]any) (*QueryObject, error) {
	qo := &QueryObject{
		DatabaseID:          asInt(m["database_id"]),
		Schema:              firstString(m["schema"]),
		SQL:                 firstString(m["sql"]),
		Catalog:             firstString(m["catalog"]),
		Granularity:         firstString(m["granularity"], m["granularity_sqla"], m["time_grain_sqla"]),
		FromDttm:            firstString(m["from_dttm"]),
		ToDttm:              firstString(m["to_dttm"]),
		Extras:              asStringMap(m["extras"]),
		RowLimit:            asInt(m["row_limit"]),
		RowOffset:           asInt(m["row_offset"]),
		SeriesLimit:         asInt(m["series_limit"], m["timeseries_limit"]),
		IsTimeseries:        asBool(m["is_timeseries"]),
		ApplySeriesLimit:    asBool(m["apply_series_limit"]) || asInt(m["timeseries_limit"]) > 0,
		TimeShift:           firstString(m["time_shift"]),
		IsRowCountApprox:    asBool(m["is_rowcount"]),
		InstantaneousMethod: firstString(m["instantaneous_method"]),
	}
	qo.Metrics = asAnySlice(m["metrics"])
	qo.Groupby = asAnySlice(m["groupby"])
	qo.Columns = asAnySlice(m["columns"])
	qo.AnnotationLayers = asAnySlice(m["annotation_layers"])
	qo.Orderby = parseOrderby(m["orderby"])
	qo.PostProcessing = asMapSlice(m["post_processing"])
	qo.Filters = parseFilters(m["filters"], m["adhoc_filters"])

	if rangeRaw := firstString(m["time_range"]); rangeRaw != "" && qo.FromDttm == "" && qo.ToDttm == "" {
		from, to := SplitTimeRange(rangeRaw)
		qo.FromDttm, qo.ToDttm = from, to
	}
	if qo.Extras != nil {
		if v, ok := qo.Extras["time_range"]; ok {
			from, to := SplitTimeRange(fmt.Sprint(v))
			if qo.FromDttm == "" {
				qo.FromDttm = from
			}
			if qo.ToDttm == "" {
				qo.ToDttm = to
			}
		}
		if v, ok := qo.Extras["from_dttm"]; ok && qo.FromDttm == "" {
			qo.FromDttm = fmt.Sprint(v)
		}
		if v, ok := qo.Extras["to_dttm"]; ok && qo.ToDttm == "" {
			qo.ToDttm = fmt.Sprint(v)
		}
	}
	if qo.Granularity == "" {
		if v, ok := qo.Extras["time_grain_sqla"]; ok {
			qo.Granularity = fmt.Sprint(v)
		}
	}
	return qo, nil
}

// queryObjectFromFormData builds a query object from the legacy form_data only
// payload (pre-query-context clients and GET /chart/{pk}/data).
func queryObjectFromFormData(fd map[string]any) *QueryObject {
	qo := &QueryObject{
		Extras:           map[string]any{},
		IsTimeseries:     asBool(fd["is_timeseries"]),
		RowLimit:         asInt(fd["row_limit"]),
		RowOffset:        asInt(fd["row_offset"]),
		SeriesLimit:      asInt(fd["series_limit"], fd["timeseries_limit"]),
		ApplySeriesLimit: asBool(fd["apply_series_limit"]),
		TimeShift:        firstString(fd["time_shift"]),
		Granularity:      firstString(fd["granularity_sqla"], fd["granularity"]),
	}
	qo.Metrics = asAnySlice(fd["metrics"])
	qo.Groupby = asAnySlice(fd["groupby"])
	qo.Columns = asAnySlice(fd["columns"])
	// Annotation layers live in form_data for saved charts and dashboard tiles;
	// carrying them onto the query object keeps the downstream reader in one
	// place instead of re-parsing the form.
	qo.AnnotationLayers = asAnySlice(fd["annotation_layers"])
	qo.Orderby = parseOrderby(fd["orderby"])
	qo.Filters = parseFilters(fd["filters"], fd["adhoc_filters"])

	if extras := asStringMap(fd["extras"]); extras != nil {
		for k, v := range extras {
			qo.Extras[k] = v
		}
	}
	if grain := firstString(fd["time_grain_sqla"]); grain != "" {
		qo.Extras["time_grain_sqla"] = grain
	}
	if from, to := SplitTimeRange(firstString(fd["time_range"])); from != "" || to != "" {
		qo.FromDttm, qo.ToDttm = from, to
	}
	// is_timeseries follows the visualisation type, never the mere presence of
	// a temporal column: heatmaps, pivots and pies keep their own x axis even
	// when the dataset has a time column selected.
	if !qo.IsTimeseries && isTimeSeriesViz(firstString(fd["viz_type"])) {
		qo.IsTimeseries = true
	}
	return qo
}

func isTimeSeriesViz(viz string) bool {
	if strings.Contains(viz, "timeseries") {
		return true
	}
	switch viz {
	case "line", "area", "bar", "dist_bar", "scatter", "bubble",
		"big_number", "big_number_total", "compare", "time_pivot",
		"heatmap_v2", "cal_heatmap", "calendar_heatmap":
		return true
	}
	return false
}

// SplitTimeRange splits a superset "from : to" range. A lone value is treated
// as the lower bound, matching the Python implementation.
func SplitTimeRange(raw string) (string, string) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "no filter") {
		return "", ""
	}
	if i := strings.Index(s, " : "); i >= 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+3:])
	}
	return s, ""
}

// parseFilters normalises both "filters" and "adhoc_filters" entries. Only
// SIMPLE clauses become SQL filters; adhoc SQL expressions live in
// extras.where in the Python implementation.
func parseFilters(sets ...any) []Filter {
	var out []Filter
	for _, set := range sets {
		list, ok := set.([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			switch v := item.(type) {
			case map[string]any:
				col := firstString(v["col"], v["column_name"])
				op := firstString(v["op"], v["operator"])
				if col == "" || op == "" {
					continue
				}
				out = append(out, Filter{Col: col, Op: op, Val: v["val"]})
			}
		}
	}
	return out
}

func parseOrderby(v any) [][2]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([][2]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.([]any)
		if !ok || len(entry) == 0 {
			continue
		}
		var dir any = true
		if len(entry) > 1 {
			dir = entry[1]
		}
		out = append(out, [2]any{entry[0], dir})
	}
	return out
}

// parseDatasourceRef accepts {"id":1,"type":"table"}, 1, "1" or "1__table".
func parseDatasourceRef(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			out[k] = val
		}
		if _, ok := out["id"]; !ok {
			if id := asInt(t["datasource_id"]); id > 0 {
				out["id"] = id
			}
		}
		if _, ok := out["type"]; !ok {
			if typ := firstString(t["type"], t["datasource_type"]); typ != "" {
				out["type"] = typ
			}
		}
		return out
	case string:
		return parseDatasourceString(t)
	case float64, int, int64, json.Number:
		id := asInt(t)
		if id > 0 {
			return map[string]any{"id": id, "type": "table"}
		}
	}
	return nil
}

func parseDatasourceString(s string) map[string]any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if i := strings.Index(s, "__"); i > 0 {
		id, err := strconv.Atoi(s[:i])
		if err == nil && id > 0 {
			return map[string]any{"id": id, "type": s[i+2:]}
		}
	}
	if id, err := strconv.Atoi(s); err == nil && id > 0 {
		return map[string]any{"id": id, "type": "table"}
	}
	return nil
}

func datasourceID(ds map[string]any) (int, string) {
	if ds == nil {
		return 0, ""
	}
	id := asInt(ds["id"], ds["datasource_id"])
	typ := firstString(ds["type"], ds["datasource_type"])
	return id, typ
}

// Small JSON coercion helpers ------------------------------------------------

func asStringMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

func asMapSlice(v any) []map[string]any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func asAnySlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case nil:
		return nil
	default:
		return []any{t}
	}
}

func asInt(vals ...any) int {
	for _, v := range vals {
		switch t := v.(type) {
		case nil:
			continue
		case int:
			return t
		case int64:
			return int(t)
		case float64:
			return int(t)
		case float32:
			return int(t)
		case json.Number:
			if n, err := t.Int64(); err == nil {
				return int(n)
			}
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
				return n
			}
		}
	}
	return 0
}

func asIntPtr(v any) *int {
	switch t := v.(type) {
	case nil:
		return nil
	case float64:
		n := int(t)
		return &n
	case int:
		n := t
		return &n
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return &n
		}
	}
	return nil
}

func asBool(vals ...any) bool {
	for _, v := range vals {
		switch t := v.(type) {
		case bool:
			return t
		case string:
			if strings.EqualFold(t, "true") || t == "1" {
				return true
			}
			if strings.EqualFold(t, "false") || t == "0" {
				return false
			}
		case float64:
			return t != 0
		case int:
			return t != 0
		}
	}
	return false
}

func firstString(vals ...any) string {
	for _, v := range vals {
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(t, 'f', -1, 64)
		case int:
			return strconv.Itoa(t)
		case json.Number:
			return t.String()
		}
	}
	return ""
}
