// Package screens holds the built-in data-visualisation wall templates.
//
// A template is nothing but a canvas document (position, style, demo data).
// Keeping them as Go data — rather than SQL seeds — means a fresh install has
// a populated template gallery with no migration ordering problems, and a
// template can never drift from the component schema.
package screens

import (
	"fmt"
	"sort"
)

// Template is one entry of the built-in gallery.
type Template struct {
	Key         string         `json:"key"`
	Name        string         `json:"name"`
	Category    string         `json:"category"`
	CategoryCN  string         `json:"category_cn"`
	Description string         `json:"description"`
	Cover       string         `json:"cover"`
	Tags        []string       `json:"tags"`
	Width       int            `json:"width"`
	Height      int            `json:"height"`
	Theme       string         `json:"theme"`
	Background  string         `json:"background"`
	Config      map[string]any `json:"config"`
}

type palette struct {
	key     string
	bg      string
	text    string
	muted   string
	primary string
	accent  string
	panel   string
	border  string
}

var palettes = map[string]palette{
	"tech-blue": {
		key:     "tech-blue",
		bg:      "radial-gradient(120% 90% at 50% 0%, #0d3563 0%, #071a33 52%, #030b17 100%)",
		text:    "#e9f4ff",
		muted:   "#7ea9cc",
		primary: "#37a2ff",
		accent:  "#00e5ff",
		panel:   "rgba(13,53,99,0.42)",
		border:  "rgba(55,162,255,0.38)",
	},
	"tech-cyan": {
		key:     "tech-cyan",
		bg:      "radial-gradient(120% 90% at 50% 0%, #063a4a 0%, #041f2b 55%, #020c12 100%)",
		text:    "#e6fbff",
		muted:   "#79b8c6",
		primary: "#00e0d0",
		accent:  "#59f2ff",
		panel:   "rgba(6,58,74,0.42)",
		border:  "rgba(0,224,208,0.36)",
	},
	"tech-green": {
		key:     "tech-green",
		bg:      "radial-gradient(120% 90% at 50% 0%, #073a2c 0%, #04221a 55%, #020d0a 100%)",
		text:    "#e7fff5",
		muted:   "#77b39c",
		primary: "#00e39a",
		accent:  "#9dff6b",
		panel:   "rgba(7,58,44,0.42)",
		border:  "rgba(0,227,154,0.36)",
	},
	"tech-gold": {
		key:     "tech-gold",
		bg:      "radial-gradient(120% 90% at 50% 0%, #3a2606 0%, #221604 55%, #0d0902 100%)",
		text:    "#fff6e6",
		muted:   "#c0a173",
		primary: "#ffb020",
		accent:  "#ff7043",
		panel:   "rgba(58,38,6,0.42)",
		border:  "rgba(255,176,32,0.36)",
	},
	"tech-violet": {
		key:     "tech-violet",
		bg:      "radial-gradient(120% 90% at 50% 0%, #221a4d 0%, #130f2e 55%, #07050f 100%)",
		text:    "#f0ecff",
		muted:   "#9a8fc4",
		primary: "#8b7bff",
		accent:  "#38e8ff",
		panel:   "rgba(34,26,77,0.45)",
		border:  "rgba(139,123,255,0.38)",
	},
}

type kpiSpec struct {
	Label string
	Value string
	Unit  string
	Trend float64
	Color string
	Spark []float64
}

type seriesSpec struct {
	Name string
	Data []float64
}

type chartSpec struct {
	Title      string
	Kind       string
	Categories []string
	Series     []seriesSpec
	Unit       string
	Color      string
	Legend     bool
}

type screenSpec struct {
	Key      string
	Name     string
	Category string
	Desc     string
	Tags     []string
	Palette  string
	Variant  string // wall | grid | ops
	Title    string
	Subtitle string
	KPIs     []kpiSpec
	Charts   []chartSpec
	Bottom   string
}

// All returns every built-in template, sorted by category then key so the
// gallery order is stable across restarts.
func All() []Template {
	specs := specs()
	out := make([]Template, 0, len(specs))
	for _, spec := range specs {
		out = append(out, build(spec))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category == out[j].Category {
			return out[i].Key < out[j].Key
		}
		return categoryOrder(out[i].Category) < categoryOrder(out[j].Category)
	})
	return out
}

// Get resolves one template by its key.
func Get(key string) (Template, bool) {
	for _, spec := range specs() {
		if spec.Key == key {
			return build(spec), true
		}
	}
	return Template{}, false
}

