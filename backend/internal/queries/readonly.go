package queries

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	apperrors "superset/internal/pkg/errors"
)

// ReadOnlyOptions tunes a direct read-only statement execution.
type ReadOnlyOptions struct {
	// DatasetID resolves the target database, its authorization and its RLS rules.
	DatasetID int
	// RowLimit overrides the executor default row limit; it is capped by MaxRow.
	RowLimit int
	// ExemptMasking skips column masking. Only the compliance-officer case
	// (admin) sets it: everybody else sees the same masks the chart path shows.
	ExemptMasking bool
}

// ReadOnlyResult is the payload returned by ExecuteReadOnlySQL.
type ReadOnlyResult struct {
	SQL        string           `json:"sql"`
	Data       []map[string]any `json:"data"`
	ColNames   []string         `json:"colnames"`
	ColTypes   []string         `json:"coltypes"`
	RowCount   int              `json:"rowcount"`
	Truncated  bool             `json:"truncated"`
	QueryID    int              `json:"query_id"`
	DurationMS int              `json:"duration_ms"`
	// MaskedColumns lists the result columns whose values were rewritten, so the
	// Agent can tell the user "值已脱敏" instead of silently showing asterisks.
	MaskedColumns []string `json:"masked_columns,omitempty"`
	// SessionReadOnly reports whether the statement ran on a session the engine
	// itself downgraded to read-only. False means the guard was the only
	// boundary (an engine without a session switch).
	SessionReadOnly bool `json:"session_read_only"`
}

// ExecuteReadOnlySQL runs a single caller-supplied read-only statement against
// the dataset's database using the same authorization, row-level-security and
// auditing as the chart-data path. It is the Agent's query_executor entrypoint.
//
// The statement MUST already be validated as a single SELECT by the caller
// (see internal/ai/sql_safety.go); this method additionally wraps it so the row
// limit and any RLS clauses are always enforced.
func (e *Executor) ExecuteReadOnlySQL(ctx context.Context, userID int, statement string, opts ReadOnlyOptions) (*ReadOnlyResult, error) {
	stmt := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(statement), ";"))
	if stmt == "" {
		return nil, apperrors.BadRequest("SQL is required")
	}
	if opts.DatasetID <= 0 {
		return nil, apperrors.BadRequest("dataset_id is required")
	}

	qc := &QueryContext{Datasource: map[string]any{"id": opts.DatasetID, "type": "table"}}
	ds, database, err := e.loadDataset(ctx, qc)
	if err != nil {
		return nil, err
	}
	if err := e.authorize(ctx, userID, ds, database); err != nil {
		return nil, err
	}

	var clauses []string
	if rls := e.rlsProvider(ctx, userID); rls != nil {
		clauses = rls(ds.Table.ID)
	}

	limit := opts.RowLimit
	if limit <= 0 {
		limit = e.rowLimit()
	}
	if max := e.maxRow(); max > 0 && limit > max {
		limit = max
	}
	final := wrapReadOnly(stmt, clauses, limit)

	start := time.Now()
	pool, err := openPool(database.SQLAlchemyURI)
	if err != nil {
		e.logQueryError(database.ID, "connect", err)
		return nil, apperrors.ServiceUnavailable("The data source is unavailable")
	}
	defer pool.Close()

	// Session-level read-only downgrade: the engine refuses writes even when the
	// statement slipped past the lexical guard. Degrades to the pool for engines
	// without a session switch (the caller sees SessionReadOnly=false).
	runner, sessionReadOnly, release := openReadOnlySession(ctx, pool, database.SQLAlchemyURI, e.Log)
	defer release()

	// SQL quality gate: refuse plans that would scan an unbounded number of
	// rows (the classic "forgot the join condition" query). It fails open —
	// a guard that cannot explain must not block the query.
	if e.ExplainMaxRows > 0 && isPostgresURI(database.SQLAlchemyURI) {
		if gerr := e.explainGuard(ctx, runner, final, e.ExplainMaxRows); gerr != nil {
			return nil, gerr
		}
	}

	raw, err := executeSQL(ctx, runner, final, e.queryTimeout())
	if err != nil {
		if _, rerr := e.recordQuery(ctx, userID, database.ID, final, 0, start, time.Now(), "failed", err.Error()); rerr != nil && e.Log != nil {
			e.Log.Warn("query audit write failed", zap.Error(rerr))
		}
		e.logQueryError(database.ID, "execute", err)
		return nil, classifyQueryError(err, e.queryTimeout())
	}

	rows := raw.Data
	truncated := false
	if max := e.maxRow(); max > 0 && len(rows) > max {
		rows = rows[:max]
		truncated = true
	}
	// Masking runs before the rows leave this method, which is what makes the
	// Agent path and the chart path agree: the masked value is what reaches the
	// model, the answer and ai_tool_call_log.
	masked, mErr := e.maskResultRows(ctx, opts, rows)
	if mErr != nil {
		return nil, mErr
	}
	end := time.Now()
	id, rerr := e.recordQuery(ctx, userID, database.ID, final, len(rows), start, end, "success", "")
	if rerr != nil && e.Log != nil {
		e.Log.Warn("query audit write failed", zap.Error(rerr))
	}

	return &ReadOnlyResult{
		SQL:             final,
		Data:            rows,
		ColNames:        raw.ColNames,
		ColTypes:        raw.ColTypes,
		RowCount:        len(rows),
		Truncated:       truncated,
		QueryID:         id,
		DurationMS:      int(end.Sub(start).Milliseconds()),
		MaskedColumns:   masked,
		SessionReadOnly: sessionReadOnly,
	}, nil
}

