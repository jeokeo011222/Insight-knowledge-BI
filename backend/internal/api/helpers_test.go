package api

import "testing"

// TestQuoteFilterColumn covers the SQL-injection guard on `filters[].col`.
func TestQuoteFilterColumn(t *testing.T) {
	valid := map[string]string{
		"id":          `"id"`,
		"theme_name":  `"theme_name"`,
		"Changed_On2": `"Changed_On2"`,
		"_private":    `"_private"`,
	}
	for in, want := range valid {
		got, ok := quoteFilterColumn(in)
		if !ok || got != want {
			t.Fatalf("quoteFilterColumn(%q) = (%q, %v), want (%q, true)", in, got, ok, want)
		}
	}

	// Payloads that previously reached the WHERE clause verbatim.
	invalid := []string{
		"(SELECT pg_sleep(1) IS NULL)",
		"1=1 OR 1=1",
		`"id" = "id"`,
		"id; DROP TABLE ab_user",
		"id--",
		"id) OR (1=1",
		"",
		"users.id",
		"id`; SELECT 1",
	}
	for _, in := range invalid {
		if got, ok := quoteFilterColumn(in); ok {
			t.Fatalf("quoteFilterColumn(%q) accepted with %q, want rejection", in, got)
		}
	}
}
