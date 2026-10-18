package connectors

import "testing"

// TestGetOrDefaultResolvesURI covers the regression that made dataset sampling
// fail on every non-PostgreSQL engine: callers that hold a SQLAlchemy URI (not
// an engine name) used to fall through to the generic spec, whose identifier
// quoting is `"`. On MySQL that is a syntax error, so the data preview showed
// "Query execution failed" with no hint that the dialect was wrong.
func TestGetOrDefaultResolvesURI(t *testing.T) {
	cases := map[string]string{
		"mysql://root:secret@127.0.0.1:3306/riskos":  "mysql",
		"postgresql://superset:pw@localhost:5533/db": "postgresql",
		"mssql+pyodbc://sa:pw@host:1433/db":          "mssql",
		"clickhouse://default:@host:8123/default":    "clickhouse",
		"sqlite:////data/app.db":                     "sqlite",
		"presto://analyst@host:8080/hive/sales":      "presto",
	}
	for uri, want := range cases {
		spec := GetOrDefault(uri)
		if spec == nil {
			t.Fatalf("%s: nil spec", uri)
		}
		if got := spec.Engine(); got != want {
			t.Errorf("%s: resolved to %q, want %q", uri, got, want)
		}
	}
}

// TestGetOrDefaultQuotesPerDialect asserts the resolved spec quotes identifiers
// the way its engine expects — the actual symptom of the bug above.
func TestGetOrDefaultQuotesPerDialect(t *testing.T) {
	mysql := GetOrDefault("mysql://root:pw@127.0.0.1:3306/shop")
	got := mysql.SelectStar("shop.orders")
	if want := "SELECT * FROM `shop`.`orders`"; got != want {
		t.Errorf("mysql: got %q, want %q", got, want)
	}

	pg := GetOrDefault("postgresql://u:pw@localhost:5432/db")
	got = pg.SelectStar("public.orders")
	if want := `SELECT * FROM "public"."orders"`; got != want {
		t.Errorf("postgres: got %q, want %q", got, want)
	}
}

// TestGetOrDefaultStillAcceptsEngineNames makes sure the plain-name path kept
// working after the URI branch was added.
func TestGetOrDefaultStillAcceptsEngineNames(t *testing.T) {
	spec := GetOrDefault("mysql")
	if spec.Engine() != "mysql" {
		t.Fatalf("got %q, want mysql", spec.Engine())
	}
	if unknown := GetOrDefault("not_a_real_engine"); unknown == nil {
		t.Fatal("unknown engine must still return the generic fallback")
	}
}
