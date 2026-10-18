package queries

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"superset/internal/connectors"
	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
)

// DatasetInfo bundles a physical/virtual dataset with its columns and metrics.
type DatasetInfo struct {
	Table   *models.SqlaTable
	Columns []models.TableColumn
	Metrics []models.SqlMetric
}

// BuildOptions controls SQL generation for a single query object.
type BuildOptions struct {
	Timezone        string
	DefaultRowLimit int
	MaxRow          int
	Now             time.Time
	// RLSProvider returns the row level security clauses for a dataset.
	RLSProvider func(datasetID int) []string
}

// Projection is one entry of the generated SELECT list.
// Projection is one selected column. The JSON tags matter: this struct is
// returned verbatim by the semantic query API (dry runs) and the MCP bridge,
// so Go field names would leak into a public contract.
type Projection struct {
	Label      string `json:"label"`
	Expression string `json:"expression"`
	Type       string `json:"type"`
	IsMetric   bool   `json:"is_metric"`
}

// BuiltQuery is the result of BuildSQL.
type BuiltQuery struct {
	SQL            string
	Query          string
	TimeColumn     string
	Projections    []Projection
	AppliedFilters []map[string]any
}

// ApplyMaxRowLimit normalises a requested row limit:
//
//	0      -> DefaultRowLimit
//	< 0    -> unlimited (0 is returned, the caller omits LIMIT)
//	> max  -> truncated to max
func ApplyMaxRowLimit(rowLimit, maxRow int) int {
	if rowLimit < 0 {
		return 0
	}
	if rowLimit == 0 {
		rowLimit = DefaultRowLimit
	}
	if maxRow > 0 && rowLimit > maxRow {
		rowLimit = maxRow
	}
	return rowLimit
}