// Categories lists the gallery filter chips.
func Categories() []map[string]string {
	return []map[string]string{
		{"key": "gov", "name": "政务", "name_en": "Government"},
		{"key": "park", "name": "园区", "name_en": "Campus"},
		{"key": "energy", "name": "能源", "name_en": "Energy"},
		{"key": "manufacture", "name": "制造", "name_en": "Manufacturing"},
	}
}

func categoryOrder(key string) int {
	switch key {
	case "gov":
		return 0
	case "park":
		return 1
	case "energy":
		return 2
	case "manufacture":
		return 3
	}
	return 9
}

// build turns one compact spec into a full canvas document.
func build(spec screenSpec) Template {
	pal := palettes[spec.Palette]
	if pal.key == "" {
		pal = palettes["tech-blue"]
	}
	components := make([]map[string]any, 0, 20)

	// Layer 0: background texture + corner decorations.
	components = append(components,
		comp("bg-grid", "decoration", "背景网格", 0, 0, 1920, 1080, 0, map[string]any{
			"variant": "grid", "color": pal.primary, "opacity": 0.10,
		}),
		comp("bg-glow", "decoration", "光晕", 0, 0, 1920, 1080, 1, map[string]any{
			"variant": "glow", "color": pal.primary, "opacity": 0.35,
		}),
	)

	// Header: title, subtitle, live clock.
	components = append(components,
		comp("title", "text", "主标题", 320, 18, 1280, 58, 10, map[string]any{
			"text": spec.Title, "fontSize": 38, "color": pal.text, "align": "center",
			"bold": true, "letterSpacing": 6, "fontFamily": "var(--screen-title-font)",
		}),
		comp("subtitle", "text", "副标题", 320, 78, 1280, 26, 10, map[string]any{
			"text": spec.Subtitle, "fontSize": 14, "color": pal.muted, "align": "center",
			"letterSpacing": 3,
		}),
		comp("clock", "clock", "实时时钟", 1600, 22, 280, 60, 10, map[string]any{
			"format": "datetime", "color": pal.text, "fontSize": 20, "align": "right",
			"mutedColor": pal.muted,
		}),
		comp("header-line", "shape", "标题装饰线", 320, 106, 1280, 2, 9, map[string]any{
			"shape": "rect", "fill": pal.border,
		}),
	)

	// KPI row.
	kpiY := 122.0
	kpiH := 118.0
	count := len(spec.KPIs)
	for i, k := range spec.KPIs {
		x, w := kpiSlot(i, count, spec.Variant)
		color := k.Color
		if color == "" {
			color = pal.primary
		}
		spark := k.Spark
		if len(spark) == 0 {
			spark = seq(12, 40, 30, 7+i*13)
		}
		components = append(components, comp(
			fmt.Sprintf("kpi-%d", i+1), "kpi", k.Label, x, kpiY, w, kpiH, 20,
			map[string]any{
				"label": k.Label, "value": k.Value, "unit": k.Unit,
				"trend": k.Trend, "color": color, "spark": spark,
				"panel": pal.panel, "border": pal.border, "muted": pal.muted,
			},
		))
	}

	// Charts.
	slots := chartSlots(spec.Variant)
	for i, ch := range spec.Charts {
		if i >= len(slots) {
			break
		}
		color := ch.Color
		if color == "" {
			if i%2 == 0 {
				color = pal.primary
			} else {
				color = pal.accent
			}
		}
		series := make([]map[string]any, 0, len(ch.Series))
		for _, s := range ch.Series {
			series = append(series, map[string]any{"name": s.Name, "data": s.Data})
		}
		slot := slots[i]
		components = append(components, comp(
			fmt.Sprintf("chart-%d", i+1), "chart", ch.Title, slot[0], slot[1], slot[2], slot[3], 30,
			map[string]any{
				"title": ch.Title, "kind": ch.Kind, "categories": ch.Categories,
				"series": series, "unit": ch.Unit, "color": color,
				"legend": ch.Legend, "palette": []string{pal.primary, pal.accent, "#ffd166", "#ff7a7a", "#8b7bff"},
				"panel": pal.panel, "border": pal.border, "muted": pal.muted, "text": pal.text,
			},
		))
	}

	// Footer marquee + bottom line.
	components = append(components,
		comp("marquee", "marquee", "滚动播报", 40, 1006, 1840, 40, 40, map[string]any{
			"text": spec.Bottom, "speed": 60, "color": pal.muted, "fontSize": 14,
			"background": "transparent",
		}),
		comp("footer-line", "shape", "底部装饰线", 40, 998, 1840, 1, 9, map[string]any{
			"shape": "rect", "fill": pal.border,
		}),
	)

	config := map[string]any{
		"version": 1,
		"canvas": map[string]any{
			"width": 1920, "height": 1080, "background": pal.bg,
			"gridSize": 8, "showGrid": true, "scaleMode": "fit",
			"theme": pal.key, "padding": 40,
		},
		"theme": map[string]any{
			"primary": pal.primary, "accent": pal.accent, "text": pal.text,
			"muted": pal.muted, "panel": pal.panel, "border": pal.border,
		},
		"components": components,
		"filters":    []any{},
		"interaction": map[string]any{
			"refreshSec": 60, "watermark": false, "showToolbar": false,
			"autoRotate": false, "rotateSec": 15,
		},
		"meta": map[string]any{
			"category": spec.Category, "templateKey": spec.Key, "tags": spec.Tags,
			"variant": spec.Variant,
		},
	}

	cover := fmt.Sprintf(
		"linear-gradient(135deg, %s 0%%, %s 55%%, #050b14 100%%)",
		pal.primary, pal.accent,
	)

	return Template{
		Key: spec.Key, Name: spec.Name, Category: spec.Category,
		CategoryCN: categoryCN(spec.Category), Description: spec.Desc,
		Cover: cover, Tags: spec.Tags, Width: 1920, Height: 1080,
		Theme: pal.key, Background: pal.bg, Config: config,
	}
}

