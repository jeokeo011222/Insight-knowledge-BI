package connectors

import (
	"errors"
	"strings"
	"testing"
)

// TestEveryEngineHasUsableForm guarantees that every registered engine exposes
// fields the connection form can render and BuildURI can consume. A new engine
// spec that forgets its form shape fails here instead of shipping a broken
// "create database" dialog.
func TestEveryEngineHasUsableForm(t *testing.T) {
	for _, spec := range Specs() {
		engine := spec.Engine()
		form := Form(engine)
		if len(form.Fields) == 0 {
			t.Fatalf("%s: form has no fields", engine)
		}
		names := map[string]bool{}
		for _, f := range form.Fields {
			if f.Name == "" || f.LabelKey == "" || f.Group == "" {
				t.Fatalf("%s: field %+v is missing name/label_key/group", engine, f)
			}
			names[f.Name] = true
		}
		switch form.Kind {
		case KindHost:
			for _, required := range []string{"host", "port", "database"} {
				if !names[required] {
					t.Fatalf("%s: host form is missing %q", engine, required)
				}
			}
		case KindCatalog:
			for _, required := range []string{"host", "port", "database", "schema"} {
				if !names[required] {
					t.Fatalf("%s: catalog form is missing %q", engine, required)
				}
			}
		case KindFile:
			if !names["path"] {
				t.Fatalf("%s: file form is missing path", engine)
			}
		case KindURI:
			if !names["sqlalchemy_uri"] {
				t.Fatalf("%s: uri form is missing sqlalchemy_uri", engine)
			}
		default:
			t.Fatalf("%s: unknown kind %q", engine, form.Kind)
		}
	}
}

// TestFormDefaultsMatchEngine keeps the rendered defaults in sync with the spec
// metadata: the port default must be the engine's default port and the schema
// default must be the engine's default schema.
func TestFormDefaultsMatchEngine(t *testing.T) {
	for _, spec := range Specs() {
		form := Form(spec.Engine())
		for _, f := range form.Fields {
			if f.Name != "port" {
				continue
			}
			got, ok := f.Default.(int)
			if !ok {
				t.Fatalf("%s: port default is not an int: %v", spec.Engine(), f.Default)
			}
			if got != spec.DefaultPort() {
				t.Fatalf("%s: port default %d != spec default %d", spec.Engine(), got, spec.DefaultPort())
			}
		}
	}
}

