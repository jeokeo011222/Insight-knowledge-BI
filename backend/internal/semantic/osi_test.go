package semantic

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"superset/internal/models"
)

// OSI accepts two shapes for an expression (bare string, dialect list) and so
// does JSON. A parser that only handles one of them silently imports an empty
// expression, which compiles into a passthrough column that does not exist.
func TestOSIExprAcceptsBothShapes(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"yaml scalar", `expr: order_id`},
		{"yaml dialect list", `expr:
  dialects:
    - dialect: ANSI_SQL
      expression: "first_name || ' ' || last_name"`},
		{"yaml dialect list, no ANSI", `expr:
  dialects:
    - dialect: SNOWFLAKE
      expression: "amount::float"`},
	}
	for _, c := range cases {
		var wrapper struct {
			Expr osiExpr `yaml:"expr"`
		}
		if err := yaml.Unmarshal([]byte(c.raw), &wrapper); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if strings.TrimSpace(wrapper.Expr.Expression) == "" {
			t.Fatalf("%s: expression came back empty", c.name)
		}
	}

	var jsonWrapper struct {
		Expr osiExpr `json:"expr"`
	}
	if err := json.Unmarshal([]byte(`{"expr":{"dialects":[{"dialect":"ANSI_SQL","expression":"SUM(x)"}]}}`), &jsonWrapper); err != nil {
		t.Fatal(err)
	}
	if jsonWrapper.Expr.Expression != "SUM(x)" {
		t.Fatalf("json dialect list not parsed: %q", jsonWrapper.Expr.Expression)
	}
	if err := json.Unmarshal([]byte(`{"expr":"SUM(y)"}`), &jsonWrapper); err != nil {
		t.Fatal(err)
	}
	if jsonWrapper.Expr.Expression != "SUM(y)" {
		t.Fatalf("json scalar not parsed: %q", jsonWrapper.Expr.Expression)
	}
}