// BuildSQL renders the SELECT statement for a query object. The returned
// "Query" is the statement shown to the user (superset shows the raw SQL).
func BuildSQL(ds *DatasetInfo, q *QueryObject, spec connectors.EngineSpec, opts BuildOptions) (*BuiltQuery, error) {
	if ds == nil || ds.Table == nil {
		return nil, apperrors.BadRequest("Dataset is required")
	}
	if spec == nil {
		return nil, apperrors.BadRequest("Unsupported database engine")
	}
	if q == nil {
		return nil, apperrors.BadRequest("Query object is required")
	}
	if opts.DefaultRowLimit <= 0 {
		opts.DefaultRowLimit = DefaultRowLimit
	}
	if opts.MaxRow <= 0 {
		opts.MaxRow = DefaultRowLimit
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	b := &sqlBuilder{ds: ds, q: q, spec: spec, opts: opts, labels: map[string]string{}}
	return b.build()
}

type sqlBuilder struct {
	ds     *DatasetInfo
	q      *QueryObject
	spec   connectors.EngineSpec
	opts   BuildOptions
	labels map[string]string

	selects []string
	projs   []Projection
	groupBy []string
	// timeExpr is the (possibly grained) expression used in SELECT/GROUP BY.
	timeExpr string
	// timeFilterExpr is the un-grained expression used for range filters so
	// that predicates stay sargable.
	timeFilterExpr string
}

func (b *sqlBuilder) build() (*BuiltQuery, error) {
	timeCol := b.timeColumn()
	grain := b.timeGrain()

	if timeCol != "" {
		expr := b.columnExpr(timeCol)
		if b.q.TimeShift != "" {
			shifted, err := ApplyTimeShift(b.spec, expr, b.q.TimeShift)
			if err != nil {
				return nil, apperrors.BadRequest("%s", err.Error())
			}
			expr = shifted
		}
		b.timeFilterExpr = expr
		// The grain is only meaningful for timeseries queries. Categorical
		// visualisations (heatmap, pivot, pie, table …) keep their own axes
		// even when the dataset has a temporal column and a grain selected.
		if grain != "" && b.q.IsTimeseries {
			ge, err := b.spec.TimeGrainExpr(expr, grain, b.opts.Timezone)
			if err != nil {
				return nil, apperrors.BadRequest("%s", err.Error())
			}
			expr = ge
		}
		b.timeExpr = expr
	}

	// Row count queries replace the projection with COUNT(*).
	if b.q.IsRowCountApprox {
		expr := "COUNT(*)"
		b.selects = append(b.selects, expr+" AS "+b.spec.QuoteIdentifier("count"))
		b.projs = append(b.projs, Projection{Label: "count", Expression: expr, Type: "INT", IsMetric: true})
		return b.finish()
	}

	if b.timeExpr != "" && b.q.IsTimeseries {
		b.addSelect(b.timeExpr, DTTMColumnAlias, "DATETIME", false)
		b.groupBy = append(b.groupBy, b.timeExpr)
	}

	for _, item := range b.q.Columns {
		expr, label, typ, err := b.columnItem(item)
		if err != nil {
			return nil, err
		}
		if expr == "" {
			continue
		}
		if b.addSelect(expr, label, typ, false) {
			b.groupBy = appendUnique(b.groupBy, expr)
		}
	}
	for _, item := range b.q.Groupby {
		expr, label, typ, err := b.columnItem(item)
		if err != nil {
			return nil, err
		}
		if expr == "" {
			continue
		}
		if b.addSelect(expr, label, typ, false) {
			b.groupBy = appendUnique(b.groupBy, expr)
		}
	}
	for _, item := range b.q.Metrics {
		expr, label, typ, err := b.metricItem(item)
		if err != nil {
			return nil, err
		}
		b.addSelect(expr, label, typ, true)
	}

	if len(b.selects) == 0 {
		return nil, apperrors.BadRequest("Query requires at least one column or metric")
	}

	return b.finish()
}

// finish assembles the final statement.
func (b *sqlBuilder) finish() (*BuiltQuery, error) {
	where, applied, err := b.whereClauses()
	if err != nil {
		return nil, err
	}
	from, err := b.fromClause()
	if err != nil {
		return nil, err
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(strings.Join(b.selects, ", "))
	sb.WriteString("\nFROM ")
	sb.WriteString(from)
	if len(where) > 0 {
		sb.WriteString("\nWHERE ")
		sb.WriteString(strings.Join(where, "\n  AND "))
	}
	if len(b.groupBy) > 0 {
		sb.WriteString("\nGROUP BY ")
		sb.WriteString(strings.Join(b.groupBy, ", "))
	}
	if having := b.havingClause(); having != "" {
		sb.WriteString("\nHAVING ")
		sb.WriteString(having)
	}
	if order := b.orderClause(); order != "" {
		sb.WriteString("\nORDER BY ")
		sb.WriteString(order)
	}
	if clause := b.limitClause(); clause != "" {
		sb.WriteString("\n")
		sb.WriteString(clause)
	}

	sqlText := sb.String()
	return &BuiltQuery{
		SQL:            sqlText,
		Query:          sqlText,
		TimeColumn:     b.timeExpr,
		Projections:    b.projs,
		AppliedFilters: applied,
	}, nil
}

// addSelect appends a projection once per label. It reports whether the entry
// was added (used to keep GROUP BY in sync).
func (b *sqlBuilder) addSelect(expr, label, typ string, isMetric bool) bool {
	if label == "" {
		label = expr
	}
	if prev, ok := b.labels[strings.ToLower(label)]; ok {
		// Same label already projected: keep the original expression so the
		// result shape stays stable.
		_ = prev
		return false
	}
	b.labels[strings.ToLower(label)] = expr
	b.selects = append(b.selects, expr+" AS "+b.spec.QuoteIdentifier(label))
	b.projs = append(b.projs, Projection{Label: label, Expression: expr, Type: typ, IsMetric: isMetric})
	return true
}

func appendUnique(in []string, v string) []string {
	for _, existing := range in {
		if existing == v {
			return in
		}
	}
	return append(in, v)
}

// timeColumn returns the dataset temporal column, falling back to the legacy
// granularity field when it holds a column name.
func (b *sqlBuilder) timeColumn() string {
	if col := strings.TrimSpace(b.ds.Table.MainDttmCol); col != "" {
		return col
	}
	g := strings.TrimSpace(b.q.Granularity)
	if g == "" {
		return ""
	}
	if _, err := connectors.NormalizeGrain(g); err == nil {
		return ""
	}
	return g
}

// timeGrain resolves the ISO grain from extras.time_grain_sqla, then from the
// legacy granularity field.
func (b *sqlBuilder) timeGrain() string {
	candidates := []string{}
	if b.q.Extras != nil {
		if v, ok := b.q.Extras["time_grain_sqla"]; ok {
			candidates = append(candidates, fmt.Sprint(v))
		}
	}
	candidates = append(candidates, b.q.Granularity)
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" || strings.EqualFold(c, "Time Column") {
			continue
		}
		if norm, err := connectors.NormalizeGrain(c); err == nil && norm != "" {
			return norm
		}
	}
	return ""
}

