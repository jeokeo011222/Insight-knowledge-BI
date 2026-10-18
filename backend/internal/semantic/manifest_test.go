package semantic

import (
	"testing"

	"gorm.io/datatypes"

	"superset/internal/models"
)

func baseModel() models.SemanticModel {
	return models.SemanticModel{Name: "orders", DatasetID: 1, PrimaryKey: "id", Version: 1, Status: "draft"}
}

func field(name, kind, expr string) models.SemanticField {
	return models.SemanticField{Name: name, Kind: kind, Expression: expr}
}

// "关系引用不存在的列/模型" is the W-05 acceptance criterion: a model that
// points at a model that does not exist must not publish.
func TestCompileRejectsDanglingRelationship(t *testing.T) {
	spec, issues := Compile(CompileInput{
		Model:  baseModel(),
		Fields: []models.SemanticField{field("id", models.FieldKindColumn, "")},
		Relationships: []models.SemanticRelationship{
			{Name: "to_customers", FromModelID: 7, ToModelID: 9, JoinType: models.JoinManyToOne, Condition: "orders.customer_id = customers.id"},
		},
		ModelNames: map[int]string{7: "orders"},
	})
	if !HasErrors(issues) {
		t.Fatalf("expected a blocking error for a dangling relationship, got %+v", issues)
	}
	var found bool
	for _, i := range issues {
		if i.Code == IssueDanglingJoin {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected %s, got %+v", IssueDanglingJoin, issues)
	}
	if spec == nil {
		t.Fatal("a partial manifest must still be returned so the UI can show what is wrong")
	}
}

func TestCompileRejectsDuplicateFields(t *testing.T) {
	_, issues := Compile(CompileInput{
		Model: baseModel(),
		Fields: []models.SemanticField{
			field("amount", models.FieldKindColumn, ""),
			field("amount", models.FieldKindMetric, "SUM(amount)"),
		},
	})
	if !hasCode(issues, IssueDuplicateField) {
		t.Fatalf("expected %s, got %+v", IssueDuplicateField, issues)
	}
}

func TestCompileRequiresExpressionForDerivedFields(t *testing.T) {
	_, issues := Compile(CompileInput{
		Model:  baseModel(),
		Fields: []models.SemanticField{field("net_revenue", models.FieldKindMetric, "")},
	})
	if !hasCode(issues, IssueMissingExpression) {
		t.Fatalf("expected %s, got %+v", IssueMissingExpression, issues)
	}
}

// A column field with no expression is a passthrough, so it compiles to its own
// name — that is the whole point of the convention and must not be an error.
func TestCompileColumnFieldDefaultsToItsOwnName(t *testing.T) {
	spec, issues := Compile(CompileInput{
		Model: baseModel(),
		Fields: []models.SemanticField{
			field("id", models.FieldKindColumn, ""),
			field("amount", models.FieldKindColumn, ""),
		},
	})
	if HasErrors(issues) {
		t.Fatalf("unexpected errors: %+v", issues)
	}
	for _, f := range spec.Fields {
		if f.Name == "amount" && f.Expression != "amount" {
			t.Fatalf("passthrough expression = %q, want amount", f.Expression)
		}
	}
}

func TestCompileRejectsUnknownPrimaryKey(t *testing.T) {
	m := baseModel()
	m.PrimaryKey = "order_id"
	_, issues := Compile(CompileInput{
		Model:  m,
		Fields: []models.SemanticField{field("id", models.FieldKindColumn, "")},
	})
	if !hasCode(issues, IssueUnknownPrimaryKey) {
		t.Fatalf("expected %s, got %+v", IssueUnknownPrimaryKey, issues)
	}
}

func TestCompileManyToManyNeedsPrimaryKey(t *testing.T) {
	m := baseModel()
	m.ID = 1
	m.PrimaryKey = ""
	_, issues := Compile(CompileInput{
		Model:  m,
		Fields: []models.SemanticField{field("id", models.FieldKindColumn, "")},
		Relationships: []models.SemanticRelationship{
			{Name: "m2m", FromModelID: 1, ToModelID: 2, JoinType: models.JoinManyToMany, Condition: "a.id = b.a_id"},
		},
		ModelNames: map[int]string{1: "orders", 2: "tags"},
	})
	if !hasCode(issues, IssueManyToManyNoPK) {
		t.Fatalf("expected %s, got %+v", IssueManyToManyNoPK, issues)
	}
}

func TestCompileRejectsColumnMissingFromDataset(t *testing.T) {
	_, issues := Compile(CompileInput{
		Model:           baseModel(),
		Fields:          []models.SemanticField{field("nope", models.FieldKindColumn, "")},
		PhysicalColumns: map[string]bool{"id": true, "amount": true},
	})
	if !hasCode(issues, IssueUnknownColumn) {
		t.Fatalf("expected %s, got %+v", IssueUnknownColumn, issues)
	}
}

// The manifest hash is the "did the Agent's vocabulary change" signal: it must
// not depend on the order fields happen to be stored in.
func TestManifestHashIsOrderInsensitive(t *testing.T) {
	a, _ := Compile(CompileInput{
		Model: baseModel(),
		Fields: []models.SemanticField{
			field("id", models.FieldKindColumn, ""),
			field("amount", models.FieldKindColumn, ""),
		},
	})
	b, _ := Compile(CompileInput{
		Model: baseModel(),
		Fields: []models.SemanticField{
			field("amount", models.FieldKindColumn, ""),
			field("id", models.FieldKindColumn, ""),
		},
	})
	if a.ManifestSHA == "" || a.ManifestSHA != b.ManifestSHA {
		t.Fatalf("hash depends on field order: %q vs %q", a.ManifestSHA, b.ManifestSHA)
	}
}

func TestManifestHashChangesWithSemantics(t *testing.T) {
	a, _ := Compile(CompileInput{Model: baseModel(), Fields: []models.SemanticField{field("id", models.FieldKindColumn, "")}})
	b, _ := Compile(CompileInput{Model: baseModel(), Fields: []models.SemanticField{
		{Name: "id", Kind: models.FieldKindColumn, Description: "订单主键"},
	}})
	if a.ManifestSHA == b.ManifestSHA {
		t.Fatal("changing a description must change the hash: it changes what the Agent is told")
	}
}

func TestCompileCollectsDefaultFilters(t *testing.T) {
	spec, issues := Compile(CompileInput{
		Model:  baseModel(),
		Fields: []models.SemanticField{field("id", models.FieldKindColumn, "")},
		Rules: []models.SemanticRule{{
			Category: models.RuleDefaultFilter, Title: "排除已删除",
			Body:   "不含软删除",
			Spec:   datatypes.JSON(`{"field":"deleted_at","op":"is_null"}`),
			Status: "active",
		}},
	})
	if HasErrors(issues) {
		t.Fatalf("unexpected errors: %+v", issues)
	}
	if len(spec.Filters) != 1 || spec.Filters[0].Field != "deleted_at" || spec.Filters[0].Op != "is_null" {
		t.Fatalf("default filter not compiled: %+v", spec.Filters)
	}
	if spec.Filters[0].Reason == "" {
		t.Fatal("an auto-applied filter must carry a reason: an unexplained number is worse than no number")
	}
}

func TestCompileRejectsBrokenDefaultFilterSpec(t *testing.T) {
	_, issues := Compile(CompileInput{
		Model:  baseModel(),
		Fields: []models.SemanticField{field("id", models.FieldKindColumn, "")},
		Rules: []models.SemanticRule{{
			Category: models.RuleDefaultFilter, Status: "active",
			Spec: datatypes.JSON(`{"field":"","op":""}`),
		}},
	})
	if !hasCode(issues, IssueDefaultFilterBroken) {
		t.Fatalf("expected %s, got %+v", IssueDefaultFilterBroken, issues)
	}
}

func TestNormalizeModelName(t *testing.T) {
	cases := map[string]string{
		"Orders":        "orders",
		"sales order":   "sales_order",
		"2024 revenue":  "m_2024_revenue",
		"客户订单":          "model",
		"  birth_names ": "birth_names",
	}
	for in, want := range cases {
		if got := NormalizeModelName(in); got != want {
			t.Errorf("NormalizeModelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func hasCode(issues []Issue, code string) bool {
	for _, i := range issues {
		if i.Code == code {
			return true
		}
	}
	return false
}