// maskResultRows applies the dataset's column masks unless the caller is exempt.
// A metadata failure fails closed: returning raw PII because a lookup failed
// would defeat the whole mechanism.
func (e *Executor) maskResultRows(ctx context.Context, opts ReadOnlyOptions, rows []map[string]any) ([]string, error) {
	if opts.ExemptMasking || len(rows) == 0 {
		return nil, nil
	}
	rules, err := LoadMaskRules(ctx, e.DB, opts.DatasetID)
	if err != nil {
		return nil, apperrors.Internal("Failed to load column masking rules: %s", err.Error())
	}
	if len(rules) == 0 {
		return nil, nil
	}
	// Only report the columns that actually appeared in the result.
	present := make([]string, 0, len(rules))
	for column := range rules {
		if len(rows) > 0 {
			if _, ok := rows[0][column]; ok {
				present = append(present, column)
			}
		}
	}
	ApplyMaskRules(rows, rules)
	sortStrings(present)
	return present, nil
}

// sortStrings sorts in place (a tiny helper keeps the import list stable).
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// wrapReadOnly encloses the statement so that RLS clauses apply to its output
// and the row limit is always enforced regardless of the inner statement.
// isPostgresURI reports whether the datasource speaks Postgres.
func isPostgresURI(uri string) bool {
	u := strings.ToLower(uri)
	return strings.HasPrefix(u, "postgres://") || strings.HasPrefix(u, "postgresql://") ||
		strings.HasPrefix(u, "postgresql+psycopg2://")
}

// planNode is the subset of a Postgres EXPLAIN (FORMAT JSON) plan we inspect.
type planNode struct {
	NodeType string     `json:"Node Type"`
	PlanRows float64    `json:"Plan Rows"`
	Plans    []planNode `json:"Plans"`
}

// explainGuard rejects a statement whose plan estimates a sequential scan over
// more rows than maxRows. Errors are swallowed: the guard must never be the
// reason a legitimate query fails.
func (e *Executor) explainGuard(ctx context.Context, q sqlQuerier, stmt string, maxRows int) error {
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := q.QueryContext(qctx, "EXPLAIN (FORMAT JSON) "+stmt)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var raw []byte
	for rows.Next() {
		var chunk []byte
		if err := rows.Scan(&chunk); err != nil {
			return nil
		}
		raw = append(raw, chunk...)
	}
	if err := rows.Err(); err != nil {
		return nil
	}

	var plans []struct {
		Plan planNode `json:"Plan"`
	}
	if err := json.Unmarshal(raw, &plans); err != nil || len(plans) == 0 {
		return nil
	}
	if rows := worstSeqScan(plans[0].Plan); rows > float64(maxRows) {
		return apperrors.BadRequest(
			"Query is estimated to scan about %d rows; add a filter or fix the join condition and retry",
			int64(rows))
	}
	return nil
}

// worstSeqScan returns the largest estimated row count among sequential scans
// in a plan tree.
func worstSeqScan(n planNode) float64 {
	worst := 0.0
	if strings.EqualFold(n.NodeType, "Seq Scan") && n.PlanRows > worst {
		worst = n.PlanRows
	}
	for _, child := range n.Plans {
		if v := worstSeqScan(child); v > worst {
			worst = v
		}
	}
	return worst
}

