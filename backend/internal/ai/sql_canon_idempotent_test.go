package ai

import (
	"strings"
	"testing"
)

// The canonicalizer runs on every statement that goes to the database, and a
// replayed statement is canonicalized a second time on the way back in. So it
// has to be a normal form: canon(canon(x)) == canon(x). If it is not, a replayed
// query can drift away from the text that was recorded for it, and "same
// question, same SQL" stops being checkable.
func TestCanonicalIsIdempotent(t *testing.T) {
	opt := CanonicalOptions{
		Schema: "public", Table: "birth_names",
		Columns: []string{"id", "name", "gender", "state", "num", "ds", "tags"},
	}
	for _, sql := range canonCorpus {
		once := canonicalizeSQL(sql, opt)
		twice := canonicalizeSQL(once, opt)
		if once != twice {
			t.Errorf("not idempotent:/n  in:    %s\n  once:  %q\n  twice: %q", sql, once, twice)
		}
	}
}

// Every literal and every number must survive verbatim. The two bugs this suite
// was written after both changed meaning without changing syntax: `tags[1]`
// became the alias `"1"`, and an alias got a schema prefix. A dropped or
// rewritten literal is the same class of failure — it returns wrong rows and
// logs nothing — so it gets its own guard.
func TestCanonicalKeepsLiteralsAndNumbers(t *testing.T) {
	opt := CanonicalOptions{Schema: "public", Table: "birth_names"}
	for _, sql := range canonCorpus {
		out := canonicalizeSQL(sql, opt)
		for _, lit := range extractLiterals(sql) {
			if !strings.Contains(out, lit) {
				t.Errorf("literal lost | lit: %s\n  in:    %s\n  out:   %q", lit, sql, out)
			}
		}
		for _, nm := range extractNumbers(sql) {
			// `count(1)` → `count(*)` is the one intentional rewrite: that 1 is
			// not data, it is the constant the old spelling happened to count.
			if nm == "1" && hasCountOne(sql) {
				continue
			}
			if !strings.Contains(out, nm) {
				t.Errorf("number lost | num: %s\n  in:    %s\n  out:   %q", nm, sql, out)
			}
		}
		// The table has to still be named. A rewrite that loses it would query
		// something else entirely (or nothing).
		if strings.Contains(strings.ToLower(sql), "from birth_names") &&
			!strings.Contains(strings.ToLower(out), "birth_names") {
			t.Errorf("table lost | in: %s\n  out: %q", sql, out)
		}
	}
}

