package queries

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This file implements the subset of superset/utils/date_parser.py used by the
// SQL builder: human readable relative ranges ("Last week", "previous calendar
// month", "90 days ago", ...) plus ISO/RFC3339 fallbacks.

var (
	// "90 days ago" / "3 months ago"
	agoPattern = regexp.MustCompile(`^(-?\d+)\s+(second|minute|hour|day|week|month|quarter|year)s?\s+ago$`)
	// "next 30 days" / "last 7 days"
	nextPattern = regexp.MustCompile(`^next\s+(\d+)\s+(second|minute|hour|day|week|month|quarter|year)s?$`)
	lastPattern = regexp.MustCompile(`^last\s+(\d+)\s+(second|minute|hour|day|week|month|quarter|year)s?$`)
	// "start of month" / "end of quarter"
	boundPattern = regexp.MustCompile(`^(start|end)\s+of\s+(second|minute|hour|day|week|month|quarter|year)$`)
)

var humanLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006-01",
	"2006",
}

// ParseHumanDatetime resolves a superset time expression into a [start, end)
// interval. Unrecognised expressions fall back to ISO/RFC3339 parsing, in which
// case the returned interval is degenerate (start == end) because the value is
// a single instant that callers use as a boundary.
func ParseHumanDatetime(s string, tz *time.Location, now time.Time) (time.Time, time.Time, error) {
	if tz == nil {
		tz = time.UTC
	}
	if now.IsZero() {
		now = time.Now()
	}
	if strings.TrimSpace(s) == "" {
		return time.Time{}, time.Time{}, fmt.Errorf("empty time expression")
	}
	local := now.In(tz)
	norm := normalizeHuman(s)

	switch norm {
	case "now", "end of today":
		return local, local, nil
	case "today":
		start := startOf(local, "day")
		return start, shiftTime(start, "day", 1), nil
	case "yesterday":
		start := shiftTime(startOf(local, "day"), "day", -1)
		return start, shiftTime(start, "day", 1), nil
	case "last day", "last 24 hours":
		return shiftTime(local, "day", -1), local, nil
	case "last week":
		return shiftTime(local, "week", -1), local, nil
	case "last month":
		return shiftTime(local, "month", -1), local, nil
	case "last quarter":
		return shiftTime(local, "quarter", -1), local, nil
	case "last year":
		return shiftTime(local, "year", -1), local, nil
	case "previous calendar week":
		start := shiftTime(startOf(local, "week"), "week", -1)
		return start, shiftTime(start, "week", 1), nil
	case "previous calendar month":
		start := shiftTime(startOf(local, "month"), "month", -1)
		return start, shiftTime(start, "month", 1), nil
	case "previous calendar quarter":
		start := shiftTime(startOf(local, "quarter"), "quarter", -1)
		return start, shiftTime(start, "quarter", 1), nil
	case "previous calendar year":
		start := shiftTime(startOf(local, "year"), "year", -1)
		return start, shiftTime(start, "year", 1), nil
	case "next day":
		return local, shiftTime(local, "day", 1), nil
	case "next week":
		return local, shiftTime(local, "week", 1), nil
	case "next month":
		return local, shiftTime(local, "month", 1), nil
	case "next quarter":
		return local, shiftTime(local, "quarter", 1), nil
	case "next year":
		return local, shiftTime(local, "year", 1), nil
	}

	if m := nextPattern.FindStringSubmatch(norm); m != nil {
		n, _ := strconv.Atoi(m[1])
		return local, shiftTime(local, m[2], n), nil
	}
	if m := lastPattern.FindStringSubmatch(norm); m != nil {
		n, _ := strconv.Atoi(m[1])
		return shiftTime(local, m[2], -n), local, nil
	}
	if m := agoPattern.FindStringSubmatch(norm); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := m[2]
		start := shiftTime(local, unit, -n)
		return start, shiftTime(start, unit, 1), nil
	}
	if m := boundPattern.FindStringSubmatch(norm); m != nil {
		unit := m[2]
		if m[1] == "start" {
			start := startOf(local, unit)
			return start, shiftTime(start, unit, 1), nil
		}
		// "end of X" yields the exclusive end boundary.
		end := shiftTime(startOf(local, unit), unit, 1)
		return end, end, nil
	}

	for _, layout := range humanLayouts {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), tz); err == nil {
			return t, t, nil
		}
	}
	return time.Time{}, time.Time{}, fmt.Errorf("unable to parse time expression %q", s)
}

// ApplyTimeRange resolves an explicit from/to pair. Explicit bounds win; when
// both are empty the range is considered unbounded. The third return value
// reports whether the pair could be resolved.
func ApplyTimeRange(fromDttm, toDttm string) (time.Time, time.Time, bool) {
	now := time.Now().UTC()
	var start, end time.Time
	ok := false

	if strings.TrimSpace(fromDttm) != "" {
		s, _, err := ParseHumanDatetime(fromDttm, time.UTC, now)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		start, ok = s, true
	}
	if strings.TrimSpace(toDttm) != "" {
		_, e, err := ParseHumanDatetime(toDttm, time.UTC, now)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		end, ok = e, true
	}
	return start, end, ok
}

// ResolveInstant is a helper for extras.relative_start / relative_end, which are
// point-in-time expressions rather than ranges.
func ResolveInstant(expr string, now time.Time) (time.Time, bool) {
	start, _, err := ParseHumanDatetime(expr, time.UTC, now)
	if err != nil {
		return time.Time{}, false
	}
	return start, true
}

func normalizeHuman(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.Join(strings.Fields(s), " ")
}

// startOf truncates a timestamp to the beginning of the given unit. Weeks start
// on Monday to match pandas/ISO semantics.
func startOf(t time.Time, unit string) time.Time {
	y, m, d := t.Date()
	switch unit {
	case "second":
		return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, t.Location())
	case "minute":
		return time.Date(y, m, d, t.Hour(), t.Minute(), 0, 0, t.Location())
	case "hour":
		return time.Date(y, m, d, t.Hour(), 0, 0, 0, t.Location())
	case "day":
		return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	case "week":
		day := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
		offset := (int(day.Weekday()) + 6) % 7 // Monday == 0
		return day.AddDate(0, 0, -offset)
	case "month":
		return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
	case "quarter":
		qm := time.Month((int(m)-1)/3*3 + 1)
		return time.Date(y, qm, 1, 0, 0, 0, 0, t.Location())
	case "year":
		return time.Date(y, 1, 1, 0, 0, 0, 0, t.Location())
	}
	return t
}

// shiftTime adds n units to t. Calendar units use AddDate so that month and
// year arithmetic clamp like Python's relativedelta.
func shiftTime(t time.Time, unit string, n int) time.Time {
	switch unit {
	case "second":
		return t.Add(time.Duration(n) * time.Second)
	case "minute":
		return t.Add(time.Duration(n) * time.Minute)
	case "hour":
		return t.Add(time.Duration(n) * time.Hour)
	case "day":
		return t.AddDate(0, 0, n)
	case "week":
		return t.AddDate(0, 0, 7*n)
	case "month":
		return t.AddDate(0, n, 0)
	case "quarter":
		return t.AddDate(0, 3*n, 0)
	case "year":
		return t.AddDate(n, 0, 0)
	}
	return t
}