// wrapReadOnly enforces the row limit and applies row-level-security
// predicates.
//
// The predicates must be injected into the query's own WHERE clause, not
// wrapped around its output: an aggregate (`SELECT count(*) AS c`) projects no
// user column, so an outer `WHERE tenant_id = ...` either errors or filters
// nothing. When the statement cannot be rewritten safely (set operations), it
// falls back to the legacy outer filter so behaviour never gets weaker.
func wrapReadOnly(stmt string, clauses []string, limit int) string {
	if len(clauses) > 0 {
		if injected, ok := injectPredicate(stmt, clauses); ok {
			return fmt.Sprintf("SELECT * FROM (\n%s\n) AS __ai_sub\nLIMIT %d", injected, limit)
		}
		var b strings.Builder
		b.WriteString("SELECT * FROM (\n")
		b.WriteString(stmt)
		b.WriteString("\n) AS __ai_sub\nWHERE (")
		b.WriteString(strings.Join(clauses, ") AND ("))
		b.WriteString(")")
		fmt.Fprintf(&b, "\nLIMIT %d", limit)
		return b.String()
	}
	var b strings.Builder
	b.WriteString("SELECT * FROM (\n")
	b.WriteString(stmt)
	b.WriteString("\n) AS __ai_sub")
	fmt.Fprintf(&b, "\nLIMIT %d", limit)
	return b.String()
}

// injectPredicate inserts `AND (<clause>)` into the statement's top-level
// WHERE, or adds a WHERE before GROUP/HAVING/ORDER/LIMIT when there is none.
// It reports false for statements it cannot rewrite safely (top-level set
// operations, unbalanced quotes) so the caller can fall back.
func injectPredicate(stmt string, clauses []string) (string, bool) {
	body := strings.TrimSpace(stmt)
	body = strings.TrimSuffix(body, ";")
	if body == "" {
		return "", false
	}
	cond := "(" + strings.Join(clauses, ") AND (") + ")"

	positions, ok := topLevelKeywords(body)
	if !ok {
		return "", false
	}
	for _, kw := range positions {
		if kw.name == "UNION" || kw.name == "EXCEPT" || kw.name == "INTERSECT" {
			return "", false
		}
	}

	// Boundary keywords that end a WHERE condition.
	boundary := func(from int) int {
		for _, kw := range positions {
			if kw.start > from && (kw.name == "GROUP" || kw.name == "HAVING" ||
				kw.name == "ORDER" || kw.name == "LIMIT" || kw.name == "OFFSET" ||
				kw.name == "FETCH" || kw.name == "WINDOW") {
				return kw.start
			}
		}
		return len(body)
	}

	// splitAt cuts the statement, trimming the whitespace around the seam so
	// the rewritten SQL reads cleanly ("... 'paid' AND (...) \nGROUP BY").
	splitAt := func(at int, connector string) string {
		head := strings.TrimRight(body[:at], " \t\r\n")
		tail := strings.TrimLeft(body[at:], " \t")
		return head + " " + connector + " " + cond + "\n" + tail
	}

	for _, kw := range positions {
		if kw.name != "WHERE" {
			continue
		}
		end := boundary(kw.start)
		return splitAt(end, "AND"), true
	}

	// No WHERE: insert before the first clause that must follow it, else append.
	insertAt := len(body)
	for _, kw := range positions {
		if kw.name == "GROUP" || kw.name == "HAVING" || kw.name == "ORDER" ||
			kw.name == "LIMIT" || kw.name == "OFFSET" || kw.name == "FETCH" ||
			kw.name == "WINDOW" {
			insertAt = kw.start
			break
		}
	}
	return splitAt(insertAt, "WHERE"), true
}

// topKeyword is one clause keyword found at nesting depth zero.
type topKeyword struct {
	name  string
	start int
}

// topLevelKeywords scans a statement and returns the clause keywords that sit
// outside parentheses, string literals and comments. It returns ok=false when
// quoting is unbalanced, because rewriting such a statement is unsafe.
func topLevelKeywords(s string) ([]topKeyword, bool) {
	var (
		out   []topKeyword
		depth int
		i     int
	)
	isWord := func(c byte) bool {
		return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\'' || c == '"':
			quote := c
			i++
			for i < len(s) {
				if s[i] == '\\' && quote == '\'' {
					i += 2
					continue
				}
				if s[i] == quote {
					// Doubled quote is an escaped quote inside the literal.
					if i+1 < len(s) && s[i+1] == quote {
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
		case c == '(':
			depth++
			i++
			continue
		case c == ')':
			if depth > 0 {
				depth--
			}
			i++
			continue
		}
		if depth == 0 && isWord(c) && (i == 0 || !isWord(s[i-1])) {
			j := i
			for j < len(s) && isWord(s[j]) {
				j++
			}
			word := strings.ToUpper(s[i:j])
			switch word {
			case "WHERE", "GROUP", "HAVING", "ORDER", "LIMIT", "OFFSET", "FETCH",
				"UNION", "EXCEPT", "INTERSECT", "WINDOW":
				out = append(out, topKeyword{name: word, start: i})
			}
			i = j
			continue
		}
		i++
	}
	return out, true
}
