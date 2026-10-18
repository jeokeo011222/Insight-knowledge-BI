package ai

import "testing"

var canonOpt = CanonicalOptions{
	Table:   "birth_names",
	Columns: []string{"id", "name", "gender", "state", "num", "ds"},
}

func canon(statement string) string { return canonicalizeSQL(statement, canonOpt) }

// TestCanonicalStyleVariantsCollapse is the whole point of the file: three ways
// of writing the same query must produce one text.
func TestCanonicalStyleVariantsCollapse(t *testing.T) {
	variants := []string{
		`SELECT COUNT(*) AS record_count FROM public.birth_names`,
		`select count(1) as total from "birth_names"`,
		"SELECT COUNT(*)\nFROM \"public\".\"birth_names\";",
	}
	want := canon(variants[0])
	if want == "" {
		t.Fatal("canonical form is empty")
	}
	for _, v := range variants[1:] {
		if got := canon(v); got != want {
			t.Errorf("variant did not collapse:\n  in:   %s\n  got:  %q\n  want: %q", v, got, want)
		}
	}
}

func TestCanonicalAggregateAliases(t *testing.T) {
	cases := map[string]string{
		`SELECT COUNT(*) AS record_count FROM birth_names`:            "SELECT COUNT(*) AS count\nFROM birth_names",
		`SELECT SUM(num) AS total FROM birth_names`:                   "SELECT SUM(num) AS sum_num\nFROM birth_names",
		`SELECT COUNT(DISTINCT name) AS n FROM birth_names`:           "SELECT COUNT(DISTINCT name) AS count_distinct_name\nFROM birth_names",
		`SELECT ROUND(AVG(num), 2) AS avg_births FROM birth_names`:    "SELECT ROUND(AVG(num), 2) AS avg_num\nFROM birth_names",
		`SELECT MIN(num) AS mn, MAX(num) AS mx FROM birth_names`:      "SELECT MIN(num) AS min_num,\n  MAX(num) AS max_num\nFROM birth_names",
		`SELECT SUM(num) / COUNT(*) AS per_record FROM birth_names`:   "SELECT SUM(num) / COUNT(*) AS ratio_sum_num_count\nFROM birth_names",
		`SELECT state, COUNT(*) AS c FROM birth_names GROUP BY state`: "SELECT state,\n  COUNT(*) AS count\nFROM birth_names\nGROUP BY state",
		`SELECT gender AS gender FROM birth_names`:                    "SELECT gender\nFROM birth_names",
	}
	for in, want := range cases {
		if got := canon(in); got != want {
			t.Errorf("alias canonicalisation:\n  in:   %s\n  got:  %q\n  want: %q", in, got, want)
		}
	}
}

// TestCanonicalRewritesAliasReferences keeps GROUP BY / ORDER BY pointing at
// the renamed column instead of at a name that no longer exists.
func TestCanonicalRewritesAliasReferences(t *testing.T) {
	got := canon(`SELECT gender, COUNT(*) AS cnt FROM birth_names GROUP BY gender ORDER BY cnt DESC`)
	want := "SELECT gender,\n  COUNT(*) AS count\nFROM birth_names\nGROUP BY gender\nORDER BY count DESC"
	if got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
}

// TestCanonicalSkipsRenameOnColumnCollision: renaming would make `WHERE num`
// ambiguous, so the statement keeps the model's aliases and only gets formatted.
func TestCanonicalSkipsRenameOnColumnCollision(t *testing.T) {
	got := canon(`SELECT SUM(num) AS num FROM birth_names WHERE num > 5`)
	if got != "SELECT SUM(num) AS num\nFROM birth_names\nWHERE num > 5" {
		t.Fatalf("alias rename should have been skipped, got %q", got)
	}
}

// TestCanonicalKeepsComplexStatements: a CTE is not a flat query, so only the
// formatting is applied — renaming an inner alias could break an outer
// reference. Clauses inside the CTE stay on one line; the spacing is still
// canonical, which is what makes two runs comparable.
func TestCanonicalKeepsComplexStatements(t *testing.T) {
	got := canon(`WITH t AS (SELECT state, COUNT(*) AS c FROM birth_names GROUP BY state) SELECT state, c FROM t WHERE c > 10`)
	if want := "WITH t AS (SELECT state, COUNT(*) AS c FROM birth_names GROUP BY state)\nSELECT state,\n  c\nFROM t\nWHERE c > 10"; got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
	if again := canon(`with t as ( select  state , count( * ) as c from "birth_names" group by state ) select state , c from t where c > 10 ;`); again != got {
		t.Fatalf("CTE formatting is not canonical:\n a=%q\n b=%q", got, again)
	}
}