// A bare identifier must stay a scalar: emitting {dialects:[...]} for every
// column makes the file unreadable for the humans who review it in git.
func TestOSIExprMarshalShape(t *testing.T) {
	plain, err := yaml.Marshal(map[string]any{"e": osiExpr{Expression: "order_id"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "dialects") {
		t.Fatalf("bare identifier should stay scalar: %s", plain)
	}
	computed, err := yaml.Marshal(map[string]any{"e": osiExpr{Expression: "SUM(amount)"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(computed), "dialects") {
		t.Fatalf("computed expression needs a dialect tag: %s", computed)
	}
}

func TestParseJoinColumns(t *testing.T) {
	from, to := parseJoinColumns("orders.customer_id = customers.id")
	if len(from) != 1 || from[0] != "orders.customer_id" || to[0] != "customers.id" {
		t.Fatalf("single pair not parsed: %v %v", from, to)
	}
	from, to = parseJoinColumns("a.x = b.y AND a.z = b.w")
	if len(from) != 2 || len(to) != 2 {
		t.Fatalf("composite condition not split: %v %v", from, to)
	}
	// A condition the regex cannot read must not produce garbage columns; the
	// verbatim condition still travels in custom_extensions.
	if from, _ := parseJoinColumns("lower(a.email) = lower(b.email)"); len(from) != 0 {
		t.Fatalf("expression join should yield no structured columns: %v", from)
	}
}

func TestJoinConditionPrefersVerbatim(t *testing.T) {
	rel := osiRel{
		FromColumns: []string{"a.x"}, ToColumns: []string{"b.y"},
		Extensions: []osiExt{{VendorName: OSIVendorName, Data: `{"condition":"a.x = b.y AND a.live"}`}},
	}
	if got := joinCondition(rel); got != "a.x = b.y AND a.live" {
		t.Fatalf("verbatim condition lost: %q", got)
	}
	rel.Extensions = nil
	if got := joinCondition(rel); got != "a.x = b.y" {
		t.Fatalf("structured columns not reassembled: %q", got)
	}
	if got := joinCondition(osiRel{FromColumns: []string{"a.x"}}); got != "" {
		t.Fatalf("mismatched column lists must not invent a condition: %q", got)
	}
}

func TestPickOSITable(t *testing.T) {
	sm := osiModel{Name: "shop", Datasets: []osiTable{
		{Name: "orders", Source: "shop.public.orders"},
		{Name: "customers", Source: "shop.public.customers"},
	}}
	got, rest, err := pickOSITable(sm, "orders")
	if err != nil || got.Name != "orders" || len(rest) != 1 || rest[0] != "customers" {
		t.Fatalf("match by name failed: %+v rest=%v err=%v", got, rest, err)
	}
	got, _, err = pickOSITable(sm, "customers")
	if err != nil || got.Name != "customers" {
		t.Fatalf("match by source failed: %+v err=%v", got, err)
	}
	if _, _, err := pickOSITable(sm, "payments"); err == nil {
		t.Fatal("a document that describes other tables must be rejected")
	}

	// One dataset with a different name is unambiguous; two is not.
	single := osiModel{Datasets: []osiTable{{Name: "fact_sales", Source: "x.y.fact_sales"}}}
	if got, _, err := pickOSITable(single, "orders"); err != nil || got.Name != "fact_sales" {
		t.Fatalf("single-dataset fallback failed: %+v err=%v", got, err)
	}
	dup := osiModel{Datasets: []osiTable{
		{Name: "orders", Source: "a.orders"}, {Name: "orders_v2", Source: "a.orders"},
	}}
	if _, _, err := pickOSITable(dup, "orders"); err == nil {
		t.Fatal("two datasets pointing at the same table must be rejected")
	}
}

func TestPickOSIModel(t *testing.T) {
	one := osiDoc{SemanticModel: []osiModel{{Name: "anything"}}}
	if _, err := pickOSIModel(one, "orders"); err != nil {
		t.Fatalf("single model must be accepted: %v", err)
	}
	multi := osiDoc{SemanticModel: []osiModel{{Name: "shop"}, {Name: "billing"}}}
	if sm, err := pickOSIModel(multi, "billing"); err != nil || sm.Name != "billing" {
		t.Fatalf("name match failed: %+v %v", sm, err)
	}
	if _, err := pickOSIModel(multi, "orders"); err == nil {
		t.Fatal("ambiguous document must be rejected")
	}
}

// Our models hold one column in the primary key. Narrowing a composite key is
// allowed, but it has to be reported: the join cardinality depends on it.
func TestOSIPrimaryKeyNarrowsComposite(t *testing.T) {
	var issues []Issue
	if got := osiPrimaryKey([]any{"ss_item_sk", "ss_ticket_number"}, &issues, "store_sales"); got != "ss_item_sk" {
		t.Fatalf("expected first column, got %q", got)
	}
	if len(issues) != 1 || issues[0].Severity != "warning" {
		t.Fatalf("composite narrowing must warn: %+v", issues)
	}
	issues = nil
	if got := osiPrimaryKey("id", &issues, "orders"); got != "id" || len(issues) != 0 {
		t.Fatalf("scalar key must pass through: %q %+v", got, issues)
	}
}

// The vendor block is where every attribute OSI has no field for travels.
// Losing it turns a governed field into an anonymous column.
func TestOSIRoundTripKeepsFieldMeta(t *testing.T) {
	doc := osiDoc{Version: OSIVersion, SemanticModel: []osiModel{{
		Name: "shop",
		Datasets: []osiTable{{
			Name: "orders", Source: "shop.public.orders",
			Fields: []osiField{{
				Name:       "amount",
				Expression: osiExpr{Expression: "amount"},
				Dimension:  &osiDim{IsTime: false},
				Extensions: []osiExt{{VendorName: OSIVendorName, Data: extJSON(osiFieldMeta{
					Kind: "column", Type: "DECIMAL(18,2)", Unit: "CNY",
					Enum: []string{"a"}, PIILevel: "low", DefaultAgg: "SUM",
					Synonyms: []string{"金额"},
				})}},
			}},
		}},
		Metrics: []osiMetric{{
			Name: "gmv", Expression: osiExpr{Expression: "SUM(orders.amount)"},
			Extensions: []osiExt{{VendorName: OSIVendorName, Data: extJSON(osiFieldMeta{
				Kind: "metric", Dataset: "orders",
			})}},
		}},
	}}}
	blob, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var back osiDoc
	if err := yaml.Unmarshal(blob, &back); err != nil {
		t.Fatalf("%v\n%s", err, blob)
	}
	f := back.SemanticModel[0].Datasets[0].Fields[0]
	meta, _ := readExt(f.Extensions)
	if meta.Kind != "column" || meta.Type != "DECIMAL(18,2)" || meta.Unit != "CNY" ||
		meta.PIILevel != "low" || meta.DefaultAgg != "SUM" || len(meta.Enum) != 1 || len(meta.Synonyms) != 1 {
		t.Fatalf("field metadata lost in round trip: %+v", meta)
	}
	if f.Dimension == nil || f.Dimension.IsTime {
		t.Fatalf("dimension hint lost: %+v", f.Dimension)
	}
	m := back.SemanticModel[0].Metrics[0]
	if m.Expression.Expression != "SUM(orders.amount)" {
		t.Fatalf("metric expression lost: %q", m.Expression.Expression)
	}
	mm, _ := readExt(m.Extensions)
	if mm.Dataset != "orders" {
		t.Fatalf("metric owner lost: %+v", mm)
	}
}

// A Wren-produced file carries the type only in its own vendor block; reading
// it keeps OSI useful as an import format rather than export-only.
func TestReadExtFallsBackToWrenType(t *testing.T) {
	meta, wrenType := readExt([]osiExt{{VendorName: "WREN", Data: `{"type":"BIGINT"}`}})
	if wrenType != "BIGINT" || meta.Type != "" {
		t.Fatalf("wren type not read: %q %+v", wrenType, meta)
	}
	meta, wrenType = readExt([]osiExt{
		{VendorName: "WREN", Data: `{"type":"BIGINT"}`},
		{VendorName: OSIVendorName, Data: `{"type":"NUMERIC","kind":"metric"}`},
	})
	if wrenType != "BIGINT" || meta.Type != "NUMERIC" || meta.Kind != "metric" {
		t.Fatalf("our block must win over wren's: %q %+v", wrenType, meta)
	}
}

func TestOSISource(t *testing.T) {
	if got := osiSource(models.SqlaTable{Catalog: "cat", Schema: "public", TableNameStr: "orders"}); got != "cat.public.orders" {
		t.Fatalf("qualified source expected, got %q", got)
	}
	if got := osiSource(models.SqlaTable{Schema: "public", TableNameStr: "orders"}); got != "public.orders" {
		t.Fatalf("empty catalog must not leave a leading dot: %q", got)
	}
	// A virtual dataset has no table name that can be queried; its SQL is the
	// only honest source.
	if got := osiSource(models.SqlaTable{TableNameStr: "v", SQL: "SELECT 1 AS x"}); got != "SELECT 1 AS x" {
		t.Fatalf("virtual dataset should expose its SQL, got %q", got)
	}
}

// An OSI metric that aggregates another dataset cannot be evaluated by a
// per-dataset model. Importing it anyway yields a metric whose SQL fails at the
// engine, which is worse than refusing: the model looks complete and is not.
func TestMetricDatasetsDetectsForeignReference(t *testing.T) {
	names := datasetNames(osiModel{Datasets: []osiTable{{Name: "birth_names"}, {Name: "orders"}}})
	got := metricDatasets("SUM(orders.amount)", names)
	if len(got) != 1 || got[0] != "orders" {
		t.Fatalf("foreign dataset reference not detected: %v", got)
	}
	if got := metricDatasets("SUM(orders.amount) / COUNT(birth_names.id)", names); len(got) != 2 {
		t.Fatalf("both datasets should be reported: %v", got)
	}
	// A bare column, a wildcard and a decimal literal must not look like
	// dataset references.
	for _, expr := range []string{"COUNT(*)", "SUM(num)", "num * 1.0", "amount / 2.5"} {
		if got := metricDatasets(expr, names); len(got) != 0 {
			t.Fatalf("%q produced false references: %v", expr, got)
		}
	}
	// A schema-qualified column is ambiguous (catalog.schema.column looks the
	// same as dataset.column). Whatever comes back must at worst name the local
	// dataset — never a foreign one.
	for _, r := range metricDatasets("public.birth_names.num", names) {
		if r != "birth_names" {
			t.Fatalf("schema prefix misread as a foreign dataset: %v", r)
		}
	}
}
