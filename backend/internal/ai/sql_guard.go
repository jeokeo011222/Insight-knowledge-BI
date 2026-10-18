package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"superset/internal/models"
)

// SQL guard (结构化 SQL 守卫).
//
// The regex blocklist in sql_safety.go answers one question: "does this
// statement start with SELECT/WITH and contain no write keyword?". That is
// necessary but nowhere near sufficient — it happily accepts
//
//	SELECT * FROM other_table                     -- cross-dataset read
//	SELECT pg_read_file('/etc/passwd')            -- file access
//	SELECT * FROM dblink('...', 'DELETE ...')     -- remote write
//	SELECT * FROM t WHERE id = 1 FOR UPDATE       -- lock + side effect
//
// so the guard below adds four structural checks on top of it:
//
//  1. table whitelist   — only the dataset's own physical table (and its
//     declared schema) may appear in FROM/JOIN, plus CTE names;
//  2. source functions  — a table-valued function in FROM (read_csv,
//     generate_series, …) is rejected, because the FROM position is where a
//     read can come from anywhere;
//  3. function blocklist — data-reading / side-effecting functions are rejected
//     in *any* position, since `SELECT read_csv(...)` hides in the projection;
//  4. hidden columns    — a column with agent_visible=false is rejected by name,
//     so the model cannot select what it cannot see (mirrors Wren's "a column
//     that is not in the model does not exist").
//
// Every rejection carries a machine-readable code plus the list of things that
// *are* allowed, so the model repairs in one step instead of guessing.

// SQLGuardError is a structured rejection. Error() renders JSON because the
// text goes into a tool observation: the model reads fields, not prose.
type SQLGuardError struct {
	Code             string   `json:"code"`
	Phase            string   `json:"phase"`
	Message          string   `json:"message"`
	AvailableTables  []string `json:"available_tables,omitempty"`
	AvailableColumns []string `json:"available_columns,omitempty"`
	Hint             string   `json:"hint,omitempty"`
}

// Error renders the rejection as a single JSON object.
func (e *SQLGuardError) Error() string {
	payload := map[string]any{
		"status":  "rejected_by_sql_guard",
		"code":    e.Code,
		"phase":   e.Phase,
		"message": e.Message,
	}
	if len(e.AvailableTables) > 0 {
		payload["available_tables"] = e.AvailableTables
	}
	if len(e.AvailableColumns) > 0 {
		payload["available_columns"] = e.AvailableColumns
	}
	if e.Hint != "" {
		payload["hint"] = e.Hint
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("rejected_by_sql_guard: %s", e.Message)
	}
	return string(b)
}

// Guard codes.
const (
	GuardNotReadOnly         = "NOT_READ_ONLY"
	GuardUnbalanced          = "UNBALANCED_SQL"
	GuardTableNotAllowed     = "TABLE_NOT_ALLOWED"
	GuardSourceNotAllowed    = "SOURCE_FUNCTION_NOT_ALLOWED"
	GuardFunctionBlocked     = "FUNCTION_BLOCKED"
	GuardColumnHidden        = "COLUMN_HIDDEN"
	GuardSelectStar          = "SELECT_STAR_NOT_ALLOWED"
	GuardMetadataUnavailable = "METADATA_UNAVAILABLE"
)

// blockedFunctions are dangerous in any position. Names are canonicalised
// (lowercase, schema prefix stripped) so `pg_catalog.pg_read_file(...)` and
// `PG_READ_FILE(...)` match the same entry.
var blockedFunctions = map[string]bool{
	// Postgres: server-side file access, large objects, remote links, signals.
	"pg_read_file": true, "pg_read_binary_file": true, "pg_ls_dir": true,
	"pg_stat_file": true, "lo_import": true, "lo_export": true,
	"dblink": true, "dblink_connect": true, "dblink_exec": true,
	"pg_terminate_backend": true, "pg_cancel_backend": true,
	"pg_reload_conf": true, "pg_rotate_logfile": true, "pg_notify": true,
	"set_config": true, "setval": true, "nextval": true, "pg_sleep": true,
	"pg_sleep_for": true, "pg_sleep_until": true, "pg_read_server_files": true,
	// DuckDB / ClickHouse / Spark file readers.
	"read_csv": true, "read_csv_auto": true, "read_parquet": true,
	"read_json": true, "read_json_auto": true, "read_text": true,
	"parquet_scan": true, "iceberg_scan": true, "delta_scan": true,
	"glob": true, "file": true, "url": true, "s3": true, "remote": true,
	"clickhouse": true, "mysql": true, "postgresql": true, "sqlite": true,
	// MySQL / MariaDB.
	"load_file": true, "load_extension": true, "get_lock": true,
	"release_lock": true, "benchmark": true, "sleep": true,
	"sys_exec": true, "sys_eval": true,
	// SQL Server.
	"xp_cmdshell": true, "openrowset": true, "opendatasource": true,
	"openquery": true, "openxml": true,
}