// canonCorpus is the shapes the benchmark actually produces, plus the awkward
// ones a hand-written rewriter tends to break: window frames, FILTER, casts,
// ARRAY literals, LATERAL, set operations, comments and dollar quoting.
var canonCorpus = []string{
	`SELECT COUNT(*) FROM birth_names`,
	`select count(1) from "birth_names"`,
	`SELECT COUNT(*) AS record_count FROM public.birth_names`,
	`SELECT COUNT(*) AS total FROM "public"."birth_names" WHERE state = 'CA'`,
	`SELECT gender, SUM(num) AS total FROM birth_names GROUP BY gender ORDER BY total DESC`,
	`SELECT DISTINCT state FROM birth_names ORDER BY state ASC`,
	`SELECT COUNT(DISTINCT name) FROM birth_names WHERE num != 0`,
	`SELECT state, COUNT(*) FROM birth_names WHERE num > 100 GROUP BY state HAVING COUNT(*) > 5`,
	`SELECT name, num FROM birth_names ORDER BY num DESC LIMIT 10`,
	`SELECT DATE_TRUNC('year', ds) AS y, SUM(num) FROM birth_names GROUP BY 1 ORDER BY 1`,
	`SELECT CASE WHEN num > 1000 THEN 'big' ELSE 'small' END AS bucket, COUNT(*) FROM birth_names GROUP BY 1`,
	`SELECT COUNT(*) FILTER (WHERE gender = 'F') AS female FROM birth_names`,
	`SELECT state, SUM(num) OVER (PARTITION BY state ORDER BY ds ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM birth_names`,
	`SELECT ROW_NUMBER() OVER (PARTITION BY state ORDER BY num DESC) AS rn, name FROM birth_names`,
	`SELECT num::numeric / 100 AS pct FROM birth_names`,
	`SELECT CAST(num AS numeric) FROM birth_names`,
	`SELECT name FROM birth_names WHERE state = ANY(ARRAY['CA','NY'])`,
	`SELECT name FROM birth_names WHERE tags[1] = 'x' AND arr[i][2] > 0`,
	`SELECT name FROM birth_names WHERE num IS DISTINCT FROM 0`,
	`SELECT string_agg(name, ',' ORDER BY name) FROM birth_names`,
	`SELECT a.state, b.cnt FROM birth_names a JOIN (SELECT state, COUNT(*) AS cnt FROM birth_names GROUP BY state) b ON a.state = b.state`,
	`SELECT state FROM birth_names UNION ALL SELECT state FROM birth_names`,
	`SELECT * FROM birth_names WHERE name ILIKE '%a%'`,
	`SELECT COUNT(*) FROM birth_names WHERE ds >= CURRENT_DATE - INTERVAL '1 day'`,
	`SELECT COUNT(*) FROM birth_names WHERE ds >= now() - interval '7 days'`,
	`SELECT COUNT(*) FROM birth_names /* block comment */ WHERE state = 'CA'`,
	"-- leading comment\nSELECT COUNT(*) FROM birth_names",
	`SELECT COUNT(*) FROM birth_names WHERE name = $tag$ O'Brien $tag$`,
	`SELECT COUNT(*) FROM birth_names WHERE name = 'O''Brien'`,
	`SELECT l.state FROM birth_names b, LATERAL (SELECT b.state AS state) l`,
	`WITH t AS (SELECT state, COUNT(*) AS c FROM birth_names GROUP BY state) SELECT * FROM t`,
	`SELECT COUNT(*) FROM birth_names birth_names`,
	`SELECT COUNT(*) FROM (SELECT num FROM birth_names) birth_names`,
	`SELECT 1 != 2, 3 <> 4, NULL IS NULL`,
	`SELECT COALESCE(SUM(num), 0) AS total FROM birth_names`,
	`SELECT ROUND(AVG(num), 2) AS avg_num FROM birth_names`,
	`SELECT state, COUNT(*) AS count FROM birth_names GROUP BY state ORDER BY count DESC, state`,
	`SELECT EXTRACT(YEAR FROM ds) AS y, SUM(num) FROM birth_names GROUP BY 1`,
	`SELECT COUNT(*) FROM birth_names WHERE state IN ('CA', 'NY', 'TX')`,
}

// extractLiterals returns the single-quoted and dollar-quoted literals, exactly
// as they appear, so the test checks byte equality rather than "something
// similar survived".
func extractLiterals(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			j := i + 1
			for j < len(s) {
				if s[j] == '\'' {
					if j+1 < len(s) && s[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j < len(s) {
				out = append(out, s[i:j+1])
				i = j
			}
		case '$':
			j := i + 1
			for j < len(s) && s[j] != '$' && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
				j++
			}
			if j < len(s) && s[j] == '$' {
				tag := s[i : j+1]
				if end := strings.Index(s[j+1:], tag); end >= 0 {
					stop := j + 1 + end + len(tag)
					out = append(out, s[i:stop])
					i = stop - 1
				}
			}
		}
	}
	return out
}

// extractNumbers returns numeric literals: digit runs not glued to an identifier.
// `birth_names2` is an identifier, `100` and `2.5` are numbers.
func extractNumbers(s string) []string {
	var out []string
	isWord := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			inQuote = !inQuote
			continue
		}
		if inQuote || c < '0' || c > '9' {
			continue
		}
		if i > 0 && isWord(s[i-1]) {
			continue
		}
		j := i
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
			j++
		}
		if j < len(s) && isWord(s[j]) {
			i = j - 1
			continue
		}
		out = append(out, s[i:j])
		i = j - 1
	}
	return out
}

// hasCountOne reports whether the statement spells an aggregate count with the
// constant 1, which the canonicalizer rewrites to count(*).
func hasCountOne(sql string) bool {
	lower := strings.ToLower(sql)
	lower = strings.Join(strings.Fields(lower), " ")
	return strings.Contains(lower, "count(1)") || strings.Contains(lower, "count (1)")
}