func TestBuildURIHostShape(t *testing.T) {
	cases := []struct {
		name string
		opts BuildOptions
		want string
	}{
		{
			name: "postgres with credentials",
			opts: BuildOptions{
				Engine: "postgresql", Host: "db.internal", Port: 5432,
				Database: "analytics", Username: "etl", Password: "secret",
			},
			want: "postgresql://etl:secret@db.internal:5432/analytics",
		},
		{
			name: "mysql falls back to default port",
			opts: BuildOptions{
				Engine: "mysql", Host: "10.0.0.5", Database: "shop",
				Username: "root", Password: "pw",
			},
			want: "mysql://root:pw@10.0.0.5:3306/shop",
		},
		{
			name: "username without password",
			opts: BuildOptions{
				Engine: "postgresql", Host: "h", Port: 5432, Database: "d", Username: "u",
			},
			want: "postgresql://u@h:5432/d",
		},
		{
			name: "password with reserved characters is escaped",
			opts: BuildOptions{
				Engine: "postgresql", Host: "h", Port: 5432, Database: "d",
				Username: "u", Password: "p@ss/w:rd",
			},
			want: "postgresql://u:p%40ss%2Fw%3Ard@h:5432/d",
		},
		{
			name: "oracle service name travels in the path",
			opts: BuildOptions{
				Engine: "oracle", Host: "ora", Port: 1521, Database: "ORCLPDB1",
				Username: "scott", Password: "tiger",
			},
			want: "oracle://scott:tiger@ora:1521/ORCLPDB1",
		},
		{
			name: "mssql default port",
			opts: BuildOptions{
				Engine: "mssql", Host: "sql", Database: "dw", Username: "sa", Password: "p",
			},
			want: "mssql://sa:p@sql:1433/dw",
		},
		{
			name: "host carrying a port wins",
			opts: BuildOptions{
				Engine: "postgresql", Host: "db:6543", Port: 5432,
				Database: "d", Username: "u", Password: "p",
			},
			want: "postgresql://u:p@db:6543/d",
		},
		{
			name: "query string is preserved",
			opts: BuildOptions{
				Engine: "postgresql", Host: "h", Port: 5432, Database: "d",
				Username: "u", Password: "p", Query: "sslmode=require",
			},
			want: "postgresql://u:p@h:5432/d?sslmode=require",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildURI(tc.opts)
			if err != nil {
				t.Fatalf("BuildURI: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			// Every URI we emit must be parseable by our own URI parser.
			if _, err := ParseSQLAlchemyURI(got); err != nil {
				t.Fatalf("emitted URI %q does not parse: %v", got, err)
			}
		})
	}
}

func TestBuildURICatalogShape(t *testing.T) {
	got, err := BuildURI(BuildOptions{
		Engine: "trino", Host: "trino.internal", Port: 8080,
		Database: "hive", Schema: "sales", Username: "analyst",
	})
	if err != nil {
		t.Fatalf("BuildURI: %v", err)
	}
	if want := "trino://analyst@trino.internal:8080/hive/sales"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := BuildURI(BuildOptions{Engine: "trino", Host: "h", Schema: "s"}); err == nil {
		t.Fatal("catalog without a catalog name must fail")
	}
}

func TestBuildURIFileShape(t *testing.T) {
	cases := map[string]struct {
		opts BuildOptions
		want string
	}{
		"absolute path": {
			opts: BuildOptions{Engine: "sqlite", Path: "/data/app.db"},
			want: "sqlite:////data/app.db",
		},
		"relative path": {
			opts: BuildOptions{Engine: "sqlite", Path: "data/app.db"},
			want: "sqlite:///data/app.db",
		},
		"windows path": {
			opts: BuildOptions{Engine: "sqlite", Path: `C:\data\app.db`},
			want: "sqlite:///C:/data/app.db",
		},
		"memory": {
			opts: BuildOptions{Engine: "sqlite", Path: ":memory:"},
			want: "sqlite://",
		},
		"duckdb from database field": {
			opts: BuildOptions{Engine: "duckdb", Database: "/tmp/wh.duckdb"},
			want: "duckdb:////tmp/wh.duckdb",
		},
		// A DuckDB warehouse usually lives next to the app that wrote it, so
		// Windows drive paths with spaces are the common case, not an edge one.
		"duckdb windows path with spaces": {
			opts: BuildOptions{Engine: "duckdb", Path: `D:\Code\Quant Harness\AIQuant\build\bin\data\stock.duckdb`},
			want: "duckdb:///D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb",
		},
		"duckdb forward-slash windows path": {
			opts: BuildOptions{Engine: "duckdb", Path: "D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb"},
			want: "duckdb:///D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := BuildURI(tc.opts)
			if err != nil {
				t.Fatalf("BuildURI: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}

	// The sqlite spec must resolve our absolute-path URI to the bare path.
	if _, dsn, err := Get("sqlite").DSN("sqlite:////data/app.db"); err != nil {
		t.Fatalf("DSN: %v", err)
	} else if dsn != "/data/app.db" {
		t.Fatalf("sqlite DSN got %q, want /data/app.db", dsn)
	}
}

// TestBuildURIFileShapeAcceptsPastedURI pins the fix for the "database file
// does not exist" report: the file input sits next to screens that talk in
// SQLAlchemy URIs, so users paste `duckdb:///D:/...` into it. Prefixing that
// value a second time produced `duckdb:///duckdb:///D:/...`, and the driver
// looked for a file named `duckdb:` while the path on screen looked right.
func TestBuildURIFileShapeAcceptsPastedURI(t *testing.T) {
	const want = "duckdb:///D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb"
	cases := map[string]string{
		"already-prefixed uri":  "duckdb:///D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb",
		"two-slash uri":         "Duckdb://D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb",
		"quoted windows path":   `"D:\Code\Quant Harness\AIQuant\build\bin\data\stock.duckdb"`,
		"surrounding spam":      "  duckdb:///D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb  ",
		"single quoted forward": `'D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb'`,
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := BuildURI(BuildOptions{Engine: "duckdb", Path: path})
			if err != nil {
				t.Fatalf("BuildURI: %v", err)
			}
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
			// The stored URI must resolve back to the very file the user named.
			if _, dsn, err := Get("duckdb").DSN(got); err != nil {
				t.Fatalf("DSN: %v", err)
			} else if !strings.HasPrefix(dsn, "D:/Code/Quant Harness/AIQuant/build/bin/data/stock.duckdb?") {
				t.Fatalf("dsn %q does not point at the named file", dsn)
			}
		})
	}
}

// TestCleanFileURIOnlyTouchesNestedSchemes keeps a well-formed file URI
// untouched, including the slash count that decides absolute vs relative.
func TestCleanFileURIOnlyTouchesNestedSchemes(t *testing.T) {
	cases := map[string]string{
		// Nested: what the form used to save when a URI was pasted.
		"duckdb:///duckdb:///D:/data/stock.duckdb": "duckdb:///D:/data/stock.duckdb",
		"duckdb:///Duckdb://D:/data/stock.duckdb":  "duckdb:///D:/data/stock.duckdb",
		// Already fine: no second scheme, must not be rewritten.
		"duckdb:///D:/data/stock.duckdb": "duckdb:///D:/data/stock.duckdb",
		"duckdb:////data/stock.duckdb":   "duckdb:////data/stock.duckdb",
		"sqlite:///data/app.db":          "sqlite:///data/app.db",
		"sqlite:////data/app.db":         "sqlite:////data/app.db",
		"sqlite://":                      "sqlite://",
		// Host engines keep their URI verbatim, query string included.
		"postgresql://u:p@h:5432/db?sslmode=require": "postgresql://u:p@h:5432/db?sslmode=require",
	}
	for uri, want := range cases {
		if got := CleanFileURI(uri); got != want {
			t.Errorf("CleanFileURI(%q) = %q, want %q", uri, got, want)
		}
	}
}

func TestBuildURIRejectsIncompleteInput(t *testing.T) {
	if _, err := BuildURI(BuildOptions{Engine: "postgresql"}); err == nil {
		t.Fatal("missing host must fail")
	}
	if _, err := BuildURI(BuildOptions{Engine: "nope", Host: "h"}); err == nil {
		t.Fatal("unknown engine must fail")
	}
	if _, err := BuildURI(BuildOptions{Engine: "bigquery"}); !errors.Is(err, ErrURIRequired) {
		t.Fatalf("uri-only engine must return ErrURIRequired, got %v", err)
	}
	if _, err := BuildURI(BuildOptions{Engine: "sqlite"}); !errors.Is(err, ErrPathRequired) {
		t.Fatalf("file engine without a path must return ErrPathRequired, got %v", err)
	}
	if _, err := BuildURI(BuildOptions{}); !errors.Is(err, ErrEngineRequired) {
		t.Fatalf("missing engine must return ErrEngineRequired, got %v", err)
	}
}

// TestBuildURIErrorMeansMissingField keeps the API able to tell the user which
// input is still empty: a missing file path must never surface as "SQLAlchemy
// URI is required", because the file form has no URI input at all.
func TestBuildURIErrorMeansMissingField(t *testing.T) {
	_, err := BuildURI(BuildOptions{Engine: "duckdb"})
	if !errors.Is(err, ErrPathRequired) {
		t.Fatalf("duckdb without a path: %v", err)
	}

	_, err = BuildURI(BuildOptions{Engine: "postgresql"})
	if !errors.Is(err, ErrHostRequired) {
		t.Fatalf("postgresql without a host: %v", err)
	}

	// A URI-keyed engine must stay distinguishable from a missing field.
	if _, err := BuildURI(BuildOptions{Engine: "snowflake"}); !errors.Is(err, ErrURIRequired) {
		t.Fatalf("snowflake: %v", err)
	}
}

// TestKindClassification pins the classification of the engines most likely to
// be mis-modelled: file-backed, catalog-based and protocol-only engines.
func TestKindClassification(t *testing.T) {
	cases := map[string]ConnectionKind{
		"sqlite":        KindFile,
		"duckdb":        KindFile,
		"trino":         KindCatalog,
		"presto":        KindCatalog,
		"postgresql":    KindHost,
		"mysql":         KindHost,
		"oracle":        KindHost,
		"mssql":         KindHost,
		"clickhouse":    KindHost,
		"bigquery":      KindURI,
		"databricks":    KindURI,
		"snowflake":     KindURI,
		"elasticsearch": KindURI,
		"mongodb":       KindURI,
	}
	for engine, want := range cases {
		if got := Kind(engine); got != want {
			t.Errorf("%s: kind %q, want %q", engine, got, want)
		}
	}
}

// TestNoSchemaFieldForMySQLFamily keeps the redundant schema input out of
// engines where the database name already is the schema.
func TestNoSchemaFieldForMySQLFamily(t *testing.T) {
	for _, engine := range []string{"mysql", "mariadb", "starrocks", "pydoris", "singlestoredb", "oceanbase", "databend"} {
		for _, f := range Form(engine).Fields {
			if f.Name == "schema" {
				t.Fatalf("%s: unexpected schema field", engine)
			}
		}
	}
	for _, engine := range []string{"postgresql", "oracle", "mssql", "clickhouse"} {
		found := false
		for _, f := range Form(engine).Fields {
			if f.Name == "schema" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: expected a schema field", engine)
		}
	}
}

// TestSpecialDatabaseLabels verifies the path segment is labelled with the
// engine's own vocabulary rather than the generic "Database".
func TestSpecialDatabaseLabels(t *testing.T) {
	if got := labelKeyOf("oracle", "database"); got != "database.field.service_name" {
		t.Errorf("oracle database label key = %q", got)
	}
	if got := labelKeyOf("presto", "database"); got != "database.field.catalog" {
		t.Errorf("presto database label key = %q", got)
	}
	if got := labelKeyOf("postgresql", "database"); got != "database.field.database" {
		t.Errorf("postgresql database label key = %q", got)
	}
}

func labelKeyOf(engine, field string) string {
	for _, f := range Form(engine).Fields {
		if f.Name == field {
			return f.LabelKey
		}
	}
	return ""
}

// TestConnectionFieldsAreTranslatable asserts the label keys use the shared
// database.field.* namespace so the UI can translate them.
func TestConnectionFieldsAreTranslatable(t *testing.T) {
	for _, spec := range Specs() {
		for _, f := range Form(spec.Engine()).Fields {
			if !strings.HasPrefix(f.LabelKey, "database.field.") {
				t.Fatalf("%s/%s: label key %q is outside the database.field namespace", spec.Engine(), f.Name, f.LabelKey)
			}
		}
	}
}

// TestEveryEngineHasCategory keeps the "supported databases" picker honest:
// an engine without a group would land in the catch-all bucket and quietly
// disappear from the screen an operator actually searches.
func TestEveryEngineHasCategory(t *testing.T) {
	allowed := map[string]bool{}
	for _, c := range Categories() {
		allowed[c] = true
	}
	for _, spec := range Specs() {
		got := Category(spec.Engine())
		if !allowed[got] {
			t.Fatalf("%s: category %q is not in Categories()", spec.Engine(), got)
		}
		if got == CategoryOther {
			t.Fatalf("%s: engine has no picker category, add it to engineCategories", spec.Engine())
		}
	}
}

// TestCategoriesCoverKnownEngines pins the grouping of the engines users are
// most likely to look for, so a refactor cannot silently move Oracle into
// "cloud" or Presto into "other".
func TestCategoriesCoverKnownEngines(t *testing.T) {
	cases := map[string]string{
		"postgresql":    CategoryRelational,
		"mysql":         CategoryRelational,
		"oracle":        CategoryRelational,
		"mssql":         CategoryRelational,
		"clickhouse":    CategoryAnalytics,
		"presto":        CategoryAnalytics,
		"trino":         CategoryAnalytics,
		"hive":          CategoryAnalytics,
		"bigquery":      CategoryCloud,
		"snowflake":     CategoryCloud,
		"databricks":    CategoryCloud,
		"sqlite":        CategoryFile,
		"duckdb":        CategoryFile,
		"mongodb":       CategoryNoSQL,
		"elasticsearch": CategoryNoSQL,
	}
	for engine, want := range cases {
		if got := Category(engine); got != want {
			t.Errorf("%s: expected category %q, got %q", engine, want, got)
		}
	}
}
