package api

import (
	"strings"
	"testing"
)

func validScreenConfig() []byte {
	return []byte(`{
		"version": 1,
		"canvas": {"width": 1920, "height": 1080, "background": "#000"},
		"components": [
			{"id": "a", "type": "kpi", "x": 0, "y": 0, "w": 300, "h": 120, "props": {"label": "x"}},
			{"id": "b", "type": "chart", "x": 0, "y": 140, "w": 600, "h": 300, "props": {"kind": "bar"}}
		]
	}`)
}

func TestValidateScreenConfigAcceptsPalette(t *testing.T) {
	doc, err := validateScreenConfig(validScreenConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(doc.Components) != 2 {
		t.Fatalf("components = %d, want 2", len(doc.Components))
	}
}

// The screen document is rendered on a *public* page, so write-time validation
// is the only thing standing between an author and stored XSS.
func TestValidateScreenConfigRejectsScriptPayloads(t *testing.T) {
	cases := map[string]string{
		"script tag":     `{"version":1,"canvas":{},"components":[{"id":"a","type":"richtext","w":1,"h":1,"props":{"html":"<script>alert(1)</script>"}}]}`,
		"javascript url": `{"version":1,"canvas":{},"components":[{"id":"a","type":"image","w":1,"h":1,"props":{"src":"javascript:alert(1)"}}]}`,
		"vbscript url":   `{"version":1,"canvas":{},"components":[{"id":"a","type":"iframe","w":1,"h":1,"props":{"src":"VBScript:msgbox"}}]}`,
	}
	for name, payload := range cases {
		if _, err := validateScreenConfig([]byte(payload)); err == nil {
			t.Fatalf("%s: expected rejection", name)
		} else if !strings.Contains(err.Error(), "forbidden") {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
	}
}

func TestValidateScreenConfigRejectsBadShape(t *testing.T) {
	cases := map[string]string{
		"unknown type":  `{"version":1,"canvas":{},"components":[{"id":"a","type":"hologram","w":1,"h":1}]}`,
		"duplicate id":  `{"version":1,"canvas":{},"components":[{"id":"a","type":"kpi","w":1,"h":1},{"id":"a","type":"kpi","w":1,"h":1}]}`,
		"missing id":    `{"version":1,"canvas":{},"components":[{"id":"","type":"kpi","w":1,"h":1}]}`,
		"negative size": `{"version":1,"canvas":{},"components":[{"id":"a","type":"kpi","w":-1,"h":1}]}`,
		"tiny canvas":   `{"version":1,"canvas":{"width":10,"height":10},"components":[]}`,
	}
	for name, payload := range cases {
		if _, err := validateScreenConfig([]byte(payload)); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
	}
}

func TestValidateScreenConfigEnforcesSizeLimit(t *testing.T) {
	huge := make([]byte, screenMaxConfigBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	if _, err := validateScreenConfig(huge); err == nil {
		t.Fatal("expected an oversize config to be rejected")
	}
}
