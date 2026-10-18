package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"superset/internal/models"
)

// TestToolRowFromInputValidatesHTTPTools pins the guards that keep a broken tool
// out of the registry: a non-http scheme, an empty URL, an unknown method and
// malformed headers must all be rejected at save time.
func TestToolRowFromInputValidatesHTTPTools(t *testing.T) {
	s := &Service{}
	cases := []struct {
		name string
		in   ToolPluginInput
		ok   bool
	}{
		{"minimal get", ToolPluginInput{Title: "天气", Kind: "http", URL: "https://api.example.com/weather"}, true},
		{"post with headers", ToolPluginInput{
			Title: "工单", Kind: "http", Method: "post",
			URL: "https://api.example.com/ticket", Headers: `{"X-Token":"abc"}`,
		}, true},
		{"missing title", ToolPluginInput{Kind: "http", URL: "https://api.example.com"}, false},
		{"missing url", ToolPluginInput{Title: "空地址", Kind: "http"}, false},
		{"file scheme", ToolPluginInput{Title: "本地文件", Kind: "http", URL: "file:///c:/x.db"}, false},
		{"bad method", ToolPluginInput{Title: "怪方法", Kind: "http", Method: "TRACE", URL: "https://a.com"}, false},
		{"bad headers", ToolPluginInput{Title: "头", Kind: "http", URL: "https://a.com", Headers: "[1,2]"}, false},
		{"bad schema", ToolPluginInput{Title: "参", Kind: "http", URL: "https://a.com", InputSchema: "{oops"}, false},
		{"bad side effect", ToolPluginInput{Title: "副", Kind: "http", URL: "https://a.com", SideEffect: "boom"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.toolRowFromInput(nil, &c.in)
			if c.ok && err != nil {
				t.Fatalf("expected ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

// TestSkillRowFromInputRequiresInstructions guards the rule that a custom skill
// without a prompt would register an executor that always fails.
func TestSkillRowFromInputRequiresInstructions(t *testing.T) {
	s := &Service{}
	if _, err := s.skillRowFromInput(nil, &SkillPluginInput{Title: "口径检查"}); err == nil {
		t.Fatal("custom skill without instructions must be rejected")
	}
	row, err := s.skillRowFromInput(nil, &SkillPluginInput{
		Title: "口径检查", Instructions: "你是财务口径审核员，请检查以下指标定义是否自相矛盾。",
		ToolCodes: []string{"schema_reader"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.Enabled != true {
		t.Fatal("a new skill defaults to enabled")
	}
	if DecodeCodes(row.ToolCodes)[0] != "schema_reader" {
		t.Fatalf("tool codes not encoded: %s", row.ToolCodes)
	}
	if row.InputSchema != `{"type":"object","properties":{}}` {
		t.Fatalf("default schema missing: %s", row.InputSchema)
	}
	// Built-ins are exempt: their behaviour lives in code, so an empty prompt is
	// legitimate and must not block toggling them.
	builtin := &models.AISkillRegistry{Code: "nl2sql", IsBuiltin: true}
	if _, err := s.skillRowFromInput(builtin, &SkillPluginInput{Title: "自然语言问数"}); err != nil {
		t.Fatalf("built-in update must not require instructions: %v", err)
	}
}

// TestPlaceholderSchemaDetection pins the marker used to repair built-in rows
// seeded before their real schema existed: only a missing/empty schema counts
// as a placeholder, so an operator's edited (non-empty) schema is never
// overwritten on startup.
func TestPlaceholderSchemaDetection(t *testing.T) {
	cases := map[string]bool{
		"":                                  true,
		"  ":                                true,
		"{}":                                true,
		`{"type":"object","properties":{}}`: true,
		`{"type":"object","properties":{"sql":{"type":"string"}}}`: false,
		`{"type":"array"}`: false,
	}
	for schema, want := range cases {
		if got := isPlaceholderSchema(schema); got != want {
			t.Errorf("isPlaceholderSchema(%q)=%v, want %v", schema, got, want)
		}
	}
	if got := schemaJSON(nil); got != emptyObjectSchema {
		t.Errorf("empty registry schema should fall back to the placeholder: %q", got)
	}
	if got := schemaJSON(json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`)); got == emptyObjectSchema {
		t.Error("a real registry schema must not be replaced by the placeholder")
	}
}

// TestHTTPToolExecutorSubstitutesArguments proves the declared placeholders are
// filled from the call arguments and that a JSON response reaches the Agent as
// structured data instead of a string blob.
func TestHTTPToolExecutorSubstitutesArguments(t *testing.T) {
	var gotQuery, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("city")
		gotUser = r.Header.Get("X-User")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"temp":21}`))
	}))
	defer srv.Close()

	row := &models.AIToolRegistry{
		Code: "weather_lookup", Title: "天气", Kind: "http", Method: "GET",
		URL:     srv.URL + "/weather?city={{city}}",
		Headers: `{"X-User":"{{user}}"}`,
	}
	args, _ := json.Marshal(map[string]any{"city": "上海", "user": "alice"})
	res, err := httpToolExecutor(row)(context.Background(), args)
	if err != nil {
		t.Fatalf("executor failed: %v", err)
	}
	if gotQuery != "上海" {
		t.Fatalf("query placeholder not substituted: %q", gotQuery)
	}
	if gotUser != "alice" {
		t.Fatalf("header placeholder not substituted: %q", gotUser)
	}
	out, ok := res.Output.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type %T", res.Output)
	}
	body, ok := out["body"].(map[string]any)
	if !ok || body["temp"] != float64(21) {
		t.Fatalf("JSON body not parsed: %#v", out["body"])
	}
}

// TestInlineJSONKeepsNumbersUnquoted makes sure a numeric argument inside a
// quoted body placeholder stays a JSON number, otherwise every remote API would
// receive strings where it expects integers.
func TestInlineJSONKeepsNumbersUnquoted(t *testing.T) {
	if got := inlineJSON(float64(42)); got != "42" {
		t.Fatalf("number rendered as %q", got)
	}
	if got := inlineJSON("a\"b"); got != `a\"b` {
		t.Fatalf("string not escaped: %q", got)
	}
	body := substituteVars(`{"limit":{{n}},"name":"{{name}}"}`, map[string]any{
		"n": float64(10), "name": "上海",
	}, inlineJSON)
	if !strings.Contains(body, `"limit":10`) || !strings.Contains(body, `"name":"上海"`) {
		t.Fatalf("body template rendered wrong: %s", body)
	}
}
