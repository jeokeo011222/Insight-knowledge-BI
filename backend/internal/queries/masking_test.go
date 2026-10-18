package queries

import (
	"testing"

	"superset/internal/models"
)

func TestEffectiveMaskRuleFoldsPIILevel(t *testing.T) {
	// An explicit mask type always wins.
	explicit := EffectiveMaskRule(models.TableColumn{MaskType: MaskPhone})
	if explicit.Type != MaskPhone {
		t.Fatalf("explicit mask lost: %+v", explicit)
	}
	// A high-PII column with no mask type is still masked (full mask): the field
	// is unsafe by nature, not by format.
	high := EffectiveMaskRule(models.TableColumn{PIILevel: PIILevelHigh})
	if high.Type != MaskFull {
		t.Fatalf("high PII must fall back to full mask, got %+v", high)
	}
	// Low PII without a mask type is not masked.
	if got := EffectiveMaskRule(models.TableColumn{PIILevel: PIILevelLow}); got.Type != "" {
		t.Fatalf("low PII without mask type should not mask, got %+v", got)
	}
	// High PII with an explicit type keeps the explicit rule.
	both := EffectiveMaskRule(models.TableColumn{PIILevel: PIILevelHigh, MaskType: MaskEmail})
	if both.Type != MaskEmail {
		t.Fatalf("explicit rule must win over the PII fallback, got %+v", both)
	}
}

func TestApplyMaskRulesRewritesOnlyConfiguredColumns(t *testing.T) {
	rows := []map[string]any{
		{"phone": "13812341234", "amount": 42, "name": "张三丰"},
		{"phone": nil, "amount": 7, "name": "李四"},
	}
	ApplyMaskRules(rows, map[string]MaskRule{
		"phone": {Type: MaskPhone},
		"name":  {Type: MaskName},
	})
	if rows[0]["phone"] != "138****1234" {
		t.Fatalf("phone not masked: %v", rows[0]["phone"])
	}
	if rows[0]["name"] != "张**" {
		t.Fatalf("name not masked: %v", rows[0]["name"])
	}
	if rows[0]["amount"] != 42 {
		t.Fatalf("unconfigured column changed: %v", rows[0]["amount"])
	}
	if rows[1]["phone"] != nil {
		t.Fatalf("nil must stay nil, got %v", rows[1]["phone"])
	}
}

func TestMaskValueHandlesNumericColumns(t *testing.T) {
	// A phone stored as bigint must be masked too, otherwise the protection
	// silently depends on the column type.
	got := MaskValue(int64(13812341234), MaskPhone, "")
	if got != "138****1234" {
		t.Fatalf("numeric phone = %v", got)
	}
}
