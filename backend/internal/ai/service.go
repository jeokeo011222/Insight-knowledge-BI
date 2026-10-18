// Package ai implements the 智观 BI data-analysis Agent plane: a reasoning
// runtime plus a pluggable registry of skills (capabilities) and tools (data
// read/write accessors). It is wired onto /api/v1/ai/* and is fully isolated
// from the existing query and security logic, which it reuses rather than
// modifies.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/ai/registry"
	"superset/internal/config"
	"superset/internal/pkg/cache"
	"superset/internal/queries"
	"superset/internal/security"
)

// Invocation guard errors.
var (
	// ErrPermissionDenied is returned when the acting user lacks a capability's
	// declared permissions, or a write tool is disabled by configuration.
	ErrPermissionDenied = errors.New("permission denied")
	// ErrUnknownCapability is returned for a skill/tool name not in the registry.
	ErrUnknownCapability = errors.New("unknown capability")
)

// Service is the AI Agent plane root. It owns the capability registries and the
// model gateway; the Agent runtime (P1) and skills/tools (P2+) hang off it.
type Service struct {
	Config *config.Config
	Log    *zap.Logger
	DB     *gorm.DB
	Cache  *cache.Client
	Mgr    *security.Manager

	Skills *registry.SkillRegistry
	Tools  *registry.ToolRegistry
	LLM    *LLMGateway
	Audit  *Audit
	// settings carries the runtime-editable Agent/LLM configuration shared with
	// the gateway; see Settings() / UpdateSettings().
	settings *settingsStore
	// Queries is the reused query engine: the Agent's query_executor runs here
	// so取数 inherits the engine's RBAC, RLS, limits and auditing.
	Queries *queries.Executor
	// plugins holds the settings-page overlay on top of the compiled-in
	// registries: custom (declarative) capabilities and the operator's
	// disable switches.
	plugins *pluginState
}

// NewService builds the AI plane and registers the built-in capabilities.
func NewService(cfg *config.Config, log *zap.Logger, db *gorm.DB, c *cache.Client, mgr *security.Manager) *Service {
	s := &Service{
		Config: cfg,
		Log:    log,
		DB:     db,
		Cache:  c,
		Mgr:    mgr,
		Skills: registry.NewSkillRegistry(),
		Tools:  registry.NewToolRegistry(),
		// The plugin overlay must exist before registration so a capability
		// seeded from the DB can be applied to a live registry later.
		plugins: newPluginState(),
	}
	// Runtime settings start from the deploy-time config; a persisted override is
	// applied best-effort so a broken row never blocks startup.
	st := settingsFromConfig(cfg)
	if persisted, found, err := loadSettings(context.Background(), db); err != nil {
		log.Warn("failed to load ai settings, using config defaults", zap.Error(err))
	} else if found {
		st = persisted
	}
	s.settings = newSettingsStore(st)
	s.LLM = NewLLMGateway(cfg, s.settings, log, c)
	s.Audit = NewAudit(db, log)
	s.Queries = queries.NewFromConfig(db, log, c, mgr, cfg)
	if cfg != nil && cfg.AIQueryTimeoutSec > 0 {
		s.Queries.QueryTimeout = time.Duration(cfg.AIQueryTimeoutSec) * time.Second
	}
	if cfg != nil && cfg.AIExplainMaxRows > 0 {
		s.Queries.ExplainMaxRows = cfg.AIExplainMaxRows
	}
	registerBuiltins(s)
	// Seed the built-in definitions into the plugin table (so the settings page
	// can list and disable them) and apply any custom rows. Best effort: a
	// database hiccup must never stop the API from booting.
	if err := s.seedPlugins(context.Background()); err != nil {
		log.Warn("failed to seed ai capability plugins", zap.Error(err))
	}
	if err := s.syncPlugins(context.Background()); err != nil {
		log.Warn("failed to apply ai capability plugins", zap.Error(err))
	}
	// Ship the built-in agents. Seeded after the plugins so their skill/tool
	// allow-lists refer to rows that already exist. Also best effort.
	if err := s.seedBuiltinAgents(context.Background()); err != nil {
		log.Warn("failed to seed built-in agents", zap.Error(err))
	}
	return s
}

