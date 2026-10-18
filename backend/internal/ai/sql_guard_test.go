package ai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// guardFixture is a single-table dataset with one hidden column.
func guardFixture(statement string) error {
	return GuardSQL(SQLGuardInput{
		Statement:        statement,
		AllowedTables:    []string{"orders", "public.orders"},
		HiddenColumns:    []string{"id_card"},
		AvailableColumns: []string{"order_id", "amount", "created_at"},
		ReviewTables:     []string{"orders"},
	})
}

func TestGuardSQLRejectsOutOfScopeTables(t *testing.T) {
	cases := []string{
		`SELECT * FROM customers`,
		`SELECT * FROM public.customers`,
		`SELECT * FROM orders o JOIN customers c ON o.id = c.id`,
		// The inner query's table must be checked too.
		`SELECT * FROM (SELECT * FROM customers) x`,
		`WITH x AS (SELECT * FROM customers) SELECT * FROM x`,
		// Comma list in FROM.
		`SELECT * FROM orders o, customers c WHERE o.id = c.id`,
	}
	for _, sql := range cases {
		err := guardFixture(sql)
		var gerr *SQLGuardError
		if !errors.As(err, &gerr) || gerr.Code != GuardTableNotAllowed {
			t.Fatalf("%s: want TABLE_NOT_ALLOWED, got %v", sql, err)
		}
	}
}

func TestGuardSQLAllowsDatasetAndCTEs(t *testing.T) {
	cases := []string{
		`SELECT * FROM orders`,
		`SELECT * FROM "orders"`,
		`SELECT o.amount FROM public.orders o WHERE o.amount > 0`,
		`WITH recent AS (SELECT * FROM orders WHERE created_at > '2026-01-01') SELECT count(*) FROM recent`,
		`WITH a AS (SELECT * FROM orders), b AS (SELECT * FROM a) SELECT * FROM b`,
		`SELECT * FROM (SELECT * FROM orders) x ORDER BY x.amount LIMIT 10`,
		// A string literal that mentions a table is not a table reference.
		`SELECT 'from customers' AS label FROM orders`,
		// Comments are ignored.
		"SELECT amount -- from customers\nFROM orders",
		// FROM inside a function call is not a table introduction.
		`SELECT EXTRACT(YEAR FROM created_at) AS y, COUNT(*) FROM orders GROUP BY 1`,
		`SELECT SUBSTRING(name FROM 1 FOR 2) FROM orders`,
		`SELECT TRIM(BOTH ' ' FROM name) FROM orders`,
		`SELECT * FROM orders WHERE amount IN (SELECT amount FROM orders)`,
	}
	for _, sql := range cases {
		if err := guardFixture(sql); err != nil {
			t.Fatalf("%s: unexpected rejection: %v", sql, err)
		}
	}
}

func TestGuardSQLRejectsDangerousFunctions(t *testing.T) {
	cases := []struct {
		sql  string
		code string
	}{
		{`SELECT pg_read_file('/etc/passwd')`, GuardFunctionBlocked},
		{`SELECT pg_catalog.pg_read_file('/etc/passwd')`, GuardFunctionBlocked},
		{`SELECT * FROM orders WHERE amount > (SELECT 1 FROM dblink('x','DELETE FROM t'))`, GuardSourceNotAllowed},
		{`SELECT setval('s', 1)`, GuardFunctionBlocked},
		{`SELECT * FROM dblink('host=1','select 1') AS t(a int)`, GuardSourceNotAllowed},
		{`SELECT * FROM read_csv('/tmp/secret.csv')`, GuardSourceNotAllowed},
		{`SELECT * FROM generate_series(1, 1000000)`, GuardSourceNotAllowed},
	}
	for _, tc := range cases {
		err := guardFixture(tc.sql)
		var gerr *SQLGuardError
		if !errors.As(err, &gerr) || gerr.Code != tc.code {
			t.Fatalf("%s: want %s, got %v", tc.sql, tc.code, err)
		}
	}
}

func TestGuardSQLRejectsLockingClauses(t *testing.T) {
	for _, sql := range []string{
		`SELECT * FROM orders FOR UPDATE`,
		`SELECT * FROM orders WHERE amount > 1 FOR SHARE`,
	} {
		err := guardFixture(sql)
		var gerr *SQLGuardError
		if !errors.As(err, &gerr) || gerr.Code != GuardNotReadOnly {
			t.Fatalf("%s: want NOT_READ_ONLY, got %v", sql, err)
		}
	}
}

func TestGuardSQLRejectsHiddenColumns(t *testing.T) {
	err := guardFixture(`SELECT id_card FROM orders`)
	var gerr *SQLGuardError
	if !errors.As(err, &gerr) || gerr.Code != GuardColumnHidden {
		t.Fatalf("want COLUMN_HIDDEN, got %v", err)
	}
	if len(gerr.AvailableColumns) == 0 {
		t.Fatal("rejection must list the columns that are available")
	}
}

func TestGuardSQLRejectsUnbalancedSQL(t *testing.T) {
	err := guardFixture(`SELECT 'unterminated FROM orders`)
	var gerr *SQLGuardError
	if !errors.As(err, &gerr) || gerr.Code != GuardUnbalanced {
		t.Fatalf("want UNBALANCED_SQL, got %v", err)
	}
}

// TestGuardErrorIsMachineReadable pins the contract the model relies on: the
// rejection text is a JSON object with a code and a hint.
func TestGuardErrorIsMachineReadable(t *testing.T) {
	err := guardFixture(`SELECT * FROM customers`)
	payload := map[string]any{}
	if uerr := json.Unmarshal([]byte(err.Error()), &payload); uerr != nil {
		t.Fatalf("error is not JSON: %v (%s)", uerr, err.Error())
	}
	if payload["code"] != GuardTableNotAllowed {
		t.Fatalf("code = %v", payload["code"])
	}
	if payload["hint"] == "" {
		t.Fatal("hint must be present")
	}
	if !strings.Contains(err.Error(), "available_tables") {
		t.Fatal("rejection must name the allowed tables")
	}
}

// TestGuardAllowsVirtualDataset ensures the whitelist is skipped (deliberately)
// for a virtual dataset, whose physical table is unknown.
func TestGuardAllowsVirtualDataset(t *testing.T) {
	if err := GuardSQL(SQLGuardInput{Statement: `SELECT * FROM anywhere`}); err != nil {
		t.Fatalf("virtual dataset should not be table-restricted: %v", err)
	}
}

func TestTokenizeHandlesQuoting(t *testing.T) {
	tokens, ok := tokenizeSQL("SELECT \"weird name\", `back`, [brack] FROM t -- note\n")
	if !ok {
		t.Fatal("tokenize failed")
	}
	var idents []string
	for _, tok := range tokens {
		if tok.ident {
			idents = append(idents, tok.text)
		}
	}
	joined := strings.Join(idents, "|")
	for _, want := range []string{"weird name", "back", "brack", "t"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %q", want, joined)
		}
	}
}
