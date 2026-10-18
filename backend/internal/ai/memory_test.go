package ai

import (
	"testing"

	"superset/internal/models"
)

// tableColumnFixture builds an active, agent-visible column for schema tests.
func tableColumnFixture(name, desc string) models.TableColumn {
	return models.TableColumn{
		ColumnName: name, Description: desc, Type: "numeric",
		IsActive: true, AgentVisible: true,
	}
}

func TestNormalizeQuestionFoldsFormatting(t *testing.T) {
	cases := [][2]string{
		{"  上个月 华东的 GMV ?", "上个月华东的gmv"},
		{"Top 5 Artists by Playlist Count", "top 5 artists by playlist count"},
		{"GMV，环比？", "gmv环比"},
	}
	for _, tc := range cases {
		if got := normalizeQuestion(tc[0]); got != tc[1] {
			t.Fatalf("normalizeQuestion(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
	if questionFingerprint("上个月 华东的 GMV ?") != questionFingerprint("上个月华东的gmv") {
		t.Fatal("equivalent questions must share a fingerprint")
	}
}

func TestIsExploratorySQL(t *testing.T) {
	exploratory := []string{
		`SELECT * FROM orders LIMIT 10`,
		`SELECT id, amount FROM orders`,
	}
	for _, sql := range exploratory {
		if !isExploratorySQL(sql) {
			t.Fatalf("%s should be treated as exploratory", sql)
		}
	}
	reusable := []string{
		`SELECT region, SUM(amount) FROM orders WHERE dt > '2026-01-01' GROUP BY region`,
		`SELECT COUNT(*) FROM orders`,
		`SELECT SUM(amount) OVER (PARTITION BY region) FROM orders`,
	}
	for _, sql := range reusable {
		if isExploratorySQL(sql) {
			t.Fatalf("%s should be treated as reusable", sql)
		}
	}
}

func TestTextScoreMatchesParaphrase(t *testing.T) {
	if got := textScore("华东区 GMV", "East China GMV 华东 成交总额"); got < 0.4 {
		t.Fatalf("expected a partial match, got %v", got)
	}
	if got := textScore("完全无关的问题 xyzzy", "SELECT sum(amount) FROM orders"); got != 0 {
		t.Fatalf("unrelated text must score 0, got %v", got)
	}
}

func TestRankByTextIsDeterministic(t *testing.T) {
	type item struct{ name string }
	items := []item{{"alpha"}, {"alpha beta"}, {"beta"}}
	first := rankByText("alpha beta", items, func(i item) string { return i.name }, 3, 0)
	second := rankByText("alpha beta", items, func(i item) string { return i.name }, 3, 0)
	if len(first) != len(second) {
		t.Fatal("result length changed between runs")
	}
	for i := range first {
		if first[i].Item.name != second[i].Item.name {
			t.Fatalf("order changed at %d: %s vs %s", i, first[i].Item.name, second[i].Item.name)
		}
	}
}

func TestSchemaCompactSplitsWideTables(t *testing.T) {
	ds := &datasetSchema{}
	ds.Table.TableNameStr = "sales"
	ds.Table.MainDttmCol = "dt"
	for i := 0; i < 200; i++ {
		name := "col_"
		if i%2 == 0 {
			name = "revenue_"
		}
		ds.Columns = append(ds.Columns, tableColumnFixture(name, "金额口径说明 "+name))
	}
	if _, sliced := ds.compactFor("revenue", 1000); !sliced {
		t.Fatal("a wide dataset must fall back to retrieval")
	}
	compact, sliced := ds.compactFor("revenue", 1000)
	if !sliced {
		t.Fatal("expected retrieval")
	}
	if len(compact) > 4000 {
		t.Fatalf("compact context should shrink, got %d chars", len(compact))
	}
}
