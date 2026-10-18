// Package registry defines the Skill and Tool contracts plus their in-memory
// registries. It is the lowest layer of the AI Agent plane: the Agent runtime,
// the built-in capabilities and the frontend capability menu all share these
// self-describing descriptors.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// ErrNotImplemented marks a capability whose executor is not wired yet.
var ErrNotImplemented = errors.New("capability not implemented")

// Permission is an (action, resource) capability required to run a skill/tool.
type Permission struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
}

// CostHint estimates the cost profile of a capability for the planner.
type CostHint struct {
	Tier      string `json:"tier"` // cheap|standard|heavy
	Tokens    int    `json:"tokens"`
	LatencyMS int    `json:"latency_ms"`
}

// AIContext carries the caller's UI context into a run.
type AIContext struct {
	Scope       string           `json:"scope,omitempty"`
	DashboardID *int             `json:"dashboard_id,omitempty"`
	ChartID     *int             `json:"chart_id,omitempty"`
	DatasetID   *int             `json:"dataset_id,omitempty"`
	Language    string           `json:"locale,omitempty"`
	TimeRange   string           `json:"time_range,omitempty"`
	Filters     []map[string]any `json:"filters,omitempty"`
	Extra       map[string]any   `json:"extra,omitempty"`
}

// Invocation is the input handed to a skill executor.
type Invocation struct {
	RunID   int             `json:"run_id"`
	UserID  int             `json:"user_id"`
	Skill   string          `json:"skill"`
	Args    json.RawMessage `json:"args"`
	Context AIContext       `json:"context"`
}

// Result is what a skill returns to the Agent.
type Result struct {
	Output     any            `json:"output,omitempty"`
	Markdown   string         `json:"markdown,omitempty"`
	Artifacts  map[string]any `json:"artifacts,omitempty"`
	TokenUsage int            `json:"token_usage,omitempty"`
}

// SkillExecutor runs a skill. It receives the registry ToolRuntime so it can
// invoke tools; it may be nil for a registered-but-unwired skill.
type SkillExecutor func(ctx context.Context, in *Invocation, rt *ToolRuntime) (*Result, error)

// Skill is a self-describing capability unit shared by the Agent and the UI.
type Skill struct {
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Domain       string          `json:"domain"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Permissions  []Permission    `json:"permissions,omitempty"`
	CostHint     CostHint        `json:"cost_hint"`
	// Executor is intentionally excluded from the JSON contract (see DTO).
	Executor SkillExecutor `json:"-"`
}

// PublicSkill strips the executor so the contract can be served to the UI.
type PublicSkill struct {
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Domain       string          `json:"domain"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Permissions  []Permission    `json:"permissions,omitempty"`
	CostHint     CostHint        `json:"cost_hint"`
	// Available mirrors PublicTool.Available: false means "registered but the
	// executor is not wired yet".
	Available bool `json:"available"`
}

// Public exposes the serializable part of the skill.
func (s *Skill) Public() PublicSkill {
	return PublicSkill{
		Name: s.Name, Title: s.Title, Description: s.Description, Domain: s.Domain,
		InputSchema: s.InputSchema, OutputSchema: s.OutputSchema,
		Permissions: s.Permissions, CostHint: s.CostHint,
		Available: s.Executor != nil,
	}
}

// SkillRegistry stores skill definitions. Reads are safe for concurrent use.
type SkillRegistry struct {
	mu     sync.RWMutex
	skills map[string]*Skill
}

// NewSkillRegistry returns an empty registry.
func NewSkillRegistry() *SkillRegistry {
	return &SkillRegistry{skills: make(map[string]*Skill)}
}

// Register adds or replaces a skill.
func (r *SkillRegistry) Register(s *Skill) {
	if s == nil || s.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[s.Name] = s
}

// Get returns a skill by name.
func (r *SkillRegistry) Get(name string) (*Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// Remove drops a skill. It reports whether anything was deleted, so callers can
// tell "already gone" from "not registered".
func (r *SkillRegistry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.skills[name]; !ok {
		return false
	}
	delete(r.skills, name)
	return true
}

// List returns all skills ordered by name.
func (r *SkillRegistry) List() []*Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PublicList returns the serializable descriptors ordered by name.
func (r *SkillRegistry) PublicList() []PublicSkill {
	all := r.List()
	out := make([]PublicSkill, 0, len(all))
	for _, s := range all {
		out = append(out, s.Public())
	}
	return out
}