func TestCanonicalLiteralsAndIdentifiers(t *testing.T) {
	got := canon(`SELECT "Gender", COUNT(*) FROM birth_names WHERE gender = 'F' AND name LIKE 'Jo%'`)
	want := "SELECT \"Gender\",\n  COUNT(*) AS count\nFROM birth_names\nWHERE gender = 'F' AND name LIKE 'Jo%'"
	if got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
	// Escaped quotes inside a literal must survive byte for byte.
	got = canon(`SELECT COUNT(*) FROM birth_names WHERE name = 'O''Brien'`)
	if want := "SELECT COUNT(*) AS count\nFROM birth_names\nWHERE name = 'O''Brien'"; got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
}

func TestCanonicalOperators(t *testing.T) {
	got := canon(`SELECT COUNT(*) AS c FROM birth_names WHERE num != 0 AND state <> 'CA'`)
	if want := "SELECT COUNT(*) AS count\nFROM birth_names\nWHERE num <> 0 AND state <> 'CA'"; got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
	got = canon(`SELECT state FROM birth_names ORDER BY state ASC`)
	if want := "SELECT state\nFROM birth_names\nORDER BY state"; got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
}

func TestCanonicalSchemaQualification(t *testing.T) {
	// Unknown schema: the public prefix goes away, the bare name stays.
	if got := canon(`SELECT COUNT(*) FROM public.birth_names`); got != "SELECT COUNT(*) AS count\nFROM birth_names" {
		t.Fatalf("got %q", got)
	}
	// Known schema: the bare name gains it, so both spellings converge.
	withSchema := canonOpt
	withSchema.Schema = "analytics"
	a := canonicalizeSQL(`SELECT COUNT(*) FROM birth_names`, withSchema)
	b := canonicalizeSQL(`SELECT COUNT(*) FROM analytics.birth_names`, withSchema)
	if a != b || a != "SELECT COUNT(*) AS count\nFROM analytics.birth_names" {
		t.Fatalf("schema qualification did not converge:\n a=%q\n b=%q", a, b)
	}
}

// TestCanonicalDateTruncAlias covers the dimension case: a computed time bucket
// must also get a stable name, and the name must not depend on the model.
func TestCanonicalDateTruncAlias(t *testing.T) {
	a := canon(`SELECT DATE_TRUNC('month', ds) AS month, COUNT(*) AS n FROM birth_names GROUP BY 1 ORDER BY 1`)
	b := canon(`SELECT date_trunc('month', "ds") AS m, count(1) AS c FROM "birth_names" GROUP BY 1 ORDER BY 1`)
	if a != b {
		t.Fatalf("time bucket did not converge:\n a=%q\n b=%q", a, b)
	}
	if a != "SELECT DATE_TRUNC('month', ds) AS date_trunc_month_ds,\n  COUNT(*) AS count\nFROM birth_names\nGROUP BY 1\nORDER BY 1" {
		t.Fatalf("unexpected canonical form: %q", a)
	}
}

func TestCanonicalEmptyAndBroken(t *testing.T) {
	if got := canonicalizeSQL("", canonOpt); got != "" {
		t.Fatalf("empty input must stay empty, got %q", got)
	}
	broken := `SELECT 'unterminated FROM birth_names`
	if got := canonicalizeSQL(broken, canonOpt); got != broken {
		t.Fatalf("unparsable input must be returned unchanged, got %q", got)
	}
}

func TestResultFingerprintIgnoresRowOrder(t *testing.T) {
	a := []map[string]any{{"state": "CA", "count": 10}, {"state": "TX", "count": 20}}
	b := []map[string]any{{"state": "TX", "count": 20}, {"state": "CA", "count": 10}}
	if resultFingerprint(a) != resultFingerprint(b) {
		t.Fatal("row order must not change the fingerprint")
	}
	// A changed number is a different result: a replay must not reuse it.
	c := []map[string]any{{"state": "CA", "count": 11}, {"state": "TX", "count": 20}}
	if resultFingerprint(a) == resultFingerprint(c) {
		t.Fatal("a changed value must change the fingerprint")
	}
	// So is an extra row.
	d := append(append([]map[string]any{}, a...), map[string]any{"state": "NY", "count": 5})
	if resultFingerprint(a) == resultFingerprint(d) {
		t.Fatal("an extra row must change the fingerprint")
	}
	if resultFingerprint(nil) != "" {
		t.Fatal("nil rows have no fingerprint")
	}
}
