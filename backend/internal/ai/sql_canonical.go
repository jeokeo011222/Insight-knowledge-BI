package ai

import (
	"strings"
)

// Canonical SQL (固定 SQL 模板).
//
// Two runs of the same question must produce the same SQL, otherwise the user
// gets two different numbers and no way to tell which one is right. Pinning the
// temperature removes most of the sampling variance, but it cannot remove the
// stylistic variance that remains even at temperature 0:
//
//	SELECT COUNT(*) AS record_count FROM public.birth_names
//	select count(1) as total from "birth_names"
//	SELECT COUNT(*) FROM "public"."birth_names"
//
// All three are the same query. This file folds them into one canonical text
// before the statement is guarded and executed, so "same question, same SQL"
// becomes a property of the product rather than a hope about the model.
//
// The transformation is deliberately conservative — every rewrite below is
// semantics-preserving, and anything the rewriter cannot prove is left alone:
//
//	quoting        "gender" and gender print as gender (mixed-case keeps quotes)
//	keywords       upper-cased; functions upper-cased; identifiers lower-cased
//	schema         public.birth_names and birth_names converge on one form
//	count(1)       becomes count(*)
//	ASC            dropped (it is the default)
//	!=             becomes <>
//	aliases        computed projections get a name derived from their own
//	               expression (count, sum_num, count_distinct_name …), and any
//	               reference to the old alias in GROUP BY / ORDER BY is rewritten
//
// Alias regeneration is the one rewrite with visible effect: the result columns
// of an Agent query get stable, expression-derived names instead of the model's
// mood of the moment. That is a feature — a saved chart keeps working — and it
// is what makes two runs comparable at all.
//
// It is skipped (only formatting is applied) when it cannot be proven safe:
// the statement has CTEs, several top-level SELECTs, or an alias that collides
// with a real column name.

// canonReserved are words that cannot be emitted as bare identifiers. A word in
// this set is always upper-cased as a keyword, so a column with the same name
// is quoted instead of being mistaken for syntax.
var canonReserved = map[string]bool{
	"all": true, "and": true, "as": true, "asc": true, "between": true,
	"by": true, "case": true, "cast": true, "cross": true, "desc": true,
	"distinct": true, "else": true, "end": true, "except": true, "exists": true,
	"false": true, "from": true, "full": true,
	"group": true, "having": true, "in": true, "inner": true, "intersect": true,
	"is": true, "join": true, "left": true, "like": true, "limit": true,
	"ilike": true, "not": true, "null": true,
	"offset": true, "on": true, "or": true, "order": true,
	"outer": true, "over": true, "partition": true,
	"right": true, "select": true, "then": true,
	"true": true, "union": true, "using": true,
	"when": true, "where": true, "with": true,
}

// canonKeywords are the words we can safely upper-case. Each one is reserved in
// PostgreSQL, so it can never be a bare column name — upper-casing it cannot
// change which identifier a statement refers to. Window-frame words (ROWS /
// RANGE / UNBOUNDED …) are deliberately absent: they are legal column names, so
// they are left as they came, which keeps such a statement valid.
var canonKeywords = map[string]bool{
	"select": true, "from": true, "where": true, "group": true, "by": true,
	"having": true, "order": true, "limit": true, "offset": true, "as": true,
	"and": true, "or": true, "not": true, "in": true, "is": true, "null": true,
	"like": true, "ilike": true, "between": true, "case": true, "when": true,
	"then": true, "else": true, "end": true, "distinct": true, "join": true,
	"left": true, "right": true, "inner": true, "outer": true, "full": true,
	"cross": true, "on": true, "using": true, "union": true, "all": true,
	"asc": true, "desc": true, "exists": true, "with": true, "over": true,
	"partition": true, "true": true, "false": true, "cast": true,
	"interval": true, "except": true, "intersect": true,
	"current_date": true, "current_timestamp": true, "current_time": true,
}