func categoryCN(key string) string {
	for _, item := range Categories() {
		if item["key"] == key {
			return item["name"]
		}
	}
	return key
}

// comp builds one absolutely positioned component document.
func comp(id, kind, name string, x, y, w, h, z float64, props map[string]any) map[string]any {
	return map[string]any{
		"id": id, "type": kind, "name": name,
		"x": x, "y": y, "w": w, "h": h, "rotate": 0,
		"z": z, "locked": false, "visible": true,
		"props": props, "style": map[string]any{},
	}
}

// kpiSlot lays out the KPI cards. `grid` uses six narrow cards, the other
// variants use four wide cards with room for a side panel.
func kpiSlot(i, total int, variant string) (x, w float64) {
	if variant == "grid" || total >= 6 {
		const cardW, gap = 290.0, 12.0
		return 40 + float64(i)*(cardW+gap), cardW
	}
	const cardW, gap = 300.0, 20.0
	return 40 + float64(i)*(cardW+gap), cardW
}

// chartSlots returns the (x, y, w, h) rectangles used by each layout variant.
func chartSlots(variant string) [][4]float64 {
	switch variant {
	case "grid":
		return [][4]float64{
			{40, 262, 600, 300}, {660, 262, 600, 300}, {1280, 262, 600, 300},
			{40, 578, 600, 300}, {660, 578, 600, 300}, {1280, 578, 600, 300},
		}
	case "ops":
		return [][4]float64{
			{40, 262, 560, 616},
			{620, 262, 660, 300}, {620, 578, 660, 300},
			{1300, 262, 580, 616},
		}
	default: // wall
		return [][4]float64{
			{40, 262, 440, 300}, {40, 578, 440, 300},
			{500, 262, 920, 616},
			{1440, 262, 440, 300}, {1440, 578, 440, 300},
		}
	}
}

// seq produces a deterministic pseudo-random series so every template has a
// distinct but stable shape (no randomness at serve time).
func seq(n int, base, amp float64, seed int) []float64 {
	out := make([]float64, n)
	x := uint32(seed*2654435761 + 12345)
	for i := range out {
		x = x*1664525 + 1013904223
		r := float64(x%1000) / 1000.0
		out[i] = float64(int((base+(r-0.4)*amp)*10)) / 10
	}
	return out
}

// months is the shared x-axis for monthly trend charts.
func months() []string {
	return []string{"1月", "2月", "3月", "4月", "5月", "6月", "7月", "8月", "9月", "10月", "11月", "12月"}
}

func days() []string {
	return []string{"周一", "周二", "周三", "周四", "周五", "周六", "周日"}
}

func sv(name string, data []float64) seriesSpec { return seriesSpec{Name: name, Data: data} }

func chart(title, kind string, cats []string, series ...seriesSpec) chartSpec {
	return chartSpec{Title: title, Kind: kind, Categories: cats, Series: series, Legend: len(series) > 1}
}
