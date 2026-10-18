package ai

import "context"

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
