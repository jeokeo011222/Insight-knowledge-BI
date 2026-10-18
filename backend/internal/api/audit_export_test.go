package api

import (
	"testing"
	"time"
)

func TestAuditCSVTimeIsUTCAndParseable(t *testing.T) {
	local := time.Date(2026, 3, 4, 12, 30, 0, 0, time.FixedZone("CST", 8*3600))
	got := auditCSVTime(&local)
	if got != "2026-03-04T04:30:00Z" {
		t.Fatalf("auditCSVTime = %q, want the UTC instant", got)
	}
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Fatalf("exported timestamp does not parse back: %v", err)
	}
	if auditCSVTime(nil) != "" {
		t.Error("nil timestamp must render as an empty cell")
	}
}

func TestAuditCSVIntRendersEmptyForNull(t *testing.T) {
	seven := 7
	if got := auditCSVInt(&seven); got != "7" {
		t.Fatalf("auditCSVInt(7) = %q", got)
	}
	// A NULL user_id / dashboard_id must stay an empty cell, not "0": the
	// auditor has to be able to tell "no target" from "target id 0".
	if got := auditCSVInt(nil); got != "" {
		t.Fatalf("auditCSVInt(nil) = %q, want empty", got)
	}
}

func TestAuditCSVHeaderCoversEveryColumn(t *testing.T) {
	// The row builder in exportLogs writes fields positionally, so a header
	// edit without a matching record edit would shift every column.
	want := []string{
		"id", "dttm", "action", "user_id", "dashboard_id", "slice_id",
		"duration_ms", "referrer", "json",
	}
	if len(auditCSVHeader) != len(want) {
		t.Fatalf("header has %d columns, record writes %d", len(auditCSVHeader), len(want))
	}
	for i := range want {
		if auditCSVHeader[i] != want[i] {
			t.Fatalf("column %d = %q, want %q", i, auditCSVHeader[i], want[i])
		}
	}
}