// columnExpr resolves a physical column to its SQL expression, honouring the
// dataset's calculated column expressions.
func (b *sqlBuilder) columnExpr(name string) string {
	name = strings.TrimSpace(name)
	for i := range b.ds.Columns {
		c := &b.ds.Columns[i]
		if c.ColumnName == name {
			if strings.TrimSpace(c.Expression) != "" {
				return c.Expression
			}
			return b.spec.QuoteIdentifier(c.ColumnName)
		}
	}
	for i := range b.ds.Columns {
		c := &b.ds.Columns[i]
		if strings.EqualFold(c.ColumnName, name) {
			if strings.TrimSpace(c.Expression) != "" {
				return c.Expression
			}
			return b.spec.QuoteIdentifier(c.ColumnName)
		}
	}
	if strings.HasPrefix(name, "(") || strings.ContainsAny(name, "( ") {
		// Free-form expression: pass through untouched.
		return name
	}
	return b.spec.QuoteIdentifier(name)
}

func (b *sqlBuilder) columnType(name string) string {
	for i := range b.ds.Columns {
		c := &b.ds.Columns[i]
		if strings.EqualFold(c.ColumnName, name) {
			return genericType(c.Type)
		}
	}
	return "STRING"
}

// columnItem resolves a groupby/columns entry (string, AdhocColumn or the JSON
// map form of one).
func (b *sqlBuilder) columnItem(item any) (expr, label, typ string, err error) {
	switch v := item.(type) {
	case string:
		label = v
		expr = b.columnExpr(v)
		typ = b.columnType(v)
		if strings.HasPrefix(v, "(") || strings.Contains(v, " ") {
			typ = "STRING"
		}
		return expr, label, typ, nil
	case AdhocColumn:
		// Go callers (the semantic_query tool) build typed AdhocColumn values
		// while the HTTP API hands the builder JSON maps. Supporting only the map
		// shape made every semantic query fail with "Unsupported column
		// definition" — the two paths must behave identically.
		label = v.Label
		if label == "" || !v.HasCustomLabel {
			if v.ColumnName != "" {
				label = v.ColumnName
			} else if label == "" {
				label = v.SQLExpression
			}
		}
		switch {
		case v.SQLExpression != "":
			return v.SQLExpression, label, "STRING", nil
		case v.ColumnName != "":
			return b.columnExpr(v.ColumnName), label, b.columnType(v.ColumnName), nil
		default:
			return "", "", "", apperrors.BadRequest("Unsupported column definition: %v", item)
		}
	case map[string]any:
		sqlExpr := firstString(v["sqlExpression"], v["sql_expression"])
		colName := firstString(v["column_name"], v["columnName"], v["column"])
		label = firstString(v["label"])
		hasCustom := asBool(v["hasCustomLabel"], v["has_custom_label"])
		if label == "" || !hasCustom {
			if colName != "" {
				label = colName
			} else if label == "" {
				label = sqlExpr
			}
		}
		switch {
		case sqlExpr != "":
			return sqlExpr, label, "STRING", nil
		case colName != "":
			return b.columnExpr(colName), label, b.columnType(colName), nil
		default:
			return "", "", "", apperrors.BadRequest("Unsupported column definition: %v", item)
		}
	}
	return "", "", "", apperrors.BadRequest("Unsupported column definition: %v", item)
}