// blockedSourceFunctions are table-valued functions in FROM. They are rejected
// even when harmless-looking, because the whitelist cannot reason about them and
// several of them read arbitrary files.
var blockedSourceFunctions = map[string]bool{
	"read_csv": true, "read_csv_auto": true, "read_parquet": true,
	"read_json": true, "read_json_auto": true, "read_text": true,
	"parquet_scan": true, "iceberg_scan": true, "delta_scan": true,
	"glob": true, "pg_read_file": true, "pg_ls_dir": true,
	"dblink": true, "generate_series": true, "range": true,
}

// guardToken is one lexical unit.
type guardToken struct {
	text  string // identifier text without quotes, or punctuation
	upper string // upper-cased text for keywords
	ident bool   // identifier (word or quoted identifier)
	pos   int
}

// tokenizeSQL splits a statement into identifiers, punctuation and numbers,
// dropping comments and string literals. Quoted identifiers are unwrapped. It
// reports false when quoting is unbalanced — such a statement cannot be
// analysed, so the caller must reject it rather than guess.
func tokenizeSQL(s string) ([]guardToken, bool) {
	var (
		out []guardToken
		i   int
	)
	isWordByte := func(c byte) bool {
		return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c >= 0x80
	}
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
			continue
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			i += end + 4
			continue
		case c == '\'':
			// String literal: skipped entirely (a literal is never a table).
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					break
				}
				i++
			}
			if i >= len(s) {
				return nil, false
			}
			i++
			continue
		case c == '"' || c == '`' || c == '[':
			closing := c
			if c == '[' {
				closing = ']'
			}
			start := i + 1
			i++
			for i < len(s) && s[i] != closing {
				i++
			}
			if i >= len(s) {
				return nil, false
			}
			text := s[start:i]
			i++
			out = append(out, guardToken{text: text, upper: strings.ToUpper(text), ident: true, pos: start})
			continue
		case c == '$' && i+1 < len(s) && (s[i+1] == '$' || isWordByte(s[i+1])):
			// Postgres dollar-quoted string: $tag$ ... $tag$
			j := i + 1
			for j < len(s) && s[j] != '$' {
				j++
			}
			if j >= len(s) {
				return nil, false
			}
			tag := s[i : j+1]
			end := strings.Index(s[j+1:], tag)
			if end < 0 {
				return nil, false
			}
			i = j + 1 + end + len(tag)
			continue
		case isWordByte(c):
			start := i
			for i < len(s) && isWordByte(s[i]) {
				i++
			}
			text := s[start:i]
			out = append(out, guardToken{text: text, upper: strings.ToUpper(text), ident: true, pos: start})
			continue
		default:
			out = append(out, guardToken{text: string(c), upper: string(c), pos: i})
			i++
			continue
		}
	}
	return out, true
}

// guardKeywords are clause boundaries: a comma after FROM only introduces
// another table before one of these appears.
var guardBoundary = map[string]bool{
	"WHERE": true, "GROUP": true, "HAVING": true, "ORDER": true, "LIMIT": true,
	"OFFSET": true, "FETCH": true, "WINDOW": true, "UNION": true, "EXCEPT": true,
	"INTERSECT": true, "ON": true, "USING": true, "JOIN": true, "INNER": true,
	"LEFT": true, "RIGHT": true, "FULL": true, "CROSS": true, "NATURAL": true,
	"LATERAL": true, "RETURNING": true, "SET": true, "VALUES": true,
}

