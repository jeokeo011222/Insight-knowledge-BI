package api

import "testing"

// TestRowTextIsCaseInsensitive guards the metadata regression: MySQL, Oracle
// and DB2 report information_schema columns in upper case (SCHEMA_NAME), while
// every dialect spec reads them with lower-case keys. Without case-insensitive
// lookup the schema list came back empty and the dataset wizard could not pick
// a table at all.
func TestRowTextIsCaseInsensitive(t *testing.T) {
	cases := []struct {
		name string
		row  map[string]any
		key  string
		want string
	}{
		{"lowercase (postgres)", map[string]any{"table_name": "orders"}, "table_name", "orders"},
		{"uppercase (mysql)", map[string]any{"TABLE_NAME": "orders"}, "table_name", "orders"},
		{"mixed case (oracle)", map[string]any{"Table_Name": "orders"}, "table_name", "orders"},
		{"quoted key", map[string]any{`"table_name"`: "orders"}, "table_name", "orders"},
		{"null value", map[string]any{"table_name": nil}, "table_name", ""},
		{"missing column", map[string]any{"other": "x"}, "table_name", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rowText(tc.row, tc.key); got != tc.want {
				t.Fatalf("rowText(%v, %q) = %q, want %q", tc.row, tc.key, got, tc.want)
			}
		})
	}
}

// TestRowValueFallsBackToAlternateNames covers the alias chain used by the
// metadata endpoints (table_name / tablename / name).
func TestRowValueFallsBackToAlternateNames(t *testing.T) {
	row := map[string]any{"NAME": "orders"}
	if got := rowText(row, "table_name", "tablename", "name"); got != "orders" {
		t.Fatalf("got %q, want orders", got)
	}
}
