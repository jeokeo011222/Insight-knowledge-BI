package connectors

import (
	"fmt"
	"strings"
)

// TimeGrain is a canonical Superset time grain.
type TimeGrain struct {
	// ID is the ISO-8601 duration identifier used by the API (PT1H, P1D, ...).
	ID string
	// Label is the human readable label.
	Label string
	// DateTrunc is the DATE_TRUNC unit name.
	DateTrunc string
	// Duration approximates the grain in seconds (0 for calendar grains).
	Duration int64
}

// TimeGrains is the full grain catalogue, mirroring
// superset/db_engine_specs/lib.py:TimeGrain.
var TimeGrains = []TimeGrain{
	{ID: "PT1S", Label: "second", DateTrunc: "second", Duration: 1},
	{ID: "PT1M", Label: "minute", DateTrunc: "minute", Duration: 60},
	{ID: "PT5M", Label: "5 minute", DateTrunc: "minute", Duration: 300},
	{ID: "PT10M", Label: "10 minute", DateTrunc: "minute", Duration: 600},
	{ID: "PT15M", Label: "15 minute", DateTrunc: "minute", Duration: 900},
	{ID: "PT30M", Label: "30 minute", DateTrunc: "minute", Duration: 1800},
	{ID: "PT1H", Label: "hour", DateTrunc: "hour", Duration: 3600},
	{ID: "PT6H", Label: "6 hour", DateTrunc: "hour", Duration: 21600},
	{ID: "P1D", Label: "day", DateTrunc: "day", Duration: 86400},
	{ID: "P1W", Label: "week", DateTrunc: "week", Duration: 604800},
	{ID: "P1M", Label: "month", DateTrunc: "month"},
	{ID: "P3M", Label: "quarter", DateTrunc: "quarter"},
	{ID: "P1Y", Label: "year", DateTrunc: "year"},
}

// legacyGrains maps SQL Lab / form_data grain aliases to their ISO ids.
var legacyGrains = map[string]string{
	"second": "PT1S", "minute": "PT1M", "5 minute": "PT5M", "10 minute": "PT10M",
	"15 minute": "PT15M", "30 minute": "PT30M", "hour": "PT1H", "6 hour": "PT6H",
	"day": "P1D", "week": "P1W", "month": "P1M", "quarter": "P3M", "year": "P1Y",
	"p1q": "P3M", "pt1q": "P3M",
	"Time Column": "", "": "",
}

// NormalizeGrain converts a legacy or ISO grain into its ISO id.
func NormalizeGrain(grain string) (string, error) {
	if grain == "" {
		return "", nil
	}
	if id, ok := legacyGrains[strings.ToLower(grain)]; ok {
		return id, nil
	}
	for _, g := range TimeGrains {
		if strings.EqualFold(g.ID, grain) {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("unsupported time grain %q", grain)
}

// GrainByID returns the grain descriptor for an ISO id.
func GrainByID(id string) (TimeGrain, bool) {
	norm, err := NormalizeGrain(id)
	if err != nil || norm == "" {
		return TimeGrain{}, false
	}
	for _, g := range TimeGrains {
		if g.ID == norm {
			return g, true
		}
	}
	return TimeGrain{}, false
}

// GrainToDateTrunc maps a grain id to a DATE_TRUNC unit.
func GrainToDateTrunc(grain string) (string, error) {
	g, ok := GrainByID(grain)
	if !ok {
		return "", fmt.Errorf("unsupported time grain %q", grain)
	}
	return g.DateTrunc, nil
}

// GrainLabel returns the display label for a grain id.
func GrainLabel(grain string) string {
	if g, ok := GrainByID(grain); ok {
		return g.Label
	}
	return grain
}
