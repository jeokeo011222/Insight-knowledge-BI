package registry

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// SideEffect classifies a tool's blast radius.
type SideEffect int

const (
	// Read is a pure read (query/schema/metadata).
	Read SideEffect = iota
	// Write mutates data but is reversible via snapshot.
	Write
	// Destructive mutates data irreversibly.
	Destructive
)

// String renders the canonical lowercase form persisted in ai_tool_call_log.
func (s SideEffect) String() string {
	switch s {
	case Write:
		return "write"
	case Destructive:
		return "destructive"
	default:
		return "read"
	}
}

// ToolResult is what a tool returns.
type ToolResult struct {
	Output     any            `json:"output,omitempty"`
	SideEffect SideEffect     `json:"-"`
	TargetIDs  []int          `json:"target_ids,omitempty"`
	Snapshot   any            `json:"snapshot,omitempty"`
	Artifacts  map[string]any `json:"artifacts,omitempty"`
	DurationMS int            `json:"duration_ms"`
}

// ToolExecutor runs a tool against raw JSON arguments.
type ToolExecutor func(ctx context.Context, in json.RawMessage) (*ToolResult, error)

// Tool is a data read/write capability exposed to the Agent.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	SideEffect  SideEffect      `json:"side_effect"`
	Permissions []Permission    `json:"permissions,omitempty"`
	// Execute is intentionally excluded from the JSON contract.
	Execute ToolExecutor `json:"-"`
}

// PublicTool strips the executor so the contract can be served to the UI.
type PublicTool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	SideEffect  string          `json:"side_effect"`
	Permissions []Permission    `json:"permissions,omitempty"`
	// Available reports whether an executor is wired. The editor greys out
	// registered-but-unimplemented tools instead of letting an operator bind a
	// capability that would only ever return "not implemented" at run time.
	Available bool `json:"available"`
}

// Public exposes the serializable part of the tool.
func (t *Tool) Public() PublicTool {
	return PublicTool{
		Name: t.Name, Title: t.Title, Description: t.Description,
		InputSchema: t.InputSchema, SideEffect: t.SideEffect.String(),
		Permissions: t.Permissions, Available: t.Execute != nil,
	}
}

// ToolRuntime is handed to skill executors so they can call tools. Tool calls
// made through InvokeTool flow through the Agent's permission/audit guard.
type ToolRuntime struct {
	UserID     int
	RunID      int
	Tools      *ToolRegistry
	InvokeTool func(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error)
}

// ToolRegistry stores tool definitions.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
}

// NewToolRegistry returns an empty registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]*Tool)}
}

// Register adds or replaces a tool.
func (r *ToolRegistry) Register(t *Tool) {
	if t == nil || t.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name] = t
}

// Get returns a tool by name.
func (r *ToolRegistry) Get(name string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Remove drops a tool. It reports whether anything was deleted.
func (r *ToolRegistry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return false
	}
	delete(r.tools, name)
	return true
}

// List returns all tools ordered by name.
func (r *ToolRegistry) List() []*Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PublicList returns the serializable descriptors ordered by name.
func (r *ToolRegistry) PublicList() []PublicTool {
	all := r.List()
	out := make([]PublicTool, 0, len(all))
	for _, t := range all {
		out = append(out, t.Public())
	}
	return out
}
