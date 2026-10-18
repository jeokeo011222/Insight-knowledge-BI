package ai

import (
	"testing"

	"go.uber.org/zap"

	"superset/internal/ai/registry"
	"superset/internal/config"
)

// TestEveryRegisteredToolIsWired guards the gap that made agents look
// "one-tool only": a tool can be registered (and therefore selectable in the
// agent editor) while having no executor, in which case every call returns
// "not implemented". Registering a tool without wiring it must fail loudly
// here instead of silently degrading at run time.
func TestEveryRegisteredToolIsWired(t *testing.T) {
	svc := &Service{
		Config: &config.Config{},
		Log:    zap.NewNop(),
		Skills: registry.NewSkillRegistry(),
		Tools:  registry.NewToolRegistry(),
	}
	registerBuiltins(svc)

	for _, tool := range svc.Tools.List() {
		if tool.Execute == nil {
			t.Errorf("tool %q is registered but has no executor", tool.Name)
		}
	}
	// Every skill must be wired too: a registered-but-unimplemented skill is
	// offered in the agent editor and then fails on every call, which is the
	// "capability with no substance" gap this guards against.
	for _, skill := range svc.Skills.List() {
		wired := skill.Executor != nil
		if !wired {
			t.Errorf("skill %q is registered but has no executor", skill.Name)
		}
		if public := skill.Public(); public.Available != wired {
			t.Errorf("skill %q availability=%v, want %v", skill.Name, public.Available, wired)
		}
	}
}

// TestPublicCapabilitiesReportAvailability makes sure the editor can tell a
// wired capability from an unimplemented one.
func TestPublicCapabilitiesReportAvailability(t *testing.T) {
	svc := &Service{
		Config: &config.Config{},
		Log:    zap.NewNop(),
		Skills: registry.NewSkillRegistry(),
		Tools:  registry.NewToolRegistry(),
	}
	registerBuiltins(svc)

	for _, tool := range svc.Tools.PublicList() {
		if !tool.Available {
			t.Errorf("tool %q reported unavailable after wiring", tool.Name)
		}
	}
}
