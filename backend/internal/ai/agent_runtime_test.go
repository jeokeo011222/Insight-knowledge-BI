package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestObservationTextCapsRows keeps one large query from blowing the model
// context window while preserving the true row count and both ends of the data.
func TestObservationTextCapsRows(t *testing.T) {
	rows := make([]map[string]any, 500)
	for i := range rows {
		rows[i] = map[string]any{"id": i, "name": strings.Repeat("x", 40)}
	}
	out := map[string]any{
		"rows": rows, "rowcount": len(rows),
		"colnames": []string{"id", "name"}, "sql": "SELECT 1",
	}

	lim := obsLimits{rows: 100, bytes: obsMinBytes}
	text := observationText(out, nil, "", lim)
	if len(text) > lim.bytes {
		t.Fatalf("observation text is %d bytes, want <= %d", len(text), lim.bytes)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("observation text is not valid JSON: %v", err)
	}
	gotRows, ok := decoded["rows"].([]any)
	if !ok {
		t.Fatalf("rows missing from observation text: %s", text)
	}
	if len(gotRows) > lim.rows {
		t.Fatalf("rows kept = %d, want <= %d", len(gotRows), lim.rows)
	}
	// Head-only truncation would drop the latest periods, so the tail must
	// survive: the last kept row is the final input row.
	last, ok := gotRows[len(gotRows)-1].(map[string]any)
	if !ok || int(last["id"].(float64)) != len(rows)-1 {
		t.Fatalf("tail row lost: last kept = %v, want id %d", gotRows[len(gotRows)-1], len(rows)-1)
	}
	// The full row count must survive so answers can still report "共 N 行".
	if rc, ok := decoded["rowcount"].(float64); !ok || int(rc) != len(rows) {
		t.Fatalf("rowcount = %v, want %d", decoded["rowcount"], len(rows))
	}
}

// TestObsLimitsForScalesWithBudget pins the intent of the adaptive cap: a large
// statement passes through in full while the run still has room, and the
// allowance collapses to the sample floor only when the budget is spent.
func TestObsLimitsForScalesWithBudget(t *testing.T) {
	roomy := obsLimitsFor(120000)
	if roomy.rows <= obsMinRows {
		t.Fatalf("roomy rows = %d, want > floor %d", roomy.rows, obsMinRows)
	}
	if roomy.rows > obsMaxRows || roomy.bytes > obsMaxBytes {
		t.Fatalf("roomy limits exceeded ceilings: %+v", roomy)
	}
	tight := obsLimitsFor(0)
	if tight.rows != obsMinRows || tight.bytes != obsMinBytes {
		t.Fatalf("spent budget limits = %+v, want floor %d/%d", tight, obsMinRows, obsMinBytes)
	}
}

// TestObservationTextKeepsSmallResults makes sure the common path is untouched.
func TestObservationTextKeepsSmallResults(t *testing.T) {
	out := map[string]any{
		"rows":     []map[string]any{{"g": "girl", "c": 10}, {"g": "boy", "c": 12}},
		"rowcount": 2, "colnames": []string{"g", "c"},
	}
	text := observationText(out, nil, "", obsLimitsFor(obsMinRows*obsTokensPerRow))
	if strings.Contains(text, "_truncated") || strings.Contains(text, "_rows_omitted") {
		t.Fatalf("small result was truncated: %s", text)
	}
}
