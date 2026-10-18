package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"superset/internal/models"
)

// ErrAgentBuiltin guards built-in agents: they ship with the product and are
// immutable — the API refuses to update or delete them so a shipped preset can
// never be silently renamed, re-scoped or removed.
var ErrAgentBuiltin = errors.New("内置智能体不可修改或删除")

// ProviderConfig is the resolved LLM endpoint the gateway calls. It is built
// from an ai_provider row, or — when no row is configured — from the deploy-time
// config, so an unconfigured deployment keeps working exactly as before.
type ProviderConfig struct {
	ID           int
	ProviderType string
	Name         string
	BaseURL      string
	Model        string
	APIKey       string
	TimeoutSec   int
}

// providerConfigOf maps a stored provider row onto the gateway's config view.
func providerConfigOf(p *models.AIProvider) *ProviderConfig {
	return &ProviderConfig{
		ID:           p.ID,
		ProviderType: p.ProviderType,
		Name:         p.Name,
		BaseURL:      p.BaseURL,
		Model:        p.Model,
		APIKey:       p.APIKey,
		TimeoutSec:   p.TimeoutSec,
	}
}

// fallbackProvider is the config-derived endpoint used when no provider row is
// configured or an agent is not bound to one.
func (s *Service) fallbackProvider() *ProviderConfig {
	return &ProviderConfig{
		ProviderType: orDefaultStr(s.Config.AILLMProvider, "openai_compatible"),
		Name:         "系统默认",
		BaseURL:      s.Config.AILLMBaseURL,
		Model:        s.Config.AILLMModel,
		APIKey:       s.Config.AILLMAPIKey,
		TimeoutSec:   s.Config.AILLMTimeoutSec,
	}
}

// fallbackAgent is the config-derived agent used when the ai_agent table is
// empty, preserving the pre-refactor behaviour (all capabilities, config
// guardrails) for existing deployments.
func (s *Service) fallbackAgent() *models.AIAgent {
	return &models.AIAgent{
		Name:                 "系统默认智能体",
		Description:          "由部署配置生成；在页面上创建智能体后将被替代。",
		Enabled:              true,
		MaxSteps:             s.Config.AIAgentMaxSteps,
		MaxToolCalls:         s.Config.AIAgentMaxToolCalls,
		TokenBudget:          s.Config.AIAgentTokenBudget,
		RunTimeoutSec:        s.Config.AIAgentRunTimeoutSec,
		WriteToolsEnabled:    s.Config.AIWriteToolsEnabled,
		WriteConfirmRequired: s.Config.AIWriteConfirmRequired,
	}
}

