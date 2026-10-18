package queries

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"gorm.io/gorm"

	"superset/internal/models"
)

// Column-level masking (列级脱敏).
//
// A dataset column can carry a mask type; any result that leaves the server for
// a caller who is not an admin is rewritten here. The logic lives in the query
// package (not the HTTP layer) because two very different callers must share
// exactly one implementation:
//
//   - the chart-data path (api/charts.go), which masks the result of a saved
//     chart;
//   - the Agent path (ExecuteReadOnlySQL), which used to bypass masking
//     entirely: a question answered by the Agent returned the raw phone number
//     that the same chart showed as 138****0000, and the raw value was then
//     written to ai_tool_call_log.result.
//
// Masking happens on the result, not in the SQL, for two reasons:
//   - a masked column can still be grouped/filtered correctly, because the
//     database sees the real value;
//   - the mask survives result caching, since the cache stores raw rows and the
//     rewrite runs per request.

// Supported mask types.
const (
	MaskPhone  = "phone"
	MaskIDCard = "idcard"
	MaskEmail  = "email"
	MaskName   = "name"
	MaskFull   = "full"
	MaskCustom = "custom"
)

// MaskTypes is the list accepted by the column editor.
func MaskTypes() []string {
	return []string{"", MaskPhone, MaskIDCard, MaskEmail, MaskName, MaskFull, MaskCustom}
}

// PII levels: a coarse classification that is independent of the mask shape.
// "high" columns are always masked on the Agent path even when no explicit mask
// type is configured (they fall back to a full mask), because a field like an
// ID-card number is unsafe by nature rather than by format.
const (
	PIILevelNone = "none"
	PIILevelLow  = "low"
	PIILevelHigh = "high"
)

// MaskRule is the resolved masking applied to one result column.
type MaskRule struct {
	Type string
	Rule string
}

// EffectiveMaskRule resolves a column's rule, folding the PII level in: an
// explicit mask type always wins, otherwise a high-PII column is fully masked.
// It returns the zero rule when the column is not sensitive at all.
func EffectiveMaskRule(column models.TableColumn) MaskRule {
	if column.MaskType != "" {
		return MaskRule{Type: column.MaskType, Rule: column.MaskRule}
	}
	if strings.EqualFold(column.PIILevel, PIILevelHigh) {
		return MaskRule{Type: MaskFull}
	}
	return MaskRule{}
}

// maskRuleCache avoids recompiling the same custom regexp for every row.
var maskRuleCache sync.Map

func maskRegexp(rule string) *regexp.Regexp {
	if cached, ok := maskRuleCache.Load(rule); ok {
		re, _ := cached.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile(rule)
	if err != nil {
		re = nil
	}
	maskRuleCache.Store(rule, re)
	return re
}

// MaskString applies one mask rule to a textual value.
func MaskString(value, maskType, rule string) string {
	runes := []rune(value)
	switch maskType {
	case MaskPhone:
		// 138****1234 — keep the carrier prefix and the last four digits.
		if len(runes) < 7 {
			return strings.Repeat("*", len(runes))
		}
		return string(runes[:3]) + "****" + string(runes[len(runes)-4:])
	case MaskIDCard:
		if len(runes) < 8 {
			return strings.Repeat("*", len(runes))
		}
		return string(runes[:4]) + strings.Repeat("*", len(runes)-8) + string(runes[len(runes)-4:])
	case MaskEmail:
		at := strings.Index(value, "@")
		if at <= 0 {
			return strings.Repeat("*", len(runes))
		}
		local := []rune(value[:at])
		return string(local[:1]) + "***" + value[at:]
	case MaskName:
		if len(runes) <= 1 {
			return strings.Repeat("*", len(runes))
		}
		return string(runes[:1]) + strings.Repeat("*", len(runes)-1)
	case MaskFull:
		return "***"
	case MaskCustom:
		re := maskRegexp(rule)
		if re == nil {
			return "***"
		}
		return re.ReplaceAllStringFunc(value, func(match string) string {
			return strings.Repeat("*", utf8.RuneCountInString(match))
		})
	default:
		return value
	}
}

// MaskValue converts non-string values so a numeric phone column is masked too.
func MaskValue(raw any, maskType, rule string) any {
	if raw == nil {
		return nil
	}
	text, ok := scalarText(raw)
	if !ok {
		// Objects and arrays keep their shape; masking them would be lossy.
		return raw
	}
	return MaskString(text, maskType, rule)
}

// scalarText renders the scalar types a driver can return.
func scalarText(raw any) (string, bool) {
	switch value := raw.(type) {
	case string:
		return value, true
	case []byte:
		return strings.TrimRight(string(value), "\x00"), true
	case bool:
		return strconv.FormatBool(value), true
	case int:
		return strconv.Itoa(value), true
	case int32:
		return strconv.FormatInt(int64(value), 10), true
	case int64:
		return strconv.FormatInt(value, 10), true
	case float32:
		return strconv.FormatFloat(float64(value), 'f', -1, 32), true
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64), true
	default:
		return "", false
	}
}

// LoadMaskRules reads the masking configuration of a dataset. A nil map means
// "nothing to mask"; a database error is returned so callers can decide whether
// to fail closed (the Agent path does) or open (the chart path never masks
// rather than blocking a dashboard on a metadata hiccup).
func LoadMaskRules(ctx context.Context, db *gorm.DB, datasetID int) (map[string]MaskRule, error) {
	if db == nil || datasetID <= 0 {
		return nil, nil
	}
	var columns []models.TableColumn
	if err := db.WithContext(ctx).
		Where("table_id = ? AND (mask_type <> '' OR pii_level = ?)", datasetID, PIILevelHigh).
		Find(&columns).Error; err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, nil
	}
	out := make(map[string]MaskRule, len(columns))
	for _, column := range columns {
		if rule := EffectiveMaskRule(column); rule.Type != "" {
			out[column.ColumnName] = rule
		}
	}
	return out, nil
}

// ApplyMaskRules rewrites every row in place.
func ApplyMaskRules(rows []map[string]any, rules map[string]MaskRule) {
	if len(rules) == 0 {
		return
	}
	for i := range rows {
		for column, rule := range rules {
			raw, ok := rows[i][column]
			if !ok {
				continue
			}
			rows[i][column] = MaskValue(raw, rule.Type, rule.Rule)
		}
	}
}