// InvokeSkill runs a registered skill as userID after checking its declared
// permissions. A skill whose executor is not wired yet (P2+) returns
// registry.ErrNotImplemented so the Agent can surface it as an observation.
func (s *Service) InvokeSkill(ctx context.Context, userID, runID int, name string, args json.RawMessage, actx registry.AIContext) (*registry.Result, error) {
	sk, ok := s.Skills.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: skill %s", ErrUnknownCapability, name)
	}
	if !s.skillVisible(name) {
		return nil, fmt.Errorf("%w: skill %s is disabled", ErrPermissionDenied, name)
	}
	if sc := runScopeFrom(ctx); !sc.allowsSkill(name) {
		return nil, fmt.Errorf("%w: skill %s is not enabled for this agent", ErrPermissionDenied, name)
	}
	if !s.allowed(ctx, userID, sk.Permissions) {
		return nil, fmt.Errorf("%w: skill %s", ErrPermissionDenied, name)
	}
	if sk.Executor == nil {
		return nil, fmt.Errorf("%w: skill %s", registry.ErrNotImplemented, name)
	}
	rt := &registry.ToolRuntime{
		UserID: userID,
		RunID:  runID,
		Tools:  s.Tools,
		InvokeTool: func(c context.Context, n string, a json.RawMessage) (*registry.ToolResult, error) {
			return s.InvokeTool(c, userID, runID, n, a)
		},
	}
	in := &registry.Invocation{RunID: runID, UserID: userID, Skill: name, Args: args, Context: actx}
	return sk.Executor(ctx, in, rt)
}

// InvokeTool runs a registered tool as userID after checking its declared
// permissions, the agent's allow-list and, for non-read tools, the write switch.
func (s *Service) InvokeTool(ctx context.Context, userID, runID int, name string, args json.RawMessage) (*registry.ToolResult, error) {
	t, ok := s.Tools.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: tool %s", ErrUnknownCapability, name)
	}
	if !s.toolVisible(name) {
		return nil, fmt.Errorf("%w: tool %s is disabled", ErrPermissionDenied, name)
	}
	if sc := runScopeFrom(ctx); !sc.allowsTool(name) {
		return nil, fmt.Errorf("%w: tool %s is not enabled for this agent", ErrPermissionDenied, name)
	}
	if !s.allowed(ctx, userID, t.Permissions) {
		return nil, fmt.Errorf("%w: tool %s", ErrPermissionDenied, name)
	}
	if t.SideEffect != registry.Read && !writeToolsAllowed(ctx) {
		return nil, fmt.Errorf("%w: write tools disabled", ErrPermissionDenied)
	}
	if t.Execute == nil {
		return nil, fmt.Errorf("%w: tool %s", registry.ErrNotImplemented, name)
	}
	// Tool executors have no user id parameter; attach it so取数 and auditing
	// act as the caller.
	return t.Execute(registry.WithUserID(ctx, userID), args)
}

// allowed reports whether userID holds every declared permission. A capability
// declaring no permissions is unrestricted; a missing manager denies.
func (s *Service) allowed(ctx context.Context, userID int, perms []registry.Permission) bool {
	if len(perms) == 0 {
		return true
	}
	if s.Mgr == nil {
		return false
	}
	for _, p := range perms {
		if !s.Mgr.HasPermission(ctx, userID, p.Action, p.Resource) {
			return false
		}
	}
	return true
}

// Enabled reports whether the AI Agent plane is switched on.
func (s *Service) Enabled() bool { return s.Settings().Enabled }

// Settings returns the live Agent/LLM configuration.
func (s *Service) Settings() *Settings { return s.settings.Load() }

// UpdateSettings persists the configuration and applies it immediately: the
// gateway and the runtime read through the same store, so no restart is needed.
func (s *Service) UpdateSettings(ctx context.Context, next *Settings) error {
	if err := saveSettings(ctx, s.DB, next); err != nil {
		return err
	}
	s.settings.Store(next)
	return nil
}

// Features exposes the capability switches to the frontend.
type Features struct {
	Agent                bool `json:"agent"`
	DomainSkills         bool `json:"domain_skills"`
	WriteTools           bool `json:"write_tools"`
	WriteConfirmRequired bool `json:"write_confirm_required"`
}

