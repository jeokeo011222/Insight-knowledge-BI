package ai

import (
	"context"
	"strings"
)

// runScope is the resolved agent policy for a single run. It is attached to the
// run's context so capability gates (which skills/tools an agent may use, and
// its write switches) can be enforced inside executors without threading the
// agent through every signature. A nil scope means "no agent policy": every
// capability is allowed and write switches fall back to their defaults.
type runScope struct {
	AgentID int
	// skills/tools are the agent's allow-lists; nil means "all registered".
	skills map[string]struct{}
	tools  map[string]struct{}
	// Provider is the resolved endpoint for this run, reused by skill helper
	// calls so they hit the same model the agent is bound to.
	Provider *ProviderConfig
	// Tier selects the cheaper/faster model when the question looks trivial.
	// "fast" = simple reads (single metric, top-N, rank, yoy); "default" = full
	// analysis (attribution, trend decomposition, writes).
	Tier string

	WriteToolsEnabled    bool
	WriteConfirmRequired bool
}

// allowsSkill reports whether the agent may invoke a skill.
func (s *runScope) allowsSkill(name string) bool {
	if s == nil || s.skills == nil {
		return true
	}
	_, ok := s.skills[name]
	return ok
}

// allowsTool reports whether the agent may invoke a tool.
func (s *runScope) allowsTool(name string) bool {
	if s == nil || s.tools == nil {
		return true
	}
	_, ok := s.tools[name]
	return ok
}

type runScopeKey struct{}

// withRunScope attaches an agent policy to a context.
func withRunScope(ctx context.Context, scope *runScope) context.Context {
	if scope == nil {
		return ctx
	}
	return context.WithValue(ctx, runScopeKey{}, scope)
}

// runScopeFrom returns the agent policy attached to ctx, or nil when the call
// runs outside an agent run.
func runScopeFrom(ctx context.Context) *runScope {
	if sc, ok := ctx.Value(runScopeKey{}).(*runScope); ok {
		return sc
	}
	return nil
}

// writeToolsAllowed reports whether the current run may use write tools. A call
// outside any run defaults to false (fail-closed).
func writeToolsAllowed(ctx context.Context) bool {
	sc := runScopeFrom(ctx)
	return sc != nil && sc.WriteToolsEnabled
}

// writeConfirmRequired reports whether the current run requires human
// confirmation before a write; false outside a run.
func writeConfirmRequired(ctx context.Context) bool {
	sc := runScopeFrom(ctx)
	return sc != nil && sc.WriteConfirmRequired
}

// routeIntent picks "fast" or "default" based on the natural-language question.
// It is intentionally cheap (no LLM call) so it does not add latency. A keyword
// match for analysis-heavy intents (attribution, trend, structure) forces the
// expensive model; otherwise single-metric reads stay on the cheap tier when a
// FastModel is configured.
func routeIntent(question string, writeEnabled bool) string {
	if writeEnabled {
		return "default" // writes always use the full model — correctness > cost.
	}
	q := strings.ToLower(question)
	for _, kw := range []string{
		"归因", "驱动", "拆解", "结构", "dupont", "structure", "attribution",
		"趋势", "拐点", "波动", "trend", "decomposition",
		"异常", "anomaly", "outlier",
		"为什么", "why", "root cause",
		"对比", "对比分析", "compare",
	} {
		if strings.Contains(q, kw) {
			return "default"
		}
	}
	// Single-metric / rank / yoy reads are the happy path for fast models.
	return "fast"
}

// pickProviderModel returns either the default or the fast model, depending on
// the scope tier and whether a FastModel is configured. Falls back to pc.Model
// in all "no tier splitting" cases.
func pickProviderModel(pc *ProviderConfig, tier string) string {
	if pc == nil {
		return ""
	}
	if tier == "fast" && pc.FastModel != "" {
		return pc.FastModel
	}
	return pc.Model
}
