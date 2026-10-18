// Package reports implements alerting and scheduled report delivery: the
// observer that runs alert SQL, the validators that decide whether an alert
// fires, the executor shared by the API and the scheduler, and a cron based
// scheduler.
package reports

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Supported validator types (superset alerts.types.ValidatorType).
const (
	ValidatorNotNull  = "not_null"
	ValidatorOperator = "operator"
)

// Validate applies a validator to the first row/first column value. It returns
// true when the alert condition is met, mirroring Superset semantics.
func Validate(validatorType string, config map[string]any, value any) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(validatorType)) {
	case ValidatorNotNull:
		return notNull(value), nil
	case ValidatorOperator:
		return operator(config, value)
	case "":
		return false, fmt.Errorf("validator type is required")
	default:
		return false, fmt.Errorf("unknown validator type %q", validatorType)
	}
}

// ParseValidatorConfig decodes a validator_config_json column.
func ParseValidatorConfig(raw []byte) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// notNull mirrors Superset's NotNullValidator: a value that is neither nil nor
// the empty string triggers.
func notNull(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case []byte:
		return strings.TrimSpace(string(t)) != ""
	}
	return true
}

// operator compares the value with the configured threshold.
func operator(config map[string]any, value any) (bool, error) {
	op := normalizeOperator(firstString(config, "op", "operator"))
	if op == "" {
		return false, fmt.Errorf("operator validator requires an op")
	}
	thresholdRaw, ok := config["threshold"]
	if !ok {
		return false, fmt.Errorf("operator validator requires a threshold")
	}
	threshold, ok := toFloat(thresholdRaw)
	if !ok {
		return false, fmt.Errorf("operator validator threshold %v is not numeric", thresholdRaw)
	}
	actual, ok := toFloat(value)
	if !ok {
		return false, fmt.Errorf("value %v is not numeric", value)
	}

	// Superset uses a tolerant comparison so that 1.0000000001 > 1 behaves as
	// expected for floating point data.
	const epsilon = 1e-9
	switch op {
	case "greater than":
		return actual-threshold > epsilon, nil
	case "less than":
		return threshold-actual > epsilon, nil
	case "equal to":
		return abs(actual-threshold) <= epsilon, nil
	case "not equal to":
		return abs(actual-threshold) > epsilon, nil
	case "greater or equal to":
		return actual-threshold > -epsilon, nil
	case "less or equal to":
		return threshold-actual > -epsilon, nil
	}
	return false, fmt.Errorf("unknown operator %q", op)
}

// normalizeOperator maps every accepted spelling onto a canonical operator.
func normalizeOperator(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "greater than", ">", "gt":
		return "greater than"
	case "less than", "<", "lt":
		return "less than"
	case "equal to", "==", "=", "eq":
		return "equal to"
	case "not equal to", "!=", "<>", "ne":
		return "not equal to"
	case "greater or equal to", ">=", "gte", "ge":
		return "greater or equal to"
	case "less or equal to", "<=", "lte", "le":
		return "less or equal to"
	}
	return ""
}

// firstString returns the first non-empty string among keys.
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// toFloat coerces driver values into float64.
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int8:
		return float64(t), true
	case int16:
		return float64(t), true
	case int32:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint:
		return float64(t), true
	case uint8:
		return float64(t), true
	case uint16:
		return float64(t), true
	case uint32:
		return float64(t), true
	case uint64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case []byte:
		return parseNumeric(string(t))
	case string:
		return parseNumeric(t)
	}
	return 0, false
}

// parseNumeric parses a string that may carry thousands separators.
func parseNumeric(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	cleaned := strings.NewReplacer(",", "", " ", "", "_", "").Replace(s)
	if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
		return f, true
	}
	return 0, false
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