// CanonicalOptions carries the metadata that lets the rewriter talk about the
// dataset's own table instead of guessing.
type CanonicalOptions struct {
	// Schema and Table are the dataset's physical location. Empty Schema means
	// "unknown", in which case a schema prefix is dropped only when it is
	// `public` (the default search_path of every connection this product opens).
	Schema string
	Table  string
	// Columns are the visible column names; used to prove that renaming an
	// alias cannot shadow a real column.
	Columns []string
}

// SQL token kinds.
const (
	canonWord = iota
	canonIdent
	canonNumber
	canonString
	canonPunct
	canonOp
)

type canonTok struct {
	kind  int
	text  string // word/identifier text (unquoted), literal raw text otherwise
	upper string
	depth int // paren depth *before* this token
}

// canonicalizeSQL folds a statement into its canonical text. It never returns
// an empty string for a non-empty statement: when the input cannot be
// tokenised the trimmed original is returned unchanged, so a formatting nicety
// can never block a query.
func canonicalizeSQL(statement string, opt CanonicalOptions) string {
	toks, ok := canonLex(statement)
	if !ok || len(toks) == 0 {
		return strings.TrimSpace(statement)
	}
	if isFlatQuery(toks) {
		toks = canonCountStar(toks)
		toks = canonAliases(toks, opt)
	}
	toks = canonTables(toks, opt)
	toks = canonCountStar(toks)
	toks = canonDropASC(toks)
	toks = canonOperators(toks)
	// A trailing semicolon is never part of the statement we execute.
	for len(toks) > 0 && toks[len(toks)-1].kind == canonPunct && toks[len(toks)-1].text == ";" {
		toks = toks[:len(toks)-1]
	}
	return canonPrint(toks)
}

/* ------------------------------------------------------------------ lexer */

// canonLex splits SQL into tokens while preserving what matters for
// re-emission: string literals keep their exact bytes, quoted identifiers keep
// their case, and paren depth is recorded per token.
func canonLex(s string) ([]canonTok, bool) {
	var out []canonTok
	depth := 0
	isWordByte := func(c byte) bool {
		return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c >= 0x80
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			i += end + 4
		case c == '$' && i+1 < len(s) && (s[i+1] == '$' || isWordByte(s[i+1])):
			// Postgres dollar-quoted string: $tag$ … $tag$. Its body may contain
			// quotes, so it has to be consumed as one literal before those bytes
			// are mistaken for the start of a string. The guard's tokenizer
			// already understands this form; the two must agree, otherwise the
			// canonicalizer silently gives up on statements the guard accepts.
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
			stop := j + 1 + end + len(tag)
			out = append(out, canonTok{kind: canonString, text: s[i:stop], upper: s[i:stop], depth: depth})
			i = stop
		case c == '\'':
			start := i
			i++
			closed := false
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
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false
			}
			out = append(out, canonTok{kind: canonString, text: s[start:i], upper: s[start:i], depth: depth})
		case c == '"' || c == '`' || (c == '[' && !isSubscriptAt(s, i)):
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
			out = append(out, canonTok{kind: canonIdent, text: text, upper: strings.ToUpper(text), depth: depth})
		case c == '(':
			out = append(out, canonTok{kind: canonPunct, text: "(", upper: "(", depth: depth})
			depth++
			i++
		case c == ')':
			depth--
			if depth < 0 {
				return nil, false
			}
			out = append(out, canonTok{kind: canonPunct, text: ")", upper: ")", depth: depth})
			i++
		case c >= '0' && c <= '9':
			start := i
			for i < len(s) && (isWordByte(s[i]) || s[i] == '.') {
				i++
			}
			out = append(out, canonTok{kind: canonNumber, text: s[start:i], upper: s[start:i], depth: depth})
		case isWordByte(c):
			start := i
			for i < len(s) && isWordByte(s[i]) {
				i++
			}
			text := s[start:i]
			out = append(out, canonTok{kind: canonWord, text: text, upper: strings.ToUpper(text), depth: depth})
		case strings.ContainsRune("+-*/%<>=!|&^~", rune(c)):
			start := i
			for i < len(s) && strings.ContainsRune("+-*/%<>=!|&^~", rune(s[i])) {
				i++
			}
			op := s[start:i]
			// `::` is a cast and `||` concatenation: keep them glued to their
			// operands (no spaces) instead of treating them as binary operators.
			kind := canonOp
			if op == "::" || op == "||" || op == "->" || op == "->>" || op == "#>" || op == "#>>" {
				kind = canonPunct
			}
			out = append(out, canonTok{kind: kind, text: op, upper: op, depth: depth})
		default:
			out = append(out, canonTok{kind: canonPunct, text: string(c), upper: string(c), depth: depth})
			i++
		}
	}
	return out, depth == 0
}