// SQLGuardInput is everything the guard needs to judge one statement.
type SQLGuardInput struct {
	Statement string
	// AllowedTables are the physical tables the statement may read. An empty
	// slice means "no table whitelist" (virtual dataset, whose SQL is authored by
	// the dataset owner and validated separately).
	AllowedTables []string
	// HiddenColumns are the columns the Agent must not reference.
	HiddenColumns []string
	// AvailableColumns / AvailableTables populate the structured error.
	AvailableColumns []string
	ReviewTables     []string
}

// GuardSQL validates a read-only statement against the dataset's exposure rules.
func GuardSQL(in SQLGuardInput) error {
	tokens, ok := tokenizeSQL(in.Statement)
	if !ok {
		return &SQLGuardError{
			Code: GuardUnbalanced, Phase: "sql_tokenize",
			Message: "SQL 的引号/注释不闭合，无法分析",
			Hint:    "请重新生成一条完整、单条的 SELECT 语句",
		}
	}

	// Normalise the whitelist into lower-case, quote-free names.
	allowed := make(map[string]bool, len(in.AllowedTables)*2)
	for _, name := range in.AllowedTables {
		for _, variant := range tableNameVariants(name) {
			allowed[variant] = true
		}
	}
	// A table whitelist of exactly one table means "single-table dataset": we can
	// also accept the bare name without schema, which tableNameVariants covers.
	hidden := make(map[string]bool, len(in.HiddenColumns))
	for _, name := range in.HiddenColumns {
		hidden[strings.ToLower(name)] = true
	}

	ctes := collectCTENames(tokens)
	refs := collectTableRefs(tokens)

	// 1 + 2: FROM/JOIN sources.
	for _, ref := range refs {
		if ref.isFunction {
			name := canonicalFunctionName(ref.name)
			reason := "FROM 位置只允许表名或子查询"
			if blockedSourceFunctions[name] {
				reason = "该表函数可能读取文件、访问远程源或产生无界行集"
			}
			return &SQLGuardError{
				Code: GuardSourceNotAllowed, Phase: "sql_source",
				Message: fmt.Sprintf("FROM 位置的表函数 %s(...) 不被允许：%s", ref.name, reason),
				Hint:    "改为从数据集自身的物理表或子查询取数",
			}
		}
		if ref.name == "" || ctes[strings.ToLower(ref.name)] {
			continue
		}
		if len(allowed) == 0 {
			continue
		}
		if allowed[strings.ToLower(ref.name)] {
			continue
		}
		// Accept a schema-qualified reference when the bare table is whitelisted
		// and vice versa (the dataset may or may not declare a schema).
		if allowed[lastSegment(strings.ToLower(ref.name))] {
			continue
		}
		return &SQLGuardError{
			Code: GuardTableNotAllowed, Phase: "sql_table",
			Message:          fmt.Sprintf("表 %s 不在当前数据集的可读范围内", ref.name),
			AvailableTables:  in.AvailableTables(),
			AvailableColumns: in.AvailableColumns,
			Hint:             "只能查询当前数据集的表（或本次 SQL 中定义的 CTE）",
		}
	}

	// 3: dangerous functions anywhere (projection, WHERE, ORDER BY …).
	for i := 0; i+1 < len(tokens); i++ {
		// Locking clauses (`FOR UPDATE`, `FOR SHARE`) take row locks on a read,
		// which a read-only analysis must never do.
		if tokens[i].upper == "FOR" && (tokens[i+1].upper == "UPDATE" || tokens[i+1].upper == "SHARE") {
			return &SQLGuardError{
				Code: GuardNotReadOnly, Phase: "sql_lock",
				Message: "SQL 含有行锁子句（FOR UPDATE / FOR SHARE）",
				Hint:    "分析取数不能加锁，请去掉 FOR UPDATE/SHARE",
			}
		}
		if !tokens[i].ident || tokens[i+1].text != "(" {
			continue
		}
		name := canonicalFunctionName(tokens[i].text)
		if !blockedFunctions[name] {
			continue
		}
		return &SQLGuardError{
			Code: GuardFunctionBlocked, Phase: "sql_function",
			Message: fmt.Sprintf("函数 %s() 被禁止：它可能读取文件、访问网络或产生副作用", name),
			Hint:    "只使用常规的聚合/字符串/日期函数",
		}
	}

	// 4: wildcard projection.
	//
	// `SELECT *` defeats column-level hiding: the guard rejects a hidden column
	// by name, but a wildcard returns it anyway. The Agent already receives the
	// visible column list from schema_reader, so asking it to spell the columns
	// out costs nothing and closes the leak. COUNT(*) is unaffected — its star
	// lives one parenthesis deeper than the select list.
	if err := rejectWildcardProjection(tokens); err != nil {
		return err
	}

	// 5: hidden columns.
	for _, tok := range tokens {
		if !tok.ident {
			continue
		}
		name := strings.ToLower(tok.text)
		if !hidden[name] {
			continue
		}
		return &SQLGuardError{
			Code: GuardColumnHidden, Phase: "sql_column",
			Message:          fmt.Sprintf("列 %s 对本 Agent 不可见，不能出现在 SQL 中", tok.text),
			AvailableColumns: in.AvailableColumns,
			Hint:             "该列被标记为对 Agent 隐藏；如需使用请让管理员在数据集列设置中放开",
		}
	}
	return nil
}

