package connectors

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDuckDBDSN pins how a duckdb URI becomes the path the driver opens. The
// Windows drive letter and the spaces that usually sit next to a warehouse file
// are exactly what a naive TrimPrefix gets wrong.
func TestDuckDBDSN(t *testing.T) {
	spec := Get("duckdb")
	if spec == nil {
		t.Fatal("duckdb spec is not registered")
	}
	cases := map[string]string{
		"duckdb:////data/wh.duckdb":                         "/data/wh.duckdb?access_mode=read_only",
		"duckdb:///D:/Code/Quant Harness/data/stock.duckdb": "D:/Code/Quant Harness/data/stock.duckdb?access_mode=read_only",
		"duckdb://D:/Code/Quant Harness/data/stock.duckdb":  "D:/Code/Quant Harness/data/stock.duckdb?access_mode=read_only",
		"duckdb:////data/wh.duckdb?threads=4":               "/data/wh.duckdb?threads=4&access_mode=read_only",
	}
	for uri, want := range cases {
		_, dsn, err := spec.DSN(uri)
		if err != nil {
			t.Fatalf("%s: %v", uri, err)
		}
		if dsn != want {
			t.Errorf("%s: dsn %q, want %q", uri, dsn, want)
		}
	}

	// An in-memory database has no path and must not gain file options.
	if _, dsn, err := spec.DSN("duckdb://"); err != nil || dsn != "" {
		t.Fatalf("memory dsn %q err %v, want empty", dsn, err)
	}
}

// TestDuckDBOpenRejectsMissingFile keeps a typo in the path from silently
// creating an empty database: DuckDB would happily do that, and the connection
// would look healthy while holding no data.
func TestDuckDBOpenRejectsMissingFile(t *testing.T) {
	spec := Get("duckdb")
	missing := filepath.Join(t.TempDir(), "nope.duckdb")
	_, _, err := spec.DSN("duckdb:///" + filepath.ToSlash(missing))
	if err != nil {
		t.Fatalf("DSN: %v", err)
	}
	if _, err := spec.Open(filepath.ToSlash(missing) + "?access_mode=read_only"); !errors.Is(err, ErrDBFileNotFound) {
		t.Fatalf("missing file: %v", err)
	}

	// A directory is a common mistake when someone points at the data folder.
	dir := t.TempDir()
	if _, err := spec.Open(filepath.ToSlash(dir)); !errors.Is(err, ErrDBFileNotFound) {
		t.Fatalf("directory: %v", err)
	}
	if _, statErr := os.Stat(missing); statErr == nil {
		t.Fatal("probing must not create the file")
	}
}

// TestDuckDBQueriesKeepCatalogFilter guards the duplicate-schema bug: DuckDB
// exposes a "main" schema in the system and temp catalogs as well, so a query
// without the catalog filter returned the same schema three times.
func TestDuckDBQueriesKeepCatalogFilter(t *testing.T) {
	spec := Get("duckdb")
	for name, query := range map[string]string{
		"schemas": spec.SchemasQuery(),
		"tables":  spec.TablesQuery(),
		"columns": spec.ColumnsQuery(),
	} {
		if !strings.Contains(strings.ToLower(query), "catalog") {
			t.Errorf("%s query must filter on the catalog: %s", name, query)
		}
	}
}