// metricItem resolves a metric name or an adhoc metric object.
func (b *sqlBuilder) metricItem(item any) (expr, label, typ string, err error) {
	switch v := item.(type) {
	case string:
		for i := range b.ds.Metrics {
			m := &b.ds.Metrics[i]
			if m.MetricName == v {
				return m.Expression, v, metricType(m.Expression), nil
			}
		}
		for i := range b.ds.Metrics {
			m := &b.ds.Metrics[i]
			if strings.EqualFold(m.MetricName, v) {
				return m.Expression, v, metricType(m.Expression), nil
			}
		}
		return "", "", "", apperrors.BadRequest("Unknown metric %q", v)
	case AdhocMetric:
		return b.adhocMetric(v.SQLExpression, v.Column, v.Aggregate, v.Label, v.HasCustomLabel, v.OptionName, v.Expression)
	case map[string]any:
		return b.adhocMetric(
			firstString(v["sqlExpression"], v["sql_expression"]),
			asStringMap(v["column"]),
			firstString(v["aggregate"]),
			firstString(v["label"]),
			asBool(v["hasCustomLabel"], v["has_custom_label"]),
			firstString(v["optionName"], v["option_name"]),
			firstString(v["expression"]),
		)
	}
	return "", "", "", apperrors.BadRequest("Unsupported metric definition: %v", item)
}

func (b *sqlBuilder) adhocMetric(sqlExpr string, column map[string]any, aggregate, label string, hasCustom bool, optionName, expression string) (string, string, string, error) {
	colName := ""
	if column != nil {
		colName = firstString(column["column_name"], column["columnName"])
	}
	outLabel := label
	if !hasCustom || outLabel == "" {
		if optionName != "" {
			outLabel = optionName
		}
	}

	var expr string
	switch {
	case aggregate != "" && sqlExpr != "":
		expr = aggregateExpr(aggregate, sqlExpr)
	case aggregate != "" && colName != "":
		expr = aggregateExpr(aggregate, b.columnExpr(colName))
	case sqlExpr != "":
		expr = sqlExpr
	case expression != "":
		expr = expression
	case colName != "":
		expr = b.columnExpr(colName)
	default:
		return "", "", "", apperrors.BadRequest("Adhoc metric requires an aggregate, column or expression")
	}
	if outLabel == "" {
		outLabel = expr
	}
	return expr, outLabel, metricType(expr), nil
}

// aggregateExpr wraps an expression in the aggregate function requested by the
// adhoc metric definition.
func aggregateExpr(agg, expr string) string {
	norm := strings.ToUpper(strings.TrimSpace(agg))
	switch norm {
	case "COUNT_DISTINCT", "COUNTDISTINCT", "DISTINCT_COUNT":
		return "COUNT(DISTINCT " + expr + ")"
	case "APPROX_COUNT_DISTINCT":
		return "COUNT(DISTINCT " + expr + ")"
	}
	return norm + "(" + expr + ")"
}

func metricType(expr string) string {
	u := strings.ToUpper(expr)
	switch {
	case strings.Contains(u, "COUNT("):
		return "INT"
	case strings.Contains(u, "SUM("), strings.Contains(u, "AVG("), strings.Contains(u, "MIN("),
		strings.Contains(u, "MAX("), strings.Contains(u, "STDDEV"), strings.Contains(u, "VAR"):
		return "FLOAT"
	}
	return "FLOAT"
}

// fromClause renders the FROM target: "(virtual SQL) AS table" or schema.table.
func (b *sqlBuilder) fromClause() (string, error) {
	t := b.ds.Table
	if strings.TrimSpace(t.SQL) != "" {
		alias := strings.TrimSpace(t.TableNameStr)
		if alias == "" {
			alias = "virtual_table"
		}
		return "(" + strings.TrimSpace(t.SQL) + ") AS " + b.spec.QuoteIdentifier(alias), nil
	}
	if strings.TrimSpace(t.TableNameStr) == "" {
		return "", apperrors.BadRequest("Dataset has no table name")
	}
	schema := strings.TrimSpace(t.Schema)
	if schema != "" {
		return b.spec.QuoteIdentifier(schema) + "." + b.spec.QuoteIdentifier(t.TableNameStr), nil
	}
	return b.spec.QuoteIdentifier(t.TableNameStr), nil
}