// rejectWildcardProjection refuses `SELECT *` (and `SELECT t.*`) anywhere in the
// statement. It walks the token stream instead of regexing because `count(*)`,
// `a * b` and `SELECT x FROM (SELECT * ...)` all need to be told apart.
func rejectWildcardProjection(tokens []guardToken) error {
	depth := 0
	// inSelect[d] is true while we are between SELECT and FROM at depth d.
	inSelect := map[int]bool{}
	var prev guardToken
	for _, tok := range tokens {
		switch {
		case !tok.ident && tok.text == "(":
			depth++
			inSelect[depth] = false
		case !tok.ident && tok.text == ")":
			delete(inSelect, depth)
			if depth > 0 {
				depth--
			}
		case tok.ident && strings.EqualFold(tok.text, "select"):
			inSelect[depth] = true
		case tok.ident && strings.EqualFold(tok.text, "from"):
			// `EXTRACT(YEAR FROM x)` is inside parentheses, so its FROM belongs
			// to a deeper depth and cannot close the outer select list.
			inSelect[depth] = false
		case !tok.ident && tok.text == "*" && inSelect[depth] && startsProjectionItem(prev):
			return &SQLGuardError{
				Code: GuardSelectStar, Phase: "sql_projection",
				Message: "不允许 SELECT *：它会把对 Agent 隐藏的列一起查出来",
				Hint:    "按 schema_reader 返回的列名显式列出需要的列；计数请用 COUNT(*)",
			}
		}
		prev = tok
	}
	return nil
}

// startsProjectionItem reports whether prev leaves the `*` in item position
// (a wildcard) rather than mid-expression (a multiplication, e.g. `num * 2`).
func startsProjectionItem(prev guardToken) bool {
	if prev.text == "" {
		return true
	}
	if prev.text == "," || prev.text == "." {
		return true
	}
	if prev.ident && (strings.EqualFold(prev.text, "select") || strings.EqualFold(prev.text, "distinct")) {
		return true
	}
	return false
}

// AvailableTables returns the whitelist in a stable form for error payloads.
func (in SQLGuardInput) AvailableTables() []string {
	if len(in.ReviewTables) > 0 {
		return in.ReviewTables
	}
	return in.AllowedTables
}

// tableNameVariants expands a table name into the forms a statement may use.
func tableNameVariants(name string) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return nil
	}
	out := []string{n}
	parts := strings.Split(n, ".")
	if len(parts) >= 2 {
		// schema.table → also accept table, and catalog.schema.table → schema.table
		out = append(out, strings.Join(parts[len(parts)-2:], "."), parts[len(parts)-1])
	}
	return out
}