/* -------------------------------------------------------------- transforms */

// isSubscriptAt reports whether s[i] opens an array subscript rather than
// quoting an identifier.
//
// `[` means two different things in SQL: SQL Server quotes identifiers with it
// (`[my column]`), while PostgreSQL subscripts arrays with it (`tags[1]`). The
// two are told apart by position — a subscript always follows an identifier, a
// number or a closing bracket with no space in between, whereas a quoted
// identifier opens a name, so it follows the start of the statement, a space or
// punctuation. Reading `tags[1]` as a quoted identifier re-emitted it as
// `tags "1"`, which parses as an alias and quietly returns the wrong rows, and
// the guard had no reason to reject it.
func isSubscriptAt(s string, i int) bool {
	if i == 0 {
		return false
	}
	switch s[i-1] {
	case '_', '$', ')', ']', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return true
	case ' ':
		return false
	}
	c := s[i-1]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// isFlatQuery reports whether the statement is a single SELECT at depth 0 with
// no CTE and no set operation: the only shape where renaming projection aliases
// cannot break a correlated reference from an outer query.
func isFlatQuery(toks []canonTok) bool {
	selects := 0
	for _, t := range toks {
		if t.kind != canonWord {
			continue
		}
		switch t.upper {
		case "SELECT":
			if t.depth == 0 {
				selects++
			}
		case "WITH":
			if t.depth == 0 {
				return false
			}
		case "UNION", "EXCEPT", "INTERSECT":
			if t.depth == 0 {
				return false
			}
		}
	}
	return selects == 1
}

// canonAliases rewrites the top-level projection so every computed expression
// carries a name derived from its own canonical text, then rewrites references
// to the old names in the rest of the statement.
func canonAliases(toks []canonTok, opt CanonicalOptions) []canonTok {
	start := -1
	for i, t := range toks {
		if t.kind == canonWord && t.upper == "SELECT" && t.depth == 0 {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return toks
	}
	end := len(toks)
	for i := start; i < len(toks); i++ {
		t := toks[i]
		if t.kind == canonWord && t.depth == 0 {
			switch t.upper {
			case "FROM", "WHERE", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET", "WINDOW", "INTO":
				end = i
			}
			if end != len(toks) {
				break
			}
		}
	}
	if end <= start {
		return toks
	}

	// Split the projection into items at depth-0 commas.
	var spans [][2]int
	itemStart := start
	for i := start; i < end; i++ {
		if toks[i].kind == canonPunct && toks[i].text == "," && toks[i].depth == 0 {
			spans = append(spans, [2]int{itemStart, i})
			itemStart = i + 1
		}
	}
	spans = append(spans, [2]int{itemStart, end})

	columns := make(map[string]bool, len(opt.Columns))
	for _, c := range opt.Columns {
		columns[strings.ToLower(c)] = true
	}

	replacements := make(map[string]string, len(spans))
	// Pass 1: decide the new alias of every item and collect the old → new map.
	// Nothing is mutated yet: refusing to rename requires knowing whether any
	// old alias shadows a column name, which is only visible once all of them
	// are known.
	type edit struct {
		exprStart int
		exprEnd   int
		itemEnd   int
		newAl     string
	}
	edits := make([]edit, 0, len(spans))
	used := map[string]int{}
	for _, span := range spans {
		from, to := span[0], span[1]
		if from >= to {
			continue
		}
		// Explicit `AS name` at item depth.
		asAt, aliasAt := -1, -1
		for i := from; i < to; i++ {
			if toks[i].kind == canonWord && toks[i].upper == "AS" && toks[i].depth == toks[from].depth {
				asAt, aliasAt = i, i+1
				if aliasAt >= to {
					asAt, aliasAt = -1, -1
				}
				break
			}
		}
		if asAt < 0 && to-from >= 2 {
			// Implicit alias: `COUNT(*) cnt`.
			last := toks[to-1]
			prev := toks[to-2]
			if isNameTok(last) && !canonKeywords[strings.ToLower(last.text)] &&
				((prev.kind == canonPunct && prev.text == ")") || prev.kind == canonNumber ||
					(isNameTok(prev) && !canonKeywords[strings.ToLower(prev.text)])) {
				aliasAt = to - 1
			}
		}
		exprTo := to
		if aliasAt > 0 {
			exprTo = aliasAt
			if asAt > 0 {
				exprTo = asAt
			}
		}
		exprTok := toks[from:exprTo]
		if len(exprTok) == 0 {
			continue
		}
		old := ""
		if aliasAt > 0 {
			old = strings.ToLower(toks[aliasAt].text)
		}
		if isBareColumn(exprTok, old) {
			// `SELECT gender`, `SELECT gender AS gender`, `SELECT t.gender AS gen`:
			// the first two collapse to the column; a genuine rename is kept.
			newAl := ""
			if old != "" && old != strings.ToLower(exprTok[len(exprTok)-1].text) {
				newAl = old
			}
			edits = append(edits, edit{exprStart: from, exprEnd: exprTo, itemEnd: to, newAl: newAl})
			if old != "" && newAl != "" && old != newAl {
				replacements[old] = newAl
			}
			continue
		}
		newAl := derivedAlias(exprTok)
		if newAl == "" {
			continue
		}
		if n := used[newAl]; n > 0 {
			used[newAl] = n + 1
			newAl = newAl + "_" + itoaSmall(n+1)
		} else {
			used[newAl] = 1
		}
		edits = append(edits, edit{exprStart: from, exprEnd: exprTo, itemEnd: to, newAl: newAl})
		if old != "" && old != newAl {
			replacements[old] = newAl
		}
	}

	// Refuse the whole rename when an old alias is also a real column: a
	// reference to it outside the projection would be ambiguous.
	for old := range replacements {
		if columns[old] {
			return toks
		}
	}

	// Pass 2: rebuild the token slice.
	out := make([]canonTok, 0, len(toks)+len(edits)*2)
	editsByStart := make(map[int]edit, len(edits))
	for _, e := range edits {
		editsByStart[e.exprStart] = e
	}
	for i := 0; i < len(toks); {
		if e, ok := editsByStart[i]; ok {
			out = append(out, toks[e.exprStart:e.exprEnd]...)
			if e.newAl != "" {
				out = append(out,
					canonTok{kind: canonWord, text: "AS", upper: "AS", depth: toks[i].depth},
					canonTok{kind: canonWord, text: e.newAl, upper: strings.ToUpper(e.newAl), depth: toks[i].depth},
				)
			}
			i = e.itemEnd
			continue
		}
		t := toks[i]
		if isNameTok(t) && (i < start || i >= end) {
			if next, hit := replacements[strings.ToLower(t.text)]; hit {
				t.text, t.upper, t.kind = next, strings.ToUpper(next), canonWord
			}
		}
		out = append(out, t)
		i++
	}
	return out
}

// isBareColumn reports whether a projection is just a column reference (with
// optional table qualifier), optionally aliased to the same name.
func isBareColumn(toks []canonTok, alias string) bool {
	n := len(toks)
	if n == 0 || n > 3 {
		return false
	}
	for _, t := range toks {
		switch t.kind {
		case canonWord, canonIdent:
			if canonKeywords[strings.ToLower(t.text)] {
				return false
			}
		case canonPunct:
			if t.text != "." {
				return false
			}
		default:
			return false
		}
	}
	if alias == "" {
		return true
	}
	return strings.EqualFold(alias, toks[n-1].text)
}

// derivedAlias builds the canonical column name of a computed expression:
// `COUNT(*)` → count, `SUM(num)` → sum_num, `COUNT(DISTINCT name)` →
// count_distinct_name, `SUM(num)/COUNT(*)` → ratio_sum_num_count.
func derivedAlias(expr []canonTok) string {
	text := strings.ToLower(canonPrint(expr))
	text = strings.ReplaceAll(text, `"`, "")
	if text == "" {
		return ""
	}
	// Division at the top level is the one operator worth naming, because
	// "占比" queries are the ones users read most.
	if left, right, ok := splitTopLevel(text, "/"); ok {
		l, r := derivedAliasText(left), derivedAliasText(right)
		name := "ratio"
		if l != "" {
			name += "_" + l
		}
		if r != "" {
			name += "_" + r
		}
		return trimAlias(name)
	}
	return trimAlias(derivedAliasText(text))
}

// derivedAliasText is derivedAlias for an already-rendered expression.
func derivedAliasText(text string) string {
	text = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(text, `"`, "")))
	text = strings.TrimPrefix(text, "distinct ")
	if name, inner, ok := splitCall(text); ok {
		switch name {
		case "round", "trunc", "ceil", "floor", "abs":
			// Presentation-only wrappers must not change the metric's name:
			// ROUND(AVG(num), 2) and AVG(num) are the same metric.
			if first, _, ok := splitTopLevel(inner, ","); ok {
				return derivedAliasText(first)
			}
			return derivedAliasText(inner)
		}
		body := strings.TrimSpace(inner)
		distinct := strings.HasPrefix(strings.ToLower(body), "distinct ")
		if distinct {
			body = strings.TrimSpace(body[len("distinct "):])
		}
		if body == "" {
			return name
		}
		prefix := name
		if distinct {
			prefix = name + "_distinct"
		}
		if body == "*" || body == "" {
			return collapseAlias(text)
		}
		if parts := strings.Split(body, ","); len(parts) > 1 && name != "coalesce" {
			// Multi-argument functions other than COALESCE: keep the full shape.
			out := prefix
			for _, p := range parts {
				if a := collapseAlias(p); a != "" {
					out += "_" + a
				}
			}
			return out
		}
		sub := collapseAlias(body)
		if sub == "" {
			return prefix
		}
		return prefix + "_" + sub
	}
	return collapseAlias(text)
}