// whereClauses builds the AND-joined predicates: RLS, temporal bounds, filters,
// extras.where.
func (b *sqlBuilder) whereClauses() ([]string, []map[string]any, error) {
	clauses := []string{}
	applied := []map[string]any{}

	if b.opts.RLSProvider != nil && b.ds.Table != nil {
		for _, rls := range b.opts.RLSProvider(b.ds.Table.ID) {
			rls = strings.TrimSpace(rls)
			if rls == "" {
				continue
			}
			clauses = append(clauses, "("+rls+")")
		}
	}

	// Explicit from/to bounds.
	if b.q.FromDttm != "" || b.q.ToDttm != "" {
		bounds, err := b.temporalBounds(b.q.FromDttm, b.q.ToDttm)
		if err != nil {
			return nil, nil, err
		}
		clauses = append(clauses, bounds...)
		applied = append(applied, map[string]any{
			"col": b.timeColumnName(), "op": "TEMPORAL_RANGE",
			"val": strings.TrimSpace(b.q.FromDttm + " : " + b.q.ToDttm),
		})
	}

	for _, f := range b.q.Filters {
		op := strings.ToUpper(strings.TrimSpace(f.Op))
		if op == "" {
			continue
		}
		if op == "TEMPORAL_RANGE" {
			from, to := SplitTimeRange(fmt.Sprint(f.Val))
			bounds, err := b.temporalBounds(from, to)
			if err != nil {
				return nil, nil, err
			}
			clauses = append(clauses, bounds...)
			applied = append(applied, map[string]any{"col": f.Col, "op": op, "val": f.Val})
			continue
		}
		clause, err := b.filterClause(f)
		if err != nil {
			return nil, nil, err
		}
		if clause == "" {
			continue
		}
		clauses = append(clauses, clause)
		applied = append(applied, map[string]any{"col": f.Col, "op": op, "val": f.Val})
	}

	if b.q.Extras != nil {
		for _, raw := range extrasClauses(b.q.Extras["where"]) {
			clauses = append(clauses, "("+raw+")")
		}
	}
	return clauses, applied, nil
}

func (b *sqlBuilder) timeColumnName() string {
	if c := b.ds.Table.MainDttmCol; c != "" {
		return c
	}
	return b.q.Granularity
}

// temporalBounds turns a from/to pair into predicates on the time expression.
func (b *sqlBuilder) temporalBounds(fromDttm, toDttm string) ([]string, error) {
	filterExpr := b.timeFilterExpr
	if filterExpr == "" {
		filterExpr = b.timeExpr
	}
	if filterExpr == "" {
		return nil, apperrors.BadRequest("Dataset has no temporal column; add filters on a time column instead")
	}
	if strings.TrimSpace(fromDttm) == "" && strings.TrimSpace(toDttm) == "" {
		return nil, nil
	}
	start, end, ok := ApplyTimeRange(fromDttm, toDttm)
	if !ok {
		return nil, apperrors.BadRequest("Could not parse time range %q : %q", fromDttm, toDttm)
	}
	var out []string
	if !start.IsZero() {
		out = append(out, filterExpr+" >= "+sqlTimeLiteral(start))
	}
	if !end.IsZero() {
		out = append(out, filterExpr+" < "+sqlTimeLiteral(end))
	}
	return out, nil
}

