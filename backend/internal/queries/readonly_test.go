package queries

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestInjectPredicateAddsWhereToAggregate(t *testing.T) {
	stmt := "SELECT count(*) AS c FROM orders"
	got, ok := injectPredicate(stmt, []string{"tenant_id = 7"})
	if !ok {
		t.Fatal("injectPredicate refused a plain aggregate")
	}
	want := "SELECT count(*) AS c FROM orders WHERE (tenant_id = 7)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestInjectPredicateExtendsExistingWhere(t *testing.T) {
	stmt := "SELECT region, sum(amount) FROM orders WHERE status = 'paid' GROUP BY region ORDER BY region"
	got, ok := injectPredicate(stmt, []string{"tenant_id = 7"})
	if !ok {
		t.Fatal("injectPredicate refused a statement with WHERE")
	}
	if !strings.Contains(got, "status = 'paid' AND (tenant_id = 7)") {
		t.Fatalf("predicate not appended to WHERE: %q", got)
	}
	if strings.Index(got, "tenant_id") > strings.Index(got, "GROUP BY") {
		t.Fatalf("predicate landed after GROUP BY: %q", got)
	}
}

func TestInjectPredicateInsertsBeforeGroupBy(t *testing.T) {
	stmt := "SELECT region, sum(amount) FROM orders GROUP BY region"
	got, ok := injectPredicate(stmt, []string{"tenant_id = 7"})
	if !ok {
		t.Fatal("injectPredicate refused a grouped statement")
	}
	if !strings.Contains(got, "FROM orders WHERE (tenant_id = 7)\nGROUP BY") {
		t.Fatalf("predicate not inserted before GROUP BY: %q", got)
	}
}

func TestInjectPredicateIgnoresKeywordsInLiteralsAndComments(t *testing.T) {
	stmt := "SELECT 'where union limit' AS note, id FROM orders -- where 1=1\nORDER BY id"
	got, ok := injectPredicate(stmt, []string{"tenant_id = 7"})
	if !ok {
		t.Fatal("injectPredicate refused a statement whose literals contain keywords")
	}
	if !strings.Contains(got, "FROM orders -- where 1=1") {
		t.Fatalf("statement body was mangled: %q", got)
	}
	if strings.Index(got, "tenant_id") > strings.Index(got, "ORDER BY") {
		t.Fatalf("predicate landed after ORDER BY: %q", got)
	}
}

func TestInjectPredicateRefusesSetOperations(t *testing.T) {
	if _, ok := injectPredicate("SELECT 1 UNION SELECT 2", []string{"tenant_id = 7"}); ok {
		t.Fatal("injectPredicate should refuse top-level UNION")
	}
}

func TestInjectPredicateRefusesUnbalancedQuotes(t *testing.T) {
	if _, ok := injectPredicate("SELECT 'oops FROM orders", []string{"tenant_id = 7"}); ok {
		t.Fatal("injectPredicate should refuse unbalanced quotes")
	}
}

func TestWrapReadOnlyKeepsLimit(t *testing.T) {
	out := wrapReadOnly("SELECT count(*) FROM orders", []string{"tenant_id = 7"}, 100)
	if !strings.Contains(out, "WHERE (tenant_id = 7)") {
		t.Fatalf("RLS predicate missing: %q", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "LIMIT 100") {
		t.Fatalf("row limit missing: %q", out)
	}
}

func TestClassifyQueryError(t *testing.T) {
	timeout := 30 * time.Second

	colErr := classifyQueryError(errors.New(`ERROR: column "nope" does not exist (SQLSTATE 42703)`), timeout)
	if colErr.Status() != 400 {
		t.Fatalf("unknown column status = %d, want 400", colErr.Status())
	}
	if strings.Contains(colErr.Message, "SQLSTATE") || strings.Contains(colErr.Message, "nope") {
		t.Fatalf("unknown column leaked database detail: %q", colErr.Message)
	}

	connErr := classifyQueryError(errors.New("dial tcp 10.0.0.1:5432: connect: connection refused"), timeout)
	if connErr.Status() != 424 {
		t.Fatalf("connection status = %d, want 424", connErr.Status())
	}

	timedOut := classifyQueryError(context.DeadlineExceeded, timeout)
	if timedOut.Status() != 504 {
		t.Fatalf("timeout status = %d, want 504", timedOut.Status())
	}

	unknown := classifyQueryError(errors.New("syntax error at or near \"SELEC\""), timeout)
	if unknown.Status() != 500 {
		t.Fatalf("unknown error status = %d, want 500", unknown.Status())
	}
	if strings.Contains(unknown.Message, "SELEC") {
		t.Fatalf("unknown error leaked database detail: %q", unknown.Message)
	}
	if unknown.Message != "Query execution failed" {
		t.Fatalf("unexpected message %q", unknown.Message)
	}
}

// TestWorstSeqScanPicksDeepestLargeScan covers the EXPLAIN cost gate: the guard
// must look at every node, because the expensive scan is usually nested under a
// join or an aggregate rather than being the top node.
func TestWorstSeqScanPicksDeepestLargeScan(t *testing.T) {
	plan := planNode{
		NodeType: "Aggregate",
		PlanRows: 1,
		Plans: []planNode{
			{
				NodeType: "Hash Join",
				PlanRows: 100,
				Plans: []planNode{
					{NodeType: "Seq Scan", PlanRows: 25000000},
					{NodeType: "Index Scan", PlanRows: 90000000},
				},
			},
		},
	}
	// Index Scan is deliberately huge: only Seq Scan counts as a table sweep.
	if got := worstSeqScan(plan); got != 25000000 {
		t.Fatalf("worstSeqScan = %v, want 25000000", got)
	}

	// No sequential scan at all (index-only plan) must not trip the gate.
	if got := worstSeqScan(planNode{NodeType: "Result", Plans: []planNode{{NodeType: "Index Only Scan", PlanRows: 1e9}}}); got != 0 {
		t.Fatalf("worstSeqScan = %v, want 0 for an index-only plan", got)
	}
}