// splitCall splits `fn(args)` into its name and argument text.
func splitCall(text string) (name, args string, ok bool) {
	open := strings.Index(text, "(")
	if open <= 0 || !strings.HasSuffix(text, ")") {
		return "", "", false
	}
	name = strings.TrimSpace(text[:open])
	if name == "" || strings.ContainsAny(name, " ") {
		return "", "", false
	}
	return name, text[open+1 : len(text)-1], true
}

// splitTopLevel splits on sep when it appears outside parentheses.
func splitTopLevel(text, sep string) (string, string, bool) {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
		default:
			if depth == 0 && strings.HasPrefix(text[i:], sep) {
				return text[:i], text[i+len(sep):], true
			}
		}
	}
	return "", "", false
}

// collapseAlias reduces an expression fragment to an identifier-safe token.
func collapseAlias(text string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(text) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r >= 0x80:
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// trimAlias keeps alias names short enough for a wide result but long enough to
// stay recognisable.
func trimAlias(name string) string {
	name = strings.Trim(name, "_")
	if name == "" {
		return ""
	}
	if len(name) > 48 {
		name = strings.TrimRight(name[:48], "_")
	}
	return name
}

// canonTables rewrites table references to one canonical spelling: the
// dataset's qualified name when the dataset knows its schema, and the bare
// table name otherwise (a leading `public.` is dropped, because `public` is on
// every connection's search_path).
func canonTables(toks []canonTok, opt CanonicalOptions) []canonTok {
	table := strings.ToLower(strings.TrimSpace(opt.Table))
	if table == "" {
		return toks
	}
	schema := strings.ToLower(strings.TrimSpace(opt.Schema))
	out := make([]canonTok, 0, len(toks)+2)
	// tablePos[depth] is true while the next name at that depth is the table of
	// a FROM/JOIN item. Qualifying is only safe in that position: `FROM t t`
	// (an alias that happens to match the table) and `FROM (…) t` (a subquery
	// alias) are both valid SQL that this function used to turn into
	// `schema.t`, i.e. into a statement that no longer parses.
	tablePos := map[int]bool{}
	inFrom := map[int]bool{}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind == canonWord {
			switch t.upper {
			case "FROM", "JOIN":
				tablePos[t.depth] = true
				inFrom[t.depth] = true
			case "WHERE", "GROUP", "ORDER", "HAVING", "LIMIT", "OFFSET",
				"UNION", "EXCEPT", "INTERSECT", "WINDOW":
				tablePos[t.depth] = false
				inFrom[t.depth] = false
			case "ON", "USING", "SELECT":
				tablePos[t.depth] = false
			}
		}
		// A subquery or a table function in table position: the name that
		// follows the closing paren is an alias, never the table itself.
		if t.kind == canonPunct && t.text == "(" && tablePos[t.depth] {
			tablePos[t.depth] = false
		}
		if t.kind == canonPunct && t.text == "," && inFrom[t.depth] {
			tablePos[t.depth] = true
		}
		qualified := i >= 2 && toks[i-1].kind == canonPunct && toks[i-1].text == "." && isNameTok(toks[i-2])
		if !isNameTok(t) || !strings.EqualFold(t.text, table) {
			// A non-matching *identifier* in table position consumes it. The
			// keywords that set the position (FROM, JOIN) are words too, so they
			// must not consume it themselves.
			if isNameTok(t) && !canonKeywords[strings.ToLower(t.text)] && tablePos[t.depth] {
				if i+1 < len(toks) && toks[i+1].kind == canonPunct && toks[i+1].text == "." {
					// `other.table`: the schema comes first, the table is next.
					out = append(out, t)
					continue
				}
				tablePos[t.depth] = false
			}
			out = append(out, t)
			continue
		}
		if qualified {
			prefix := strings.ToLower(toks[i-2].text)
			switch {
			case schema != "" && prefix == schema:
				// Already the canonical spelling.
			case schema == "" && prefix == "public":
				// The dataset declares no schema; drop the default one so
				// `public.t` and `t` converge. Any other prefix is left alone:
				// rewriting it would silently point the query at a different
				// table than the one the statement named.
				out = out[:len(out)-2]
			}
			out = append(out, t)
			tablePos[t.depth] = false
			continue
		}
		if !tablePos[t.depth] {
			// An alias, a column, or a CTE that shares the table's name. Leaving
			// it alone is always safe; qualifying it is not.
			out = append(out, t)
			continue
		}
		if schema != "" {
			out = append(out,
				canonTok{kind: canonWord, text: schema, upper: strings.ToUpper(schema), depth: t.depth},
				canonTok{kind: canonPunct, text: ".", upper: ".", depth: t.depth},
			)
		}
		out = append(out, t)
		tablePos[t.depth] = false
	}
	return out
}