func orDefaultStr(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

/* --------------------------------------------------------------- providers */

// ListProviders returns every configured LLM endpoint, newest first.
func (s *Service) ListProviders(ctx context.Context) ([]models.AIProvider, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	var rows []models.AIProvider
	if err := s.DB.WithContext(ctx).Order("id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetProvider loads one provider.
func (s *Service) GetProvider(ctx context.Context, id int) (*models.AIProvider, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	var row models.AIProvider
	if err := s.DB.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateProvider inserts a new endpoint.
func (s *Service) CreateProvider(ctx context.Context, p *models.AIProvider) error {
	if s.DB == nil {
		return errors.New("metadata store is unavailable")
	}
	return s.DB.WithContext(ctx).Create(p).Error
}

// UpdateProvider persists the editable fields of an existing endpoint.
func (s *Service) UpdateProvider(ctx context.Context, p *models.AIProvider) error {
	if s.DB == nil {
		return errors.New("metadata store is unavailable")
	}
	return s.DB.WithContext(ctx).Model(&models.AIProvider{}).Where("id = ?", p.ID).
		Updates(map[string]any{
			"name":          p.Name,
			"provider_type": p.ProviderType,
			"base_url":      p.BaseURL,
			"model":         p.Model,
			"api_key":       p.APIKey,
			"timeout_sec":   p.TimeoutSec,
			"enabled":       p.Enabled,
			"description":   p.Description,
		}).Error
}

// DeleteProvider removes an endpoint, refusing while any agent still binds it
// so a run can never resolve to a dangling provider.
func (s *Service) DeleteProvider(ctx context.Context, id int) error {
	if s.DB == nil {
		return errors.New("metadata store is unavailable")
	}
	var count int64
	if err := s.DB.WithContext(ctx).Model(&models.AIAgent{}).Where("provider_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: 有 %d 个智能体正在使用该供应商，请先解除绑定", ErrProviderInUse, count)
	}
	return s.DB.WithContext(ctx).Delete(&models.AIProvider{}, id).Error
}

// ErrProviderInUse is returned when deleting a provider still bound by agents.
var ErrProviderInUse = errors.New("provider is in use")

/* ------------------------------------------------------------------ agents */

// ListAgents returns every configured agent, default first.
func (s *Service) ListAgents(ctx context.Context) ([]models.AIAgent, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	var rows []models.AIAgent
	if err := s.DB.WithContext(ctx).Order("is_default desc, id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetAgent loads one agent.
func (s *Service) GetAgent(ctx context.Context, id int) (*models.AIAgent, error) {
	if s.DB == nil {
		return nil, errors.New("metadata store is unavailable")
	}
	var row models.AIAgent
	if err := s.DB.WithContext(ctx).First(&row, id).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// CreateAgent inserts a new agent and keeps the default flag unique.
func (s *Service) CreateAgent(ctx context.Context, a *models.AIAgent) error {
	if s.DB == nil {
		return errors.New("metadata store is unavailable")
	}
	if err := s.DB.WithContext(ctx).Create(a).Error; err != nil {
		return err
	}
	if a.IsDefault {
		return s.clearOtherDefaults(ctx, a.ID)
	}
	return nil
}

// UpdateAgent persists the editable fields of an existing agent. Built-ins are
// rejected: their definition is code-owned and must stay identical to the
// shipped preset.
func (s *Service) UpdateAgent(ctx context.Context, a *models.AIAgent) error {
	if s.DB == nil {
		return errors.New("metadata store is unavailable")
	}
	if err := s.rejectBuiltinAgent(ctx, a.ID); err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Model(&models.AIAgent{}).Where("id = ?", a.ID).
		Updates(map[string]any{
			"name":                   a.Name,
			"description":            a.Description,
			"provider_id":            a.ProviderID,
			"is_default":             a.IsDefault,
			"enabled":                a.Enabled,
			"skill_codes":            a.SkillCodes,
			"tool_codes":             a.ToolCodes,
			"max_steps":              a.MaxSteps,
			"max_tool_calls":         a.MaxToolCalls,
			"token_budget":           a.TokenBudget,
			"run_timeout_sec":        a.RunTimeoutSec,
			"write_tools_enabled":    a.WriteToolsEnabled,
			"write_confirm_required": a.WriteConfirmRequired,
		}).Error; err != nil {
		return err
	}
	if a.IsDefault {
		return s.clearOtherDefaults(ctx, a.ID)
	}
	return nil
}

// DeleteAgent removes an agent; the default resolver then falls back to another
// enabled agent (or the config-derived one).
func (s *Service) DeleteAgent(ctx context.Context, id int) error {
	if s.DB == nil {
		return errors.New("metadata store is unavailable")
	}
	if err := s.rejectBuiltinAgent(ctx, id); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Delete(&models.AIAgent{}, id).Error
}

// rejectBuiltinAgent refuses to mutate a built-in agent. It reports
// ErrAgentBuiltin both for an explicitly built-in row and for the config-derived
// fallback agent (id 0), which has no row and must not be edited into one.
func (s *Service) rejectBuiltinAgent(ctx context.Context, id int) error {
	if id == 0 {
		return ErrAgentBuiltin
	}
	var row models.AIAgent
	if err := s.DB.WithContext(ctx).Select("is_builtin").First(&row, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// A missing row is not a built-in; the caller's own lookup/report
			// handles the 404 path.
			return nil
		}
		return err
	}
	if row.IsBuiltin {
		return ErrAgentBuiltin
	}
	return nil
}

// builtinAgentPreset is one shipped agent definition: a named capability bundle
// with a curated skill/tool allow-list and its own guardrails.
type builtinAgentPreset struct {
	Name         string
	Description  string
	SkillCodes   []string
	ToolCodes    []string
	WriteEnabled bool
	// Guardrails ship with the preset. The financial agent gets a roomier
	// allowance because a full 三大表 analysis needs several wide queries
	// (balance sheet + income statement + cash flow) inside one run.
	MaxSteps      int
	MaxToolCalls  int
	TokenBudget   int
	RunTimeoutSec int
}

// builtinAgentPresets are the agents shipped with the product. They are seeded
// on startup and kept in sync with this list, so a preset change reaches
// existing deployments; they stay immutable through the API.
func builtinAgentPresets() []builtinAgentPreset {
	return []builtinAgentPreset{
		{
			Name:        "通用数据分析智能体",
			Description: "面向任意数据集的问数、取数与洞察分析；未指定数据集时先确认分析目标。",
			SkillCodes:  []string{"nl2sql", "sql_explain", "insight", "metric_compute", "chart_design", "trend"},
			ToolCodes: []string{
				"schema_reader", "query_executor", "semantic_reader", "metric_dictionary",
				"result_sampler", "meta_search",
			},
			MaxSteps: 8, MaxToolCalls: 12, TokenBudget: 120000, RunTimeoutSec: 150,
		},
		{
			Name:        "财务杜邦分析智能体",
			Description: "面向资产负债、利润与现金流量表的杜邦分解、结构与同环比分析，输出结论与风险提示。",
			SkillCodes:  []string{"dupont", "structure", "yoy_mom", "trend", "insight", "nl2sql", "sql_explain"},
			ToolCodes: []string{
				"schema_reader", "query_executor", "metric_dictionary", "semantic_reader", "result_sampler",
			},
			// 三大表 can be wide and are submitted in one shot, so this preset
			// needs more tool calls and a larger budget than the generic agent.
			MaxSteps: 12, MaxToolCalls: 20, TokenBudget: 200000, RunTimeoutSec: 300,
		},
		{
			Name:        "看板编排智能体",
			Description: "依据分析结论生成图表与看板初稿（写操作，需全局开启写工具且支持按运行回滚）。",
			SkillCodes:  []string{"dashboard_compose", "chart_design", "nl2sql", "insight"},
			ToolCodes: []string{
				"chart_reader", "dashboard_reader", "chart_writer", "dashboard_writer",
				"dashboard_compose_writer", "schema_reader", "query_executor",
			},
			WriteEnabled: true,
			MaxSteps:     10, MaxToolCalls: 16, TokenBudget: 120000, RunTimeoutSec: 180,
		},
	}
}

// seedBuiltinAgents inserts the shipped agents and re-syncs the ones seeded by
// an older build, so a preset change (widened guardrails, new skill) actually
// reaches existing deployments. Because built-ins are code-owned and read-only
// through the API, only code-owned columns are written; the operator's default
// flag is left alone, and a same-named agent the operator created is never
// touched. Best effort: a database hiccup must never stop the API from booting.
func (s *Service) seedBuiltinAgents(ctx context.Context) error {
	if s.DB == nil {
		return nil
	}
	for _, preset := range builtinAgentPresets() {
		var row models.AIAgent
		err := s.DB.WithContext(ctx).Where("name = ?", preset.Name).First(&row).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			created := &models.AIAgent{
				Name: preset.Name, Description: preset.Description,
				ProviderID: 0, // system default, resolved per run
				SkillCodes: EncodeCodes(preset.SkillCodes), ToolCodes: EncodeCodes(preset.ToolCodes),
				MaxSteps: preset.MaxSteps, MaxToolCalls: preset.MaxToolCalls,
				TokenBudget: preset.TokenBudget, RunTimeoutSec: preset.RunTimeoutSec,
				WriteToolsEnabled: preset.WriteEnabled,
				Enabled:           true, IsBuiltin: true,
			}
			if cErr := s.DB.WithContext(ctx).Create(created).Error; cErr != nil {
				// A concurrent seed can lose the unique-index race; that is fine.
				continue
			}
		case err != nil:
			return err
		case !row.IsBuiltin:
			// The operator picked the same name: that row is theirs, leave it be.
		default:
			if uErr := s.DB.WithContext(ctx).Model(&models.AIAgent{}).Where("id = ?", row.ID).
				Updates(map[string]any{
					"description":         preset.Description,
					"provider_id":         0,
					"skill_codes":         EncodeCodes(preset.SkillCodes),
					"tool_codes":          EncodeCodes(preset.ToolCodes),
					"max_steps":           preset.MaxSteps,
					"max_tool_calls":      preset.MaxToolCalls,
					"token_budget":        preset.TokenBudget,
					"run_timeout_sec":     preset.RunTimeoutSec,
					"write_tools_enabled": preset.WriteEnabled,
					"enabled":             true,
				}).Error; uErr != nil {
				return uErr
			}
		}
	}
	return nil
}

func (s *Service) clearOtherDefaults(ctx context.Context, keepID int) error {
	return s.DB.WithContext(ctx).Model(&models.AIAgent{}).
		Where("id <> ? AND is_default = ?", keepID, true).
		Update("is_default", false).Error
}

// ResolveAgent returns the agent a run should use: the explicit id, else the
// default enabled agent, else the first enabled one, else — when the table is
// empty — the config-derived fallback.
func (s *Service) ResolveAgent(ctx context.Context, id int) (*models.AIAgent, error) {
	if s.DB == nil {
		return s.fallbackAgent(), nil
	}
	var row models.AIAgent
	if id > 0 {
		err := s.DB.WithContext(ctx).First(&row, id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("智能体 %d 不存在", id)
		}
		if err != nil {
			return nil, err
		}
		if !row.Enabled {
			return nil, fmt.Errorf("智能体「%s」已停用", row.Name)
		}
		return &row, nil
	}
	err := s.DB.WithContext(ctx).Where("enabled = ?", true).
		Order("is_default desc, id asc").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return s.fallbackAgent(), nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// providerConfig resolves an agent's bound provider, falling back to the
// config-derived endpoint when the agent is unbound (ProviderID == 0).
func (s *Service) providerConfig(ctx context.Context, providerID int) (*ProviderConfig, error) {
	if providerID <= 0 {
		// An unbound agent means "system default". Prefer the operator's own
		// provider row so built-in agents work without pinning them to a row id
		// (which would block deleting that provider), and only fall back to the
		// config-derived endpoint when the LLM is configured by environment.
		if s.DB != nil {
			var row models.AIProvider
			if err := s.DB.WithContext(ctx).Where("enabled = ?", true).Order("id asc").
				First(&row).Error; err == nil {
				return providerConfigOf(&row), nil
			}
		}
		return s.fallbackProvider(), nil
	}
	var row models.AIProvider
	if err := s.DB.WithContext(ctx).First(&row, providerID).Error; err != nil {
		return nil, fmt.Errorf("供应商 %d 不存在: %w", providerID, err)
	}
	if !row.Enabled {
		return nil, fmt.Errorf("供应商「%s」已停用", row.Name)
	}
	return providerConfigOf(&row), nil
}

// resolveRun resolves a run's agent, its provider and the resulting capability
// scope in one step.
func (s *Service) resolveRun(ctx context.Context, agentID int) (*models.AIAgent, *ProviderConfig, *runScope, error) {
	agent, err := s.ResolveAgent(ctx, agentID)
	if err != nil {
		return nil, nil, nil, err
	}
	pc, err := s.providerConfig(ctx, agent.ProviderID)
	if err != nil {
		return nil, nil, nil, err
	}
	scope := scopeForAgent(agent)
	scope.Provider = pc
	return agent, pc, scope, nil
}

// defaultProvider resolves the endpoint used by model calls that run outside an
// explicit agent context (e.g. a skill's internal helper call).
func (s *Service) defaultProvider(ctx context.Context) (*ProviderConfig, error) {
	agent, err := s.ResolveAgent(ctx, 0)
	if err != nil {
		return s.fallbackProvider(), nil
	}
	return s.providerConfig(ctx, agent.ProviderID)
}

// providerForContext prefers the run's resolved provider, falling back to the
// default when called outside a run.
func (s *Service) providerForContext(ctx context.Context) (*ProviderConfig, error) {
	if sc := runScopeFrom(ctx); sc != nil && sc.Provider != nil {
		return sc.Provider, nil
	}
	return s.defaultProvider(ctx)
}

// DefaultWriteToolsEnabled reports whether the default agent may use write
// tools; used by user-initiated flows (e.g. save-as-chart) that carry no run.
func (s *Service) DefaultWriteToolsEnabled(ctx context.Context) bool {
	agent, err := s.ResolveAgent(ctx, 0)
	if err != nil {
		return s.Config.AIWriteToolsEnabled
	}
	return agent.WriteToolsEnabled
}

/* ------------------------------------------------------------ agent scope */

// scopeForAgent builds the capability policy carried by a run.
func scopeForAgent(a *models.AIAgent) *runScope {
	return &runScope{
		AgentID:              a.ID,
		skills:               codeSet(a.SkillCodes),
		tools:                codeSet(a.ToolCodes),
		WriteToolsEnabled:    a.WriteToolsEnabled,
		WriteConfirmRequired: a.WriteConfirmRequired,
	}
}

// codeSet parses a JSON array of capability names. Empty/blank input — or an
// empty array — yields nil, which callers interpret as "all capabilities".
func codeSet(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil
	}
	var codes []string
	if err := json.Unmarshal([]byte(raw), &codes); err != nil {
		return nil
	}
	set := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		if c = strings.TrimSpace(c); c != "" {
			set[c] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// EncodeCodes renders a capability list as a JSON array for storage.
func EncodeCodes(codes []string) string {
	cleaned := make([]string, 0, len(codes))
	for _, c := range codes {
		if c = strings.TrimSpace(c); c != "" {
			cleaned = append(cleaned, c)
		}
	}
	b, err := json.Marshal(cleaned)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// DecodeCodes exposes a stored capability list as a string slice for the API.
func DecodeCodes(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var codes []string
	if err := json.Unmarshal([]byte(raw), &codes); err != nil {
		return []string{}
	}
	if codes == nil {
		return []string{}
	}
	return codes
}