// filterClause renders a single simple filter.
func (b *sqlBuilder) filterClause(f Filter) (string, error) {
	col := b.columnExpr(f.Col)
	op := strings.ToUpper(strings.TrimSpace(f.Op))
	switch op {
	case "==", "=", "EQ":
		return col + " = " + b.literal(f.Val), nil
	case "!=", "<>", "NE":
		return col + " != " + b.literal(f.Val), nil
	case ">", ">=", "<", "<=":
		return col + " " + op + " " + b.literal(f.Val), nil
	case "IN":
		return col + " IN " + b.literalList(f.Val), nil
	case "NOT IN":
		return col + " NOT IN " + b.literalList(f.Val), nil
	case "LIKE":
		return col + " LIKE " + b.literal(f.Val), nil
	case "ILIKE":
		if b.supportsILike() {
			return col + " ILIKE " + b.literal(f.Val), nil
		}
		return col + " LIKE " + b.literal(f.Val), nil
	case "IS NULL":
		return col + " IS NULL", nil
	case "IS NOT NULL":
		return col + " IS NOT NULL", nil
	case "REGEX":
		return col + " " + b.regexOperator() + " " + b.literal(f.Val), nil
	default:
		return "", apperrors.BadRequest("Unsupported filter operator %q", f.Op)
	}
}

func (b *sqlBuilder) supportsILike() bool {
	switch b.spec.Engine() {
	case "postgresql", "clickhousedb", "duckdb":
		return true
	}
	return false
}

func (b *sqlBuilder) regexOperator() string {
	switch b.spec.Engine() {
	case "mysql":
		return "REGEXP"
	case "clickhousedb":
		return "match"
	case "sqlite":
		return "REGEXP"
	}
	return "~"
}

// havingClause renders extras.having.
func (b *sqlBuilder) havingClause() string {
	if b.q.Extras == nil {
		return ""
	}
	parts := extrasClauses(b.q.Extras["having"])
	return strings.Join(parts, " AND ")
}

// orderClause resolves the orderby triples ([expr, ascending]).
func (b *sqlBuilder) orderClause() string {
	parts := make([]string, 0, len(b.q.Orderby))
	for _, entry := range b.q.Orderby {
		col := firstString(entry[0])
		if col == "" {
			continue
		}
		expr := b.resolveOrderExpr(col)
		dir := "ASC"
		if !asBool(entry[1]) {
			dir = "DESC"
		}
		parts = append(parts, expr+" "+dir)
	}
	return strings.Join(parts, ", ")
}

func (b *sqlBuilder) resolveOrderExpr(col string) string {
	if strings.EqualFold(col, DTTMColumnAlias) && b.timeExpr != "" {
		return b.timeExpr
	}
	if expr, ok := b.labels[strings.ToLower(col)]; ok {
		return expr
	}
	for i := range b.ds.Metrics {
		m := &b.ds.Metrics[i]
		if strings.EqualFold(m.MetricName, col) {
			return m.Expression
		}
	}
	return b.columnExpr(col)
}

// limitClause renders LIMIT/OFFSET with the engine's syntax.
func (b *sqlBuilder) limitClause() string {
	limit := ApplyMaxRowLimit(b.q.RowLimit, b.opts.MaxRow)
	offset := b.q.RowOffset
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 && offset == 0 {
		return ""
	}
	var sb strings.Builder
	if limit > 0 {
		sb.WriteString(b.spec.LimitClause(limit))
	} else if offset > 0 && b.spec.Engine() == "mysql" {
		// MySQL requires LIMIT when OFFSET is used.
		sb.WriteString("LIMIT 18446744073709551615")
	}
	if offset > 0 {
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString("OFFSET ")
		sb.WriteString(strconv.Itoa(offset))
	}
	return sb.String()
}

// literal renders a filter value as SQL.
func (b *sqlBuilder) literal(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case string:
		return quoteLiteral(t)
	case bool:
		if t {
			return "TRUE"
		}
		return "FALSE"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case time.Time:
		return sqlTimeLiteral(t)
	default:
		return quoteLiteral(fmt.Sprint(t))
	}
}

func (b *sqlBuilder) literalList(v any) string {
	items := toAnyList(v)
	if len(items) == 0 {
		// "IN ()" is invalid SQL; an always-false predicate is equivalent.
		return "(NULL)"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, b.literal(item))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func toAnyList(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []string:
		out := make([]any, 0, len(t))
		for _, s := range t {
			out = append(out, s)
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		parts := strings.Split(t, ",")
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			out = append(out, strings.TrimSpace(p))
		}
		return out
	case nil:
		return nil
	default:
		return []any{v}
	}
}

