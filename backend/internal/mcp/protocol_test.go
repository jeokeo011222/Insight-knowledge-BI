package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testServer() *Server {
	return &Server{
		Name: "test", Version: "0.0.1",
		Tools: []Tool{{
			Name: "echo", Description: "echoes",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
				return map[string]any{"got": string(raw)}, nil
			},
		}, {
			Name: "fail", Description: "always fails",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler: func(context.Context, json.RawMessage) (any, error) {
				return nil, errors.New("boom")
			},
		}},
	}
}

// run feeds lines through the server and returns the decoded responses.
func run(t *testing.T, s *Server, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var resp []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("response is not JSON: %v (%q)", err, line)
		}
		resp = append(resp, m)
	}
	return resp
}

func TestInitializeEchoesProtocolVersion(t *testing.T) {
	resp := run(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if len(resp) != 1 {
		// A reply to the notification is a protocol violation: clients hang or
		// disconnect when they get one.
		t.Fatalf("expected exactly one response, got %d: %+v", len(resp), resp)
	}
	result, _ := resp[0]["result"].(map[string]any)
	if result["protocolVersion"] != "2025-03-26" {
		t.Fatalf("server must speak the client's version, got %v", result["protocolVersion"])
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatalf("tools capability missing: %+v", result["capabilities"])
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "test" || info["version"] != "0.0.1" {
		t.Fatalf("serverInfo wrong: %+v", info)
	}
}

func TestToolsListAndCall(t *testing.T) {
	resp := run(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"x":1}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fail","arguments":{}}}`)
	if len(resp) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(resp))
	}
	tools, _ := resp[0]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] != "echo" || first["inputSchema"] == nil {
		t.Fatalf("tool entry malformed: %+v", first)
	}

	ok, _ := resp[1]["result"].(map[string]any)
	if ok["isError"] != false {
		t.Fatalf("successful call flagged as error: %+v", ok)
	}
	structured, _ := ok["structuredContent"].(map[string]any)
	if structured["got"] != `{"x":1}` {
		t.Fatalf("arguments not forwarded verbatim: %+v", structured)
	}

	// A tool failure is a result with isError=true, not a JSON-RPC error: the
	// calling model is supposed to read the text and try again.
	failed, _ := resp[2]["result"].(map[string]any)
	if failed["isError"] != true {
		t.Fatalf("tool failure must be isError=true: %+v", failed)
	}
	if _, hasErr := resp[2]["error"]; hasErr {
		t.Fatalf("tool failure must not be a protocol error: %+v", resp[2])
	}
}

func TestUnknownMethodAndTool(t *testing.T) {
	resp := run(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope"}}`)
	if code := errCode(resp[0]); code != CodeMethodNotFound {
		t.Fatalf("expected %d, got %d", CodeMethodNotFound, code)
	}
	if code := errCode(resp[1]); code != CodeInvalidParams {
		t.Fatalf("expected %d, got %d", CodeInvalidParams, code)
	}
}

func TestParseErrorAndBlankLines(t *testing.T) {
	resp := run(t, testServer(), "", `not-json`, `{"jsonrpc":"2.0","id":9,"method":"ping"}`)
	if len(resp) != 2 {
		t.Fatalf("blank line must be skipped, got %d responses", len(resp))
	}
	if code := errCode(resp[0]); code != CodeParseError {
		t.Fatalf("expected parse error, got %d", code)
	}
	if resp[1]["result"] == nil {
		t.Fatalf("ping must return a result: %+v", resp[1])
	}
}

// Client probes (resources/list, prompts/list) must answer "none", not an
// error: otherwise every host has to special-case us.
func TestEmptyResourceAndPromptLists(t *testing.T) {
	resp := run(t, testServer(),
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`)
	for i, key := range []string{"resources", "prompts"} {
		result, _ := resp[i]["result"].(map[string]any)
		if _, ok := result[key].([]any); !ok {
			t.Fatalf("%s must be an empty array: %+v", key, resp[i])
		}
	}
}

func errCode(resp map[string]any) int {
	e, _ := resp["error"].(map[string]any)
	code, _ := e["code"].(float64)
	return int(code)
}
