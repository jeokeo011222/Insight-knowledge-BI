package datasets

import (
	"encoding/json"
	"testing"
	"time"

	"superset/internal/models"
)

// TestIsDue covers the boundary that decides whether the scheduler wakes a
// dataset up. Off-by-one here would either never refresh or refresh every scan.
func TestIsDue(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	tenMinutesAgo := now.Add(-10 * time.Minute)
	fiveMinutesAgo := now.Add(-5 * time.Minute)

	cases := []struct {
		name     string
		interval int
		last     *time.Time
		want     bool
	}{
		{name: "auto refresh off", interval: 0, last: &tenMinutesAgo, want: false},
		{name: "never refreshed", interval: 300, last: nil, want: true},
		{name: "interval elapsed", interval: 300, last: &tenMinutesAgo, want: true},
		{name: "interval not elapsed", interval: 900, last: &fiveMinutesAgo, want: false},
		{name: "exactly due", interval: 300, last: ptrTime(now.Add(-5 * time.Minute)), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataset := &models.SqlaTable{RefreshIntervalSec: tc.interval, LastRefreshedAt: tc.last}
			if got := isDue(dataset, now); got != tc.want {
				t.Fatalf("isDue = %v, want %v", got, tc.want)
			}
		})
	}
}

func ptrTime(v time.Time) *time.Time { return &v }

// TestSourceOfAndEncodeExtra keeps the JSON contract between the upload path and
// the refresher: extra may already carry unrelated keys, and they must survive.
func TestSourceOfAndEncodeExtra(t *testing.T) {
	dataset := &models.SqlaTable{
		Extra: `{"source":{"type":"file","filename":"orders.csv","source_path":"/tmp/a.csv"},"other":{"keep":true}}`,
	}
	src, ok := SourceOf(dataset)
	if !ok {
		t.Fatal("SourceOf returned false for a dataset with a source")
	}
	if src.Type != "file" || src.Filename != "orders.csv" || src.SourcePath != "/tmp/a.csv" {
		t.Fatalf("source = %+v", src)
	}

	encoded := EncodeExtra(dataset.Extra, &Source{Type: "api"})
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatalf("encoded extra is not valid JSON: %v", err)
	}
	if _, ok := payload["other"]; !ok {
		t.Fatalf("unrelated extra keys were dropped: %s", encoded)
	}
}

// TestSourceOfIgnoresMalformedExtra: a broken extra blob must not panic the
// scheduler, it just means "not an external dataset".
func TestSourceOfIgnoresMalformedExtra(t *testing.T) {
	for _, extra := range []string{"", "{}", "null", "not-json", `{"source":{}}`} {
		if _, ok := SourceOf(&models.SqlaTable{Extra: extra}); ok {
			t.Fatalf("extra %q unexpectedly reported a source", extra)
		}
	}
	if _, ok := SourceOf(nil); ok {
		t.Fatal("nil dataset unexpectedly reported a source")
	}
}

// TestIntervalBounds guards the documented limits used by the API validator.
func TestIntervalBounds(t *testing.T) {
	if MinIntervalSec != 60 {
		t.Fatalf("MinIntervalSec = %d", MinIntervalSec)
	}
	if MaxIntervalSec != 30*24*3600 {
		t.Fatalf("MaxIntervalSec = %d", MaxIntervalSec)
	}
	if MaxIntervalSec <= MinIntervalSec {
		t.Fatal("interval bounds are inverted")
	}
}

// TestHelpersMatchDialectExpectations pins the tiny SQL helpers the probe query
// relies on.
func TestHelpersMatchDialectExpectations(t *testing.T) {
	if got := qualified("", "orders"); got != "orders" {
		t.Fatalf("qualified without schema = %q", got)
	}
	if got := qualified("public", "orders"); got != "public.orders" {
		t.Fatalf("qualified = %q", got)
	}
	if got := escapeSQL("o'brien"); got != "o''brien" {
		t.Fatalf("escapeSQL = %q", got)
	}
	for _, dtype := range []string{"TIMESTAMP", "datetime", "date", "time without time zone"} {
		if !isTemporalType(dtype) {
			t.Fatalf("%q should be temporal", dtype)
		}
	}
	for _, dtype := range []string{"varchar", "int8", "numeric", ""} {
		if isTemporalType(dtype) {
			t.Fatalf("%q should not be temporal", dtype)
		}
	}
}