// extrasClauses normalises extras.where / extras.having, which may be a string,
// a list of strings or a list of adhoc clauses.
func extrasClauses(v any) []string {
	var out []string
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if s := strings.TrimSpace(t); s != "" {
			out = append(out, s)
		}
	case []any:
		for _, item := range t {
			switch it := item.(type) {
			case string:
				if s := strings.TrimSpace(it); s != "" {
					out = append(out, s)
				}
			case map[string]any:
				sqlExpr := firstString(it["sqlExpression"], it["sql_expression"])
				if sqlExpr != "" {
					out = append(out, sqlExpr)
					continue
				}
				col := firstString(it["col"], it["column_name"])
				op := firstString(it["op"], it["operator"])
				val := it["val"]
				if col != "" && op != "" {
					out = append(out, col+" "+op+" "+fmt.Sprint(val))
				}
			}
		}
	case map[string]any:
		if sqlExpr := firstString(t["sqlExpression"], t["sql_expression"]); sqlExpr != "" {
			out = append(out, sqlExpr)
		}
	}
	return out
}

func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func sqlTimeLiteral(t time.Time) string {
	return quoteLiteral(t.UTC().Format("2006-01-02 15:04:05"))
}

func genericType(t string) string {
	u := strings.ToUpper(strings.TrimSpace(t))
	switch {
	case u == "":
		return "STRING"
	case strings.Contains(u, "INT"):
		return "INT"
	case strings.Contains(u, "DECIMAL"), strings.Contains(u, "NUMERIC"), strings.Contains(u, "DOUBLE"),
		strings.Contains(u, "FLOAT"), strings.Contains(u, "REAL"), strings.Contains(u, "MONEY"):
		return "FLOAT"
	case strings.Contains(u, "BOOL"):
		return "BOOLEAN"
	case strings.Contains(u, "DATE"), strings.Contains(u, "TIME"):
		return "DATETIME"
	}
	return "STRING"
}

// Time shift ---------------------------------------------------------------

var timeShiftPattern = regexp.MustCompile(`^(-?\d+)\s+(second|minute|hour|day|week|month|quarter|year)s?\s+(ago|later)$`)

// ApplyTimeShift shifts a temporal expression by a relative phrase such as
// "1 year ago". It is the Go equivalent of superset's time_shift handling.
func ApplyTimeShift(spec connectors.EngineSpec, expr, shift string) (string, error) {
	if strings.TrimSpace(shift) == "" {
		return expr, nil
	}
	m := timeShiftPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(shift)))
	if m == nil {
		return "", fmt.Errorf("unable to parse time shift %q", shift)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return "", fmt.Errorf("unable to parse time shift %q", shift)
	}
	if m[3] == "ago" {
		n = -n
	}
	unit := m[2]

	switch spec.Engine() {
	case "postgresql", "duckdb":
		interval := fmt.Sprintf("'%d %s'", n, unit)
		return expr + " + INTERVAL " + interval, nil
	case "mysql":
		return fmt.Sprintf("DATE_ADD(%s, INTERVAL %d %s)", expr, n, strings.ToUpper(unit)), nil
	case "sqlite":
		modifier := fmt.Sprintf("'%d %s'", n, unit)
		if unit == "second" || unit == "minute" || unit == "hour" {
			return fmt.Sprintf("DATETIME(%s, %s)", expr, modifier), nil
		}
		return fmt.Sprintf("DATE(%s, %s)", expr, modifier), nil
	case "clickhousedb":
		fn := map[string]string{
			"second": "addSeconds", "minute": "addMinutes", "hour": "addHours",
			"day": "addDays", "week": "addWeeks", "month": "addMonths",
			"quarter": "addQuarters", "year": "addYears",
		}[unit]
		return fmt.Sprintf("%s(%s, %d)", fn, expr, n), nil
	}
	// Generic ANSI fallback.
	return expr + " + INTERVAL '" + strconv.Itoa(n) + " " + unit + "'", nil
}