// Capabilities is the /agent/skills response payload (the public contract).
type Capabilities struct {
	Skills   []registry.PublicSkill `json:"skills"`
	Tools    []registry.PublicTool  `json:"tools"`
	Features Features               `json:"features"`
}

// Capabilities returns the current user-facing capability menu. The write
// switches reflect the default agent, since the menu describes what that agent
// would be allowed to do.
func (s *Service) Capabilities(ctx context.Context) *Capabilities {
	agent, err := s.ResolveAgent(ctx, 0)
	if err != nil || agent == nil {
		agent = s.fallbackAgent()
	}
	skills := make([]registry.PublicSkill, 0, len(s.Skills.List()))
	for _, sk := range s.Skills.List() {
		if s.skillVisible(sk.Name) {
			skills = append(skills, sk.Public())
		}
	}
	return &Capabilities{
		Skills: skills,
		Tools:  s.visibleTools(),
		Features: Features{
			Agent:                s.Config.FeatureEnabled("AI_AGENT"),
			DomainSkills:         s.domainSkillsEnabled(),
			WriteTools:           agent.WriteToolsEnabled && s.Config.FeatureEnabled("AI_TOOLS_WRITE"),
			WriteConfirmRequired: agent.WriteConfirmRequired,
		},
	}
}

// Catalog is the serialisable skill/tool catalogue served to the agent editor
// (it needs no DB access, so it stays reachable while the plane is switched off).
type Catalog struct {
	Skills []registry.PublicSkill `json:"skills"`
	Tools  []registry.PublicTool  `json:"tools"`
}

// Catalog returns every registered capability for the plugin-style agent form.
func (s *Service) Catalog() *Catalog {
	skills := make([]registry.PublicSkill, 0, len(s.Skills.List()))
	for _, sk := range s.Skills.List() {
		if s.skillVisible(sk.Name) {
			skills = append(skills, sk.Public())
		}
	}
	return &Catalog{Skills: skills, Tools: s.visibleTools()}
}

// visibleTools lists the tools the settings page may offer: a tool switched off
// in the plugin table is hidden from the catalog (it would be rejected at run
// time anyway).
func (s *Service) visibleTools() []registry.PublicTool {
	all := s.Tools.List()
	out := make([]registry.PublicTool, 0, len(all))
	for _, t := range all {
		if s.toolVisible(t.Name) {
			out = append(out, t.Public())
		}
	}
	return out
}

// domainSkillNames are the professional-analysis skills gated by AI_SKILL_DOMAIN.
var domainSkillNames = map[string]bool{
	"metric_compute": true,
	"dupont":         true,
	"trend":          true,
	"structure":      true,
	"yoy_mom":        true,
}

// domainSkillsEnabled reports whether the domain-analysis skill family is on.
func (s *Service) domainSkillsEnabled() bool { return s.Config.FeatureEnabled("AI_SKILL_DOMAIN") }

// skillVisible reports whether a skill should be exposed and invocable.
func (s *Service) skillVisible(name string) bool {
	if domainSkillNames[name] && !s.domainSkillsEnabled() {
		return false
	}
	// A built-in switched off on the settings page must stop being advertised
	// and invocable, otherwise the switch would only be cosmetic.
	return s.skillEnabledByPlugin(name)
}

// toolVisible reports whether a tool is switched on in the plugin table.
func (s *Service) toolVisible(name string) bool { return s.toolEnabledByPlugin(name) }

// ToolDeclarations renders the capabilities an agent may call as
// OpenAI-compatible function declarations for the Agent's function-calling loop.
// A non-nil scope narrows the menu to that agent's allow-list.
func (s *Service) ToolDeclarations(scope *runScope) []ChatTool {
	var out []ChatTool
	for _, sk := range s.Skills.List() {
		if !s.skillVisible(sk.Name) || !scope.allowsSkill(sk.Name) {
			continue
		}
		out = append(out, ChatTool{Type: "function", Function: FunctionDefinition{
			Name:        sk.Name,
			Description: sk.Description,
			Parameters:  sk.InputSchema,
		}})
	}
	for _, t := range s.Tools.List() {
		if !scope.allowsTool(t.Name) || !s.toolVisible(t.Name) {
			continue
		}
		out = append(out, ChatTool{Type: "function", Function: FunctionDefinition{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		}})
	}
	return out
}
