package queries

import (
	"strings"
	"testing"

	"superset/internal/connectors"
	"superset/internal/models"
)

// toSemanticDataset is the minimum a semantic_query call needs: one dimension
// column and a COUNT(*) metric.
func toSemanticDataset() *DatasetInfo {
	return &DatasetInfo{
		Table: &models.SqlaTable{TableNameStr: "birth_names", MainDttmCol: "ds"},
		Columns: []models.TableColumn{
			{ColumnName: "gender", Type: "character varying"},
			{ColumnName: "num", Type: "integer"},
		},
	}
}

// The semantic_query tool builds typed AdhocColumn values, while the HTTP API
// hands the builder JSON maps. columnItem only understood the map shape, so
// every semantic query failed with "Unsupported column definition".
func TestBuildSQLAcceptsTypedAdhocColumn(t *testing.T) {
	q := &QueryObject{
		Groupby: []any{AdhocColumn{ColumnName: "gender", Label: "gender", HasCustomLabel: true}},
		Metrics: []any{AdhocMetric{SQLExpression: "COUNT(*)", Label: "count", HasCustomLabel: true}},
	}
	built, err := BuildSQL(toSemanticDataset(), q, connectors.GenericSpec(), BuildOptions{DefaultRowLimit: 100, MaxRow: 1000})
	if err != nil {
		t.Fatalf("typed AdhocColumn rejected: %v", err)
	}
	if !strings.Contains(built.SQL, "GROUP BY") {
		t.Fatalf("missing GROUP BY: %s", built.SQL)
	}
	if !strings.Contains(strings.ToUpper(built.SQL), "COUNT(*) AS") {
		t.Fatalf("missing row count projection: %s", built.SQL)
	}
}

// The map form must keep working — it is what the chart API sends.
func TestBuildSQLAcceptsMappedAdhocColumn(t *testing.T) {
	q := &QueryObject{
		Groupby: []any{map[string]any{"column_name": "gender", "label": "gender", "hasCustomLabel": true}},
		Metrics: []any{map[string]any{"sqlExpression": "COUNT(*)", "label": "count", "hasCustomLabel": true}},
	}
	built, err := BuildSQL(toSemanticDataset(), q, connectors.GenericSpec(), BuildOptions{DefaultRowLimit: 100, MaxRow: 1000})
	if err != nil {
		t.Fatalf("map AdhocColumn rejected: %v", err)
	}
	if !strings.Contains(built.SQL, "GROUP BY") {
		t.Fatalf("missing GROUP BY: %s", built.SQL)
	}
}

// A calculated dimension goes through SQLExpression, which must not be quoted
// as an identifier.
func TestBuildSQLKeepsDimensionExpressionsUntouched(t *testing.T) {
	q := &QueryObject{
		Groupby: []any{AdhocColumn{SQLExpression: "UPPER(gender)", Label: "gender_up", HasCustomLabel: true}},
		Metrics: []any{AdhocMetric{SQLExpression: "COUNT(*)", Label: "count", HasCustomLabel: true}},
	}
	built, err := BuildSQL(toSemanticDataset(), q, connectors.GenericSpec(), BuildOptions{DefaultRowLimit: 100, MaxRow: 1000})
	if err != nil {
		t.Fatalf("dimension expression rejected: %v", err)
	}
	if !strings.Contains(built.SQL, "UPPER(gender)") {
		t.Fatalf("expression got rewritten: %s", built.SQL)
	}
}