// canonCountStar folds `COUNT(1)` into `COUNT(*)`.
func canonCountStar(toks []canonTok) []canonTok {
	for i := 0; i+3 < len(toks); i++ {
		if toks[i].kind != canonWord || !strings.EqualFold(toks[i].text, "count") {
			continue
		}
		if toks[i+1].text != "(" || toks[i+2].kind != canonNumber || toks[i+2].text != "1" || toks[i+3].text != ")" {
			continue
		}
		toks[i+2] = canonTok{kind: canonOp, text: "*", upper: "*", depth: toks[i+2].depth}
	}
	return toks
}

// canonDropASC removes the default sort direction.
func canonDropASC(toks []canonTok) []canonTok {
	out := toks[:0]
	for i, t := range toks {
		if t.kind == canonWord && t.upper == "ASC" {
			next := canonTok{}
			if i+1 < len(toks) {
				next = toks[i+1]
			}
			endsItem := next.kind == canonPunct && (next.text == "," || next.text == ")")
			if endsItem || i+1 == len(toks) || (next.kind == canonWord && next.upper == "LIMIT") {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}

// canonOperators folds operator spellings to one form.
func canonOperators(toks []canonTok) []canonTok {
	for i := range toks {
		if toks[i].kind == canonOp && toks[i].text == "!=" {
			toks[i].text, toks[i].upper = "<>", "<>"
		}
	}
	return toks
}

/* ---------------------------------------------------------------- printer */

// canonPrint renders canonical text: keywords and functions upper-cased,
// identifiers lower-cased (quoted only when they must be), clauses on their own
// line, projection items one per line.
func canonPrint(toks []canonTok) string {
	var b strings.Builder
	prevClause := ""
	lineStart := true
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		text, kind := renderTok(toks, i)
		if text == "" {
			continue
		}
		// Clause layout at depth 0.
		if t.depth == 0 && t.kind == canonWord {
			switch t.upper {
			case "SELECT", "FROM", "WHERE", "HAVING", "LIMIT", "OFFSET", "UNION", "WINDOW":
				if b.Len() > 0 {
					b.WriteString("\n")
					lineStart = true
				}
				prevClause = t.upper
			case "GROUP", "ORDER":
				if b.Len() > 0 {
					b.WriteString("\n")
					lineStart = true
				}
				prevClause = t.upper
			}
		}
		// Separators.
		if b.Len() > 0 && !lineStart && needsSpace(toks, i, kind) {
			b.WriteString(" ")
		}
		b.WriteString(text)
		if t.depth == 0 && t.kind == canonPunct && t.text == "," && (prevClause == "SELECT" || prevClause == "GROUP" || prevClause == "ORDER") {
			b.WriteString("\n  ")
			lineStart = true
		} else {
			lineStart = false
		}
	}
	return strings.TrimSpace(b.String())
}

// renderTok returns the canonical spelling of one token.
func renderTok(toks []canonTok, i int) (string, int) {
	t := toks[i]
	switch t.kind {
	case canonString, canonNumber:
		return t.text, t.kind
	case canonOp:
		return t.text, t.kind
	case canonPunct:
		return t.text, t.kind
	case canonWord:
		low := strings.ToLower(t.text)
		nextIsParen := i+1 < len(toks) && toks[i+1].kind == canonPunct && toks[i+1].text == "("
		if canonKeywords[low] || (nextIsParen && !canonReserved[low]) {
			return strings.ToUpper(low), canonWord
		}
		return renderIdent(low), canonIdent
	case canonIdent:
		return renderIdent(t.text), canonIdent
	}
	return t.text, t.kind
}

// renderIdent emits an identifier bare when PostgreSQL would read it back
// identically, and quoted otherwise.
func renderIdent(name string) string {
	lower := strings.ToLower(name)
	if name == lower && !canonReserved[lower] && identLike(lower) {
		return lower
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func identLike(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || r >= 0x80:
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// needsSpace decides the separator before token i.
func needsSpace(toks []canonTok, i, _ int) bool {
	cur := toks[i]
	prev := toks[i-1]
	prevText, _ := renderTok(toks, i-1)

	switch {
	case cur.kind == canonPunct && (cur.text == ")" || cur.text == "," || cur.text == "." || cur.text == "::"):
		return false
	case cur.kind == canonPunct && cur.text == "(":
		// Function call vs keyword: `COUNT(` has no space, `IN (` does.
		if prev.kind == canonWord && !canonKeywords[strings.ToLower(prev.text)] {
			return false
		}
		return true
	case prev.kind == canonPunct && (prev.text == "(" || prev.text == "." || prev.text == "::"):
		return false
	case cur.kind == canonPunct && cur.text == "[":
		return false
	case cur.kind == canonPunct && cur.text == "]":
		return false
	case prev.kind == canonPunct && prev.text == "[":
		return false
	case cur.kind == canonOp && cur.text == "*" && prev.kind == canonPunct && prev.text == "(":
		return false
	case prev.kind == canonOp && prev.text == "::":
		return false
	case cur.kind == canonOp && cur.text == "::":
		return false
	case prevText == "-" && cur.kind == canonNumber:
		// Unary minus.
		return len(toks) > i-2 && toks[i-2].kind == canonPunct && (toks[i-2].text == "(" || toks[i-2].text == ",")
	default:
		return true
	}
}

func itoaSmall(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func isNameTok(t canonTok) bool {
	return t.kind == canonWord || t.kind == canonIdent
}
