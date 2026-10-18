package screens

import "testing"

// TestTemplatesAreComplete guards the gallery: every built-in wall must carry
// a full canvas document, a stable unique key and only known component types.
func TestTemplatesAreComplete(t *testing.T) {
	all := All()
	if len(all) < 16 {
		t.Fatalf("expected at least 16 templates, got %d", len(all))
	}
	seen := map[string]bool{}
	allowed := map[string]bool{
		"kpi": true, "chart": true, "table": true, "text": true, "marquee": true,
		"clock": true, "image": true, "video": true, "iframe": true, "border": true,
		"decoration": true, "progress": true, "shape": true, "richtext": true,
		"filter": true, "group": true, "qrcode": true, "list": true,
	}
	perCategory := map[string]int{}
	for _, tpl := range all {
		if tpl.Key == "" || tpl.Name == "" || tpl.Category == "" {
			t.Fatalf("template is missing identity fields: %+v", tpl)
		}
		if seen[tpl.Key] {
			t.Fatalf("duplicate template key %q", tpl.Key)
		}
		seen[tpl.Key] = true
		perCategory[tpl.Category]++

		if tpl.Width != 1920 || tpl.Height != 1080 {
			t.Fatalf("%s: unexpected canvas %dx%d", tpl.Key, tpl.Width, tpl.Height)
		}
		canvas, ok := tpl.Config["canvas"].(map[string]any)
		if !ok {
			t.Fatalf("%s: config has no canvas", tpl.Key)
		}
		if canvas["background"] == "" || canvas["background"] == nil {
			t.Fatalf("%s: canvas has no background", tpl.Key)
		}
		components, ok := tpl.Config["components"].([]map[string]any)
		if !ok {
			t.Fatalf("%s: components have the wrong type", tpl.Key)
		}
		if len(components) < 12 {
			t.Fatalf("%s: only %d components, expected a full layout", tpl.Key, len(components))
		}
		ids := map[string]bool{}
		kpis, charts := 0, 0
		for _, c := range components {
			kind, _ := c["type"].(string)
			if !allowed[kind] {
				t.Fatalf("%s: component %v has unknown type %q", tpl.Key, c["id"], kind)
			}
			id, _ := c["id"].(string)
			if id == "" || ids[id] {
				t.Fatalf("%s: component id %q missing or duplicated", tpl.Key, id)
			}
			ids[id] = true
			if c["w"].(float64) <= 0 || c["h"].(float64) <= 0 {
				t.Fatalf("%s: component %q has a non-positive size", tpl.Key, id)
			}
			switch kind {
			case "kpi":
				kpis++
			case "chart":
				charts++
			}
		}
		if kpis < 4 || charts < 4 {
			t.Fatalf("%s: expected >=4 KPI and >=4 charts, got %d/%d", tpl.Key, kpis, charts)
		}
	}
	for _, cat := range []string{"gov", "park", "energy", "manufacture"} {
		if perCategory[cat] < 4 {
			t.Fatalf("category %q has %d templates, expected >=4", cat, perCategory[cat])
		}
	}
}

// TestGetReturnsIndependentCopy makes sure a caller cannot mutate the cached
// template document and affect the next screen created from it.
func TestGetReturnsIndependentCopy(t *testing.T) {
	first, ok := Get("gov-service")
	if !ok {
		t.Fatal("gov-service template is missing")
	}
	components := first.Config["components"].([]map[string]any)
	components[0]["name"] = "mutated"

	second, _ := Get("gov-service")
	again := second.Config["components"].([]map[string]any)
	if again[0]["name"] == "mutated" {
		t.Fatal("template config is shared between calls")
	}
}
