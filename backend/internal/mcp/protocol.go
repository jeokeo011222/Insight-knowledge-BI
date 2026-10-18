package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// protocol.go is a small, dependency-free MCP server loop.
//
// Why not an SDK: MCP over stdio is JSON-RPC 2.0 — one message per line — and
// the surface a read-only BI bridge needs is four methods. Pulling a framework
// in would add a transitive dependency to the main binary for ~150 lines of
// framing. The trade-off is that unsupported methods must answer with a proper
// JSON-RPC error instead of pretending, which is what this file does.

// ProtocolVersion is used when the client does not name one.
const ProtocolVersion = "2025-06-18"

// Tool is one callable capability.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	// Handler receives the raw arguments object. A returned error becomes a
	// tool result with isError=true (the model is expected to read it and
	// correct itself), not a JSON-RPC protocol error.
	Handler func(ctx context.Context, args json.RawMessage) (any, error)
}

// Server is an MCP server bound to a set of tools.
type Server struct {
	Name    string
	Version string
	Tools   []Tool
	// OnError is called for protocol-level failures; nil means silent.
	OnError func(format string, args ...any)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
)

// Serve reads newline-delimited JSON-RPC from in until EOF and writes responses
// to out. A write error ends the loop; a malformed line does not — a client
// that sends one bad message should get one error, not be disconnected.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReaderSize(in, 1<<20)
	writer := bufio.NewWriter(out)
	var mu sync.Mutex

	write := func(resp rpcResponse) error {
		blob, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		if _, err := writer.Write(blob); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
		return writer.Flush()
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		line, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if uerr := json.Unmarshal(line, &req); uerr != nil {
			if werr := write(rpcResponse{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: CodeParseError, Message: "invalid JSON: " + uerr.Error()},
			}); werr != nil {
				return werr
			}
			continue
		}
		// A message without an id is a notification (e.g. notifications/
		// initialised). JSON-RPC forbids replying to it, and clients treat a
		// reply as a protocol violation.
		if len(req.ID) == 0 {
			continue
		}
		resp := s.dispatch(ctx, &req)
		if err := write(resp); err != nil {
			return err
		}
	}
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := trimSpaceBytes(line)
			if len(trimmed) > 0 {
				return trimmed, nil
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

func trimSpaceBytes(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r' || b[start] == '\n') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r' || b[end-1] == '\n') {
		end--
	}
	return b[start:end]
}

func (s *Server) dispatch(ctx context.Context, req *rpcRequest) rpcResponse {
	ok := func(result any) rpcResponse {
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	}
	fail := func(code int, msg string) rpcResponse {
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: msg}}
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := p.ProtocolVersion
		if version == "" {
			version = ProtocolVersion
		}
		return ok(map[string]any{
			"protocolVersion": version,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{"name": s.Name, "version": s.Version},
			"instructions": "Read-only BI bridge. Start with list_datasets, then describe_dataset, " +
				"then dry_plan before semantic_query or run_sql. Every call runs with the caller's " +
				"permissions: row-level security and column masking still apply.",
		})
	case "ping":
		return ok(map[string]any{})
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		for _, t := range s.Tools {
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		return ok(map[string]any{"tools": tools})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return fail(CodeInvalidParams, "invalid params: "+err.Error())
		}
		for _, t := range s.Tools {
			if t.Name != p.Name {
				continue
			}
			return ok(s.callTool(ctx, t, p.Arguments))
		}
		return fail(CodeInvalidParams, fmt.Sprintf("unknown tool %q", p.Name))
	case "resources/list":
		// Advertised as empty rather than unsupported: a client that probes
		// resources should see "none", not an error it has to special-case.
		return ok(map[string]any{"resources": []any{}})
	case "prompts/list":
		return ok(map[string]any{"prompts": []any{}})
	}
	return fail(CodeMethodNotFound, "method not found: "+req.Method)
}

// callTool runs a handler and shapes the result the way MCP expects: a content
// array plus the machine-readable half in structuredContent.
func (s *Server) callTool(ctx context.Context, t Tool, raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	out, err := t.Handler(ctx, raw)
	if err != nil {
		return map[string]any{
			"content":           []map[string]any{{"type": "text", "text": err.Error()}},
			"isError":           true,
			"structuredContent": map[string]any{"error": err.Error()},
		}
	}
	text, merr := json.MarshalIndent(out, "", "  ")
	if merr != nil {
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": "failed to encode result: " + merr.Error()}},
			"isError": true,
		}
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"isError":           false,
		"structuredContent": out,
	}
}
