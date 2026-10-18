package ingest

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Generic column types shared by the SQLite writer and the dataset metadata.
const (
	TypeInteger   = "INTEGER"
	TypeFloat     = "FLOAT"
	TypeBoolean   = "BOOLEAN"
	TypeTimestamp = "TIMESTAMP"
	TypeString    = "VARCHAR"
)

// value kinds used during inference.
const (
	kindInt = iota
	kindFloat
	kindBool
	kindTime
	kindString
)

// dateTimeLayouts are accepted when sniffing a timestamp column. The first
// layout that parses wins; a date-only value is stored at midnight.
var dateTimeLayouts = []string{
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z07:00",
	time.RFC3339Nano,
	"2006/01/02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02",
}

// InferType picks the narrowest generic type that fits every sampled value.
// isDttm reports whether the column should be flagged as a temporal column.
func InferType(values []any) (typ string, isDttm bool) {
	if len(values) > 200 {
		values = values[:200]
	}
	seen := 0
	allInt, allFloat, allBool, allTime := true, true, true, true
	for _, v := range values {
		kind, ok := classify(v)
		if !ok {
			continue
		}
		seen++
		if kind != kindInt {
			allInt = false
		}
		if kind != kindInt && kind != kindFloat {
			allFloat = false
		}
		if kind != kindBool {
			allBool = false
		}
		if kind != kindTime {
			allTime = false
		}
	}
	switch {
	case seen == 0:
		return TypeString, false
	case allInt:
		return TypeInteger, false
	case allFloat:
		return TypeFloat, false
	case allBool:
		return TypeBoolean, false
	case allTime:
		return TypeTimestamp, true
	default:
		return TypeString, false
	}
}

// classify maps a raw value to one of the inference kinds. The second result is
// false for values that carry no signal (nil, blank strings).
func classify(v any) (int, bool) {
	switch t := v.(type) {
	case nil:
		return kindString, false
	case bool:
		return kindBool, true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return kindInt, true
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) && math.Abs(t) < 1e15 {
			return kindInt, true
		}
		return kindFloat, true
	case float32:
		f := float64(t)
		if f == math.Trunc(f) && math.Abs(f) < 1e15 {
			return kindInt, true
		}
		return kindFloat, true
	case time.Time:
		return kindTime, true
	case json.Number:
		if _, err := t.Int64(); err == nil {
			return kindInt, true
		}
		if _, err := t.Float64(); err == nil {
			return kindFloat, true
		}
		return kindString, true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return kindString, false
		}
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return kindInt, true
		}
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return kindFloat, true
		}
		if _, err := toTime(s); err == nil {
			return kindTime, true
		}
		if isBoolString(s) {
			return kindBool, true
		}
		return kindString, true
	default:
		return kindString, true
	}
}

// isBoolString recognises the textual booleans produced by CSV/Excel exports.
func isBoolString(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "false", "yes", "no":
		return true
	}
	return false
}

// Coerce converts a raw value into the Go representation stored in SQLite for
// the given generic column type. Unparseable values become nil so a typed
// column never silently mixes strings with numbers.
func Coerce(typ string, v any) any {
	if v == nil {
		return nil
	}
	switch typ {
	case TypeInteger:
		if n, ok := toInt64(v); ok {
			return n
		}
		return nil
	case TypeFloat:
		if f, ok := toFloat64(v); ok {
			return f
		}
		return nil
	case TypeBoolean:
		if b, ok := toBool(v); ok {
			if b {
				return int64(1)
			}
			return int64(0)
		}
		return nil
	case TypeTimestamp:
		if ts, err := toTime(v); err == nil {
			return ts.Format("2006-01-02 15:04:05")
		}
		return nil
	default:
		return toString(v)
	}
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int8:
		return int64(t), true
	case int16:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case uint:
		return int64(t), true
	case uint8:
		return int64(t), true
	case uint16:
		return int64(t), true
	case uint32:
		return int64(t), true
	case uint64:
		if t > math.MaxInt64 {
			return 0, false
		}
		return int64(t), true
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return int64(t), true
		}
		return 0, false
	case float32:
		f := float64(t)
		if f == math.Trunc(f) && math.Abs(f) < 1e15 {
			return int64(f), true
		}
		return 0, false
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		s := strings.TrimSpace(t)
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && f == math.Trunc(f) {
			return int64(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func toFloat64(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		if n, ok := toInt64(v); ok {
			return float64(n), true
		}
		if s, ok := v.(string); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			return f, err == nil
		}
		return 0, false
	}
}

func toBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "yes":
			return true, true
		case "false", "no":
			return false, true
		}
		return false, false
	default:
		if n, ok := toInt64(v); ok {
			return n != 0, true
		}
		return false, false
	}
}

func toTime(v any) (time.Time, error) {
	if t, ok := v.(time.Time); ok {
		return t, nil
	}
	s, ok := v.(string)
	if !ok {
		s = toString(v)
	}
	s = strings.TrimSpace(s)
	for _, layout := range dateTimeLayouts {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as a timestamp", s)
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case []byte:
		return string(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprint(v)
	}
}