// lastSegment returns the final dotted component (the bare table name).
func lastSegment(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// canonicalFunctionName lower-cases a function name and strips any schema
// prefix so `pg_catalog.pg_read_file` matches `pg_read_file`.
func canonicalFunctionName(name string) string {
	return lastSegment(strings.ToLower(strings.TrimSpace(name)))
}

// tableRef is one source named in FROM/JOIN.
type tableRef struct {
	name       string // possibly schema-qualified
	isFunction bool
}

// subqueryIntro are the tokens after which "(" opens a subquery rather than a
// function call. Everything else that is an identifier before "(" is a call.
var subqueryIntro = map[string]bool{
	"IN": true, "EXISTS": true, "AS": true, "FROM": true, "JOIN": true,
	"VALUES": true, "UNION": true, "EXCEPT": true, "INTERSECT": true,
	"SELECT": true, "WHERE": true, "ON": true, "USING": true, "LATERAL": true,
	"ANY": true, "ALL": true, "SOME": true, "AND": true, "OR": true, "NOT": true,
	"THEN": true, "ELSE": true, "WHEN": true, "BY": true, "DISTINCT": true,
}

// collectTableRefs walks the token stream and returns every name that appears in
// a FROM or JOIN position, including inside subqueries. The outer loop always
// advances one token at a time, so a subquery's own FROM is visited too:
// `SELECT * FROM (SELECT * FROM secret) x` must be caught.
//
// FROM inside a function call's parentheses is not a table introduction:
// `EXTRACT(YEAR FROM created_at)`, `SUBSTRING(x FROM 1 FOR 2)` and
// `TRIM(BOTH ' ' FROM col)` all contain the keyword without reading a table. The
// depth stack distinguishes the two cases.
func collectTableRefs(tokens []guardToken) []tableRef {
	var refs []tableRef
	// funcDepth counts open parentheses that belong to a function call.
	var stack []bool
	inFunction := func() bool {
		for _, isFunc := range stack {
			if isFunc {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(tokens); i++ {
		switch tokens[i].text {
		case "(":
			isFunc := i > 0 && tokens[i-1].ident && !subqueryIntro[tokens[i-1].upper]
			stack = append(stack, isFunc)
			continue
		case ")":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if inFunction() {
			continue
		}
		switch tokens[i].upper {
		case "FROM":
			// Walk the comma list locally; i keeps advancing so nested tokens are
			// still scanned by the outer loop.
			j := i + 1
			for {
				j = collectOneSource(tokens, j, &refs)
				if j < len(tokens) && tokens[j].text == "," {
					j++
					continue
				}
				break
			}
		case "JOIN":
			collectOneSource(tokens, i+1, &refs)
		}
	}
	return refs
}

// collectOneSource reads one table reference (optionally aliased) and returns
// the index of the next unconsumed token.
func collectOneSource(tokens []guardToken, i int, refs *[]tableRef) int {
	// Skip noise keywords that may precede a source.
	for i < len(tokens) {
		switch tokens[i].upper {
		case "LATERAL", "ONLY":
			i++
			continue
		}
		break
	}
	if i >= len(tokens) {
		return i
	}
	if tokens[i].text == "(" {
		// Subquery or joined expression: skip the balanced group. Its own FROM is
		// visited by the outer scan.
		depth := 0
		for i < len(tokens) {
			if tokens[i].text == "(" {
				depth++
			} else if tokens[i].text == ")" {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
			i++
		}
		return skipAlias(tokens, i)
	}
	if !tokens[i].ident || guardBoundary[tokens[i].upper] {
		return i
	}

	// Dotted name: a.b.c
	var parts []string
	parts = append(parts, tokens[i].text)
	i++
	for i+1 < len(tokens) && tokens[i].text == "." && tokens[i+1].ident {
		parts = append(parts, tokens[i+1].text)
		i += 2
	}
	name := strings.Join(parts, ".")

	// `name(...)` is a table function, not a table.
	if i < len(tokens) && tokens[i].text == "(" {
		*refs = append(*refs, tableRef{name: name, isFunction: true})
		depth := 0
		for i < len(tokens) {
			if tokens[i].text == "(" {
				depth++
			} else if tokens[i].text == ")" {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
			i++
		}
		return skipAlias(tokens, i)
	}

	*refs = append(*refs, tableRef{name: name})
	return skipAlias(tokens, i)
}

// skipAlias consumes `AS x` or a bare alias that follows a table reference.
func skipAlias(tokens []guardToken, i int) int {
	if i < len(tokens) && tokens[i].upper == "AS" {
		i++
		if i < len(tokens) && tokens[i].ident {
			i++
		}
		return i
	}
	if i < len(tokens) && tokens[i].ident && !guardBoundary[tokens[i].upper] {
		i++
	}
	return i
}

// collectCTENames returns the names defined by WITH clauses so the whitelist does
// not reject a legitimate CTE.
func collectCTENames(tokens []guardToken) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < len(tokens); i++ {
		if tokens[i].upper != "WITH" {
			continue
		}
		// WITH [RECURSIVE] name [ (cols) ] AS ( ... ) [, name AS ( ... )]*
		j := i + 1
		if j < len(tokens) && tokens[j].upper == "RECURSIVE" {
			j++
		}
		for j < len(tokens) && tokens[j].ident {
			name := tokens[j].text
			j++
			// Optional column list.
			if j < len(tokens) && tokens[j].text == "(" {
				depth := 0
				for j < len(tokens) {
					if tokens[j].text == "(" {
						depth++
					} else if tokens[j].text == ")" {
						depth--
						if depth == 0 {
							j++
							break
						}
					}
					j++
				}
			}
			if j >= len(tokens) || tokens[j].upper != "AS" {
				break
			}
			j++
			if j >= len(tokens) || tokens[j].text != "(" {
				break
			}
			out[strings.ToLower(name)] = true
			depth := 0
			for j < len(tokens) {
				if tokens[j].text == "(" {
					depth++
				} else if tokens[j].text == ")" {
					depth--
					if depth == 0 {
						j++
						break
					}
				}
				j++
			}
			if j < len(tokens) && tokens[j].text == "," {
				j++
				continue
			}
			break
		}
	}
	return out
}

// guardDatasetSQL is the Service-level entry point: it loads the dataset and its
// columns, then guards the statement. A dataset that cannot be read is reported
// explicitly rather than silently skipping the whitelist.
func (s *Service) guardDatasetSQL(ctx context.Context, datasetID int, statement string) error {
	if s.DB == nil {
		// Without metadata we can still do the lexical checks.
		return GuardSQL(SQLGuardInput{Statement: statement})
	}
	var table models.SqlaTable
	if err := s.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).
		First(&table).Error; err != nil {
		return &SQLGuardError{
			Code: GuardMetadataUnavailable, Phase: "sql_guard",
			Message: fmt.Sprintf("数据集 %d 不存在或不可读，无法校验 SQL 的可读范围", datasetID),
			Hint:    "请确认 dataset_id 后重试",
		}
	}
	var columns []models.TableColumn
	if err := s.DB.WithContext(ctx).Where("table_id = ?", table.ID).
		Order("column_name").Find(&columns).Error; err != nil {
		return &SQLGuardError{
			Code: GuardMetadataUnavailable, Phase: "sql_guard",
			Message: "列元数据读取失败，无法校验 SQL",
			Hint:    "稍后重试；如持续失败请检查元数据库",
		}
	}

	in := SQLGuardInput{Statement: statement}
	available := make([]string, 0, len(columns))
	for _, column := range columns {
		if !column.IsActive || !column.AgentVisible {
			continue
		}
		available = append(available, column.ColumnName)
	}
	in.AvailableColumns = available
	for _, column := range columns {
		if column.AgentVisible {
			continue
		}
		in.HiddenColumns = append(in.HiddenColumns, column.ColumnName)
	}

	// Virtual datasets read through their own SQL, so the physical table name is
	// unknown to us; the session downgrade and function blocklist still apply.
	if strings.TrimSpace(table.SQL) == "" && !table.IsSQLLabView {
		in.AllowedTables = append(in.AllowedTables, table.TableNameStr)
		if table.Schema != "" {
			in.AllowedTables = append(in.AllowedTables, table.Schema+"."+table.TableNameStr)
		}
		in.ReviewTables = []string{table.TableNameStr}
	}
	return GuardSQL(in)
}
