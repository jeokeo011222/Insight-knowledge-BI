package api

import (
	"encoding/json"
	"testing"

	"superset/internal/queries"
)

func TestAnnotationIDAcceptsEveryShape(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int
	}{
		{"int", 7, 7},
		{"float whole", float64(7), 7},
		{"json number", json.Number("7"), 7},
		{"string", "7", 7},
		{"object id", map[string]any{"id": 7}, 7},
		{"object value", map[string]any{"value": "7"}, 7},
		{"object layer_id", map[string]any{"layer_id": float64(7)}, 7},
		{"zero", 0, 0},
		{"negative", -3, 0},
		{"fraction", 7.5, 0},
		{"not a number", "abc", 0},
		{"nil", nil, 0},
		{"empty object", map[string]any{}, 0},
	}
	for _, tc := range cases {
		if got := annotationID(tc.in); got != tc.want {
			t.Errorf("%s: annotationID(%v) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestAnnotationIDsFromContextMergesSourcesAndDedupes(t *testing.T) {
	qc := &queries.QueryContext{
		FormData: map[string]any{"annotation_layers": []any{float64(1), float64(2)}},
		Queries: []queries.QueryObject{
			{AnnotationLayers: []any{1, "2"}},
		},
	}
	got := annotationIDsFromContext(qc)
	want := []int{1, 2}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestAnnotationIDsFromContextFallsBackToFormData(t *testing.T) {
	// Dashboard tiles rebuild the query from stored params, so the query object
	// carries no annotation_layers at all.
	qc := &queries.QueryContext{
		FormData: map[string]any{"annotation_layers": []any{float64(4)}},
		Queries:  []queries.QueryObject{{}},
	}
	got := annotationIDsFromContext(qc)
	if len(got) != 1 || got[0] != 4 {
		t.Fatalf("got %v, want [4]", got)
	}
}

func TestAnnotationIDsFromContextCapsLayers(t *testing.T) {
	list := make([]any, 0, annotationLayerLimit+5)
	for i := 1; i <= annotationLayerLimit+5; i++ {
		list = append(list, float64(i))
	}
	qc := &queries.QueryContext{
		Queries: []queries.QueryObject{{AnnotationLayers: list}},
	}
	if got := annotationIDsFromContext(qc); len(got) != annotationLayerLimit {
		t.Fatalf("got %d ids, want the cap %d", len(got), annotationLayerLimit)
	}
}

func TestAnnotationIDsFromContextWithoutSelection(t *testing.T) {
	if got := annotationIDsFromContext(nil); got != nil {
		t.Fatalf("nil context must yield nil, got %v", got)
	}
	if got := annotationIDsFromContext(&queries.QueryContext{}); len(got) != 0 {
		t.Fatalf("empty context must yield no ids, got %v", got)
	}
}

func TestAnnotationWindowTakesTheWidestQuery(t *testing.T) {
	qc := &queries.QueryContext{
		Queries: []queries.QueryObject{
			{FromDttm: "2026-09-01", ToDttm: "2026-09-10"},
			{FromDttm: "2026-08-15T00:00:00", ToDttm: "2026-09-30"},
			{FromDttm: "", ToDttm: ""},
		},
	}
	window := annotationWindowOf(qc)
	if window.From == nil || window.From.Format("2006-01-02") != "2026-08-15" {
		t.Fatalf("unexpected lower bound: %v", window.From)
	}
	if window.To == nil || window.To.Format("2006-01-02") != "2026-09-30" {
		t.Fatalf("unexpected upper bound: %v", window.To)
	}
}
