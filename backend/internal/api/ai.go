package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"superset/internal/ai"
	"superset/internal/ai/registry"
	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterAI mounts /api/v1/ai/* — the unified Agent plane entrypoints. It is
// self-contained: it only reuses the query engine and security manager, never
// modifying existing modules.
func RegisterAI(r chi.Router, deps *Deps) {
	svc := ai.NewService(deps.Config, deps.Log, deps.DB, deps.Cache, deps.Mgr)
	a := &aiAPI{deps: deps, svc: svc}

	r.Route("/ai", func(ar chi.Router) {
		ar.Use(security.RequireUser)

		// Agent configuration. Deliberately mounted outside the `requireEnabled`
		// gate: an operator must be able to reach it while the plane is switched
		// off, otherwise the master switch could never be turned back on.
		ar.Route("/settings", func(sr chi.Router) {
			sr.With(security.RequirePermission(security.ActionRead, security.ResAIAgent)).
				Get("/", a.getSettings)
			sr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Put("/", a.updateSettings)
		})

		// Capability catalogue for the agent editor (skill/tool allow-lists).
		// No DB access, so it is safe to expose while the plane is off.
		ar.With(security.RequirePermission(security.ActionRead, security.ResAIAgent)).
			Get("/capabilities", a.listCatalog)

		// LLM provider (大模型供应商) CRUD — the table behind the settings page.
		ar.Route("/providers", func(pr chi.Router) {
			pr.With(security.RequirePermission(security.ActionRead, security.ResAIAgent)).
				Get("/", a.listProviders)
			pr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/", a.createProvider)
			pr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Put("/{id}", a.updateProvider)
			pr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Delete("/{id}", a.deleteProvider)
			// Connectivity probe for the editor: validates the endpoint and
			// credentials before the operator saves, so a typo surfaces here
			// instead of inside a failed agent run.
			pr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/test", a.testProvider)
		})

		// Agent (智能体) CRUD — plugin-style bundles binding a provider plus
		// the skills/tools the agent may use. The list is readable on the 分析
		// plane too (ResAIAnalysis): the chat UI needs it to render its agent
		// picker, and it leaks nothing — provider API keys are never returned.
		ar.Route("/agents", func(gr chi.Router) {
			gr.With(security.RequirePermission(security.ActionRead, security.ResAIAnalysis)).
				Get("/", a.listAgents)
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/", a.createAgent)
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Put("/{id}", a.updateAgent)
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Delete("/{id}", a.deleteAgent)
		})

		// Capability plugins — the 技能设置 / 工具设置 tabs. The code, not a
		// numeric id, is the path key: it is what an agent's allow-list stores.
		ar.Route("/skills", func(sr chi.Router) {
			sr.With(security.RequirePermission(security.ActionRead, security.ResAIAgent)).
				Get("/", a.listSkillPlugins)
			sr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/", a.createSkillPlugin)
			sr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Put("/{code}", a.updateSkillPlugin)
			sr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Delete("/{code}", a.deleteSkillPlugin)
		})
		ar.Route("/tools", func(tr chi.Router) {
			tr.With(security.RequirePermission(security.ActionRead, security.ResAIAgent)).
				Get("/", a.listToolPlugins)
			tr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/", a.createToolPlugin)
			tr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Put("/{code}", a.updateToolPlugin)
			tr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Delete("/{code}", a.deleteToolPlugin)
		})

		ar.Group(func(gr chi.Router) {
			gr.Use(a.requireEnabled)

			// Unified Agent entrypoint (SSE stream: plan→act→observe→answer).
			// 分析 plane: ResAIAnalysis, so an analyst can query data without
			// holding ResAIAgent (which would also let them rewrite the Agent).
			gr.With(security.RequirePermission(security.ActionQuery, security.ResAIAnalysis)).
				Post("/agent/invoke", a.agentInvoke)

			// Capability menu for the frontend.
			gr.With(security.RequirePermission(security.ActionRead, security.ResAIAnalysis)).
				Get("/agent/skills", a.listSkills)

			// Run history (audit trail).
			gr.With(security.RequirePermission(security.ActionQuery, security.ResAIAnalysis)).
				Get("/agent/runs", a.listRuns)
			gr.With(security.RequirePermission(security.ActionQuery, security.ResAIAnalysis)).
				Get("/agent/runs/{id}", a.getRun)

			// LLM audit trail (ai_audit_log): operation/model/tokens/latency.
			gr.With(security.RequirePermission(security.ActionQuery, security.ResAIAgent)).
				Get("/audit", a.listAudit)

			// Resume a run whose stream was interrupted (audit §4-5): GET only
			// inspects, POST continues from the persisted trajectory.
			gr.With(security.RequirePermission(security.ActionQuery, security.ResAIAnalysis)).
				Get("/agent/runs/{id}/resume", a.getRunResume)
			gr.With(security.RequirePermission(security.ActionQuery, security.ResAIAnalysis)).
				Post("/agent/runs/{id}/resume", a.resumeRun)

			// Roll back a run's write operations (reverse-order, from snapshots).
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/agent/runs/{id}/rollback", a.rollbackRun)

			// Lite metric dictionary (semantic layer) governance CRUD.
			gr.With(security.RequirePermission(security.ActionRead, security.ResAIAgent)).
				Get("/metrics", a.listMetrics)
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/metrics", a.createMetric)
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Put("/metrics/{pk}", a.updateMetric)
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Delete("/metrics/{pk}", a.deleteMetric)

			// One-click "save as chart" from an Agent answer's chart_spec.
			gr.With(security.RequirePermission(security.ActionWrite, security.ResChart)).
				Post("/chart", a.saveChart)

			// NL→SQL memory (范例记忆): confirmed question→SQL examples that are
			// recalled into the planning prompt. Readable by analysts (they are
			// the ones who confirm them), writable by the agent owner.
			gr.With(security.RequirePermission(security.ActionRead, security.ResAIAnalysis)).
				Get("/sql-memory", a.listSQLMemory)
			gr.With(security.RequirePermission(security.ActionRead, security.ResAIAnalysis)).
				Get("/sql-memory/export", a.exportSQLMemory)
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/sql-memory", a.createSQLMemory)
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/sql-memory/{id}/approve", a.setSQLMemoryStatus(ai.MemoryStatusApproved))
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Post("/sql-memory/{id}/reject", a.setSQLMemoryStatus(ai.MemoryStatusRejected))
			gr.With(security.RequirePermission(security.ActionWrite, security.ResAIAgent)).
				Delete("/sql-memory/{id}", a.deleteSQLMemory)
		})
	})
}

type aiAPI struct {
	deps *Deps
	svc  *ai.Service
}

// requireEnabled hides the whole AI plane while AI_ASSISTANT_ENABLED=false so
// that a disabled deployment exposes no AI surface at all.
func (a *aiAPI) requireEnabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.svc.Enabled() {
			server.WriteError(w, apperrors.New(apperrors.UnavailableError,
				"AI assistant is disabled", http.StatusServiceUnavailable))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// listSkills returns the public skill/tool contract (capability menu).
func (a *aiAPI) listSkills(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.svc.Capabilities(r.Context())})
}

// listCatalog returns every registered skill/tool for the agent editor.
func (a *aiAPI) listCatalog(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.svc.Catalog()})
}

// getSettings returns the global Agent master switch.
func (a *aiAPI) getSettings(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"enabled": a.svc.Settings().Enabled,
	}})
}

// aiSettingsRequest is the PUT /ai/settings body (the master switch only).
type aiSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

// updateSettings persists the master switch and applies it immediately: every
// Agent endpoint reads through the same store, so no restart is needed.
func (a *aiAPI) updateSettings(w http.ResponseWriter, r *http.Request) {
	var req aiSettingsRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	next := &ai.Settings{Enabled: req.Enabled}
	if err := a.svc.UpdateSettings(r.Context(), next); err != nil {
		server.WriteError(w, apperrors.Internal("Failed to save AI settings: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"enabled": next.Enabled}})
}

/* ----------------------------------------------------- LLM provider CRUD */

// providerJSON renders a provider for the browser. The stored key is masked
// behind `api_key_configured` so the page can never read it back.
func providerJSON(p *models.AIProvider) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "provider_type": p.ProviderType,
		"base_url": p.BaseURL, "model": p.Model, "timeout_sec": p.TimeoutSec,
		"enabled": p.Enabled, "description": p.Description,
		"api_key_configured": strings.TrimSpace(p.APIKey) != "",
		"created_on":         p.CreatedOn, "changed_on": p.ChangedOn,
	}
}

// providerRequest is the create/update body. `api_key` is a pointer so the form
// can distinguish "leave unchanged" (omitted) from "replace".
type providerRequest struct {
	Name         string  `json:"name"`
	ProviderType string  `json:"provider_type"`
	BaseURL      string  `json:"base_url"`
	Model        string  `json:"model"`
	TimeoutSec   int     `json:"timeout_sec"`
	Enabled      *bool   `json:"enabled"`
	Description  string  `json:"description"`
	APIKey       *string `json:"api_key"`
}

// providerFromRequest validates the payload into a model. `existing` carries the
// current row on update so an omitted key keeps its stored value.
func providerFromRequest(req providerRequest, existing *models.AIProvider) (*models.AIProvider, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("供应商名称不能为空")
	}
	ptype := strings.TrimSpace(req.ProviderType)
	if ptype == "" {
		ptype = "openai_compatible"
	}
	switch ptype {
	case "ollama", "openai_compatible":
	default:
		return nil, apperrors.BadRequest("provider_type 必须是 ollama 或 openai_compatible")
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		return nil, apperrors.BadRequest("接口地址 (base_url) 不能为空")
	}
	p := &models.AIProvider{
		Name: name, ProviderType: ptype, BaseURL: baseURL,
		Model: strings.TrimSpace(req.Model), TimeoutSec: req.TimeoutSec,
		Description: strings.TrimSpace(req.Description), Enabled: true,
	}
	if existing != nil {
		p.ID = existing.ID
		p.APIKey = existing.APIKey
		p.Enabled = existing.Enabled
	}
	if req.Enabled != nil {
		p.Enabled = *req.Enabled
	}
	if req.APIKey != nil {
		p.APIKey = strings.TrimSpace(*req.APIKey)
	}
	return p, nil
}

func (a *aiAPI) listProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := a.svc.ListProviders(r.Context())
	if err != nil {
		server.WriteError(w, apperrors.Internal("Failed to list providers: %s", err.Error()))
		return
	}
	out := make([]any, 0, len(rows))
	for i := range rows {
		out = append(out, providerJSON(&rows[i]))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out})
}

func (a *aiAPI) createProvider(w http.ResponseWriter, r *http.Request) {
	var req providerRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	p, err := providerFromRequest(req, nil)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.svc.CreateProvider(r.Context(), p); err != nil {
		server.WriteError(w, apperrors.Internal("Failed to create provider: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": providerJSON(p)})
}

func (a *aiAPI) updateProvider(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil || id <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid provider id"))
		return
	}
	existing, err := a.svc.GetProvider(r.Context(), id)
	if err != nil {
		server.WriteError(w, notFoundOr(err, "供应商不存在"))
		return
	}
	var req providerRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	p, err := providerFromRequest(req, existing)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.svc.UpdateProvider(r.Context(), p); err != nil {
		server.WriteError(w, apperrors.Internal("Failed to update provider: %s", err.Error()))
		return
	}
	p.CreatedOn = existing.CreatedOn
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": providerJSON(p)})
}

// providerTestRequest is the probe body. It mirrors the edit form: the browser
// never receives the stored key, so a blank `api_key` on an existing provider
// means "reuse the saved secret" rather than "no auth".
type providerTestRequest struct {
	ID           int     `json:"id"`
	ProviderType string  `json:"provider_type"`
	BaseURL      string  `json:"base_url"`
	Model        string  `json:"model"`
	TimeoutSec   int     `json:"timeout_sec"`
	APIKey       *string `json:"api_key"`
}

// testProvider validates an endpoint without saving it.
func (a *aiAPI) testProvider(w http.ResponseWriter, r *http.Request) {
	var req providerTestRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}

	var existing *models.AIProvider
	if req.ID > 0 {
		row, err := a.svc.GetProvider(r.Context(), req.ID)
		if err != nil {
			server.WriteError(w, notFoundOr(err, "供应商不存在"))
			return
		}
		existing = row
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" && existing != nil {
		baseURL = existing.BaseURL
	}
	if baseURL == "" {
		server.WriteError(w, apperrors.BadRequest("请先填写接口地址 (Base URL)"))
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" && existing != nil {
		model = existing.Model
	}
	ptype := strings.TrimSpace(req.ProviderType)
	if ptype == "" && existing != nil {
		ptype = existing.ProviderType
	}
	timeout := req.TimeoutSec
	if timeout <= 0 && existing != nil {
		timeout = existing.TimeoutSec
	}

	apiKey := ""
	switch {
	case req.APIKey != nil && strings.TrimSpace(*req.APIKey) != "":
		apiKey = strings.TrimSpace(*req.APIKey)
	case existing != nil:
		apiKey = existing.APIKey
	}

	probe, err := ai.ProbeProvider(r.Context(), ai.ProviderConfig{
		ProviderType: ptype, Name: "probe", BaseURL: baseURL,
		Model: model, APIKey: apiKey, TimeoutSec: timeout,
	})
	if err != nil {
		if errors.Is(err, ai.ErrProbeBaseURL) {
			server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
			return
		}
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": probe})
}

func (a *aiAPI) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil || id <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid provider id"))
		return
	}
	if err := a.svc.DeleteProvider(r.Context(), id); err != nil {
		if errors.Is(err, ai.ErrProviderInUse) {
			server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
			return
		}
		server.WriteError(w, apperrors.Internal("Failed to delete provider: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

/* ---------------------------------------------------------- agent CRUD */

// agentJSON renders an agent for the browser, resolving its provider name.
func agentJSON(a *models.AIAgent, providerName string) map[string]any {
	return map[string]any{
		"id": a.ID, "name": a.Name, "description": a.Description,
		"provider_id": a.ProviderID, "provider_name": providerName,
		"is_default": a.IsDefault, "enabled": a.Enabled,
		// Built-in agents are shipped and immutable; the UI hides their edit and
		// delete actions on this flag.
		"builtin":     a.IsBuiltin,
		"skill_codes": ai.DecodeCodes(a.SkillCodes), "tool_codes": ai.DecodeCodes(a.ToolCodes),
		"max_steps": a.MaxSteps, "max_tool_calls": a.MaxToolCalls,
		"token_budget": a.TokenBudget, "run_timeout_sec": a.RunTimeoutSec,
		"write_tools_enabled": a.WriteToolsEnabled, "write_confirm_required": a.WriteConfirmRequired,
		"created_on": a.CreatedOn, "changed_on": a.ChangedOn,
	}
}

// agentRequest is the create/update body for an agent.
type agentRequest struct {
	Name                 string   `json:"name"`
	Description          string   `json:"description"`
	ProviderID           int      `json:"provider_id"`
	IsDefault            *bool    `json:"is_default"`
	Enabled              *bool    `json:"enabled"`
	SkillCodes           []string `json:"skill_codes"`
	ToolCodes            []string `json:"tool_codes"`
	MaxSteps             int      `json:"max_steps"`
	MaxToolCalls         int      `json:"max_tool_calls"`
	TokenBudget          int      `json:"token_budget"`
	RunTimeoutSec        int      `json:"run_timeout_sec"`
	WriteToolsEnabled    bool     `json:"write_tools_enabled"`
	WriteConfirmRequired bool     `json:"write_confirm_required"`
}

// agentFromRequest validates the payload into a model. `existing` carries the
// current row on update so omitted booleans keep their stored value.
func agentFromRequest(req agentRequest, existing *models.AIAgent) (*models.AIAgent, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, apperrors.BadRequest("智能体名称不能为空")
	}
	a := &models.AIAgent{
		Name: name, Description: strings.TrimSpace(req.Description),
		ProviderID: req.ProviderID,
		SkillCodes: ai.EncodeCodes(req.SkillCodes), ToolCodes: ai.EncodeCodes(req.ToolCodes),
		MaxSteps: req.MaxSteps, MaxToolCalls: req.MaxToolCalls,
		TokenBudget: req.TokenBudget, RunTimeoutSec: req.RunTimeoutSec,
		WriteToolsEnabled: req.WriteToolsEnabled, WriteConfirmRequired: req.WriteConfirmRequired,
		Enabled: true,
	}
	if existing != nil {
		a.ID = existing.ID
		a.Enabled = existing.Enabled
		a.IsDefault = existing.IsDefault
	}
	if req.Enabled != nil {
		a.Enabled = *req.Enabled
	}
	if req.IsDefault != nil {
		a.IsDefault = *req.IsDefault
	}
	return a, nil
}

// providerNames returns id→name for the agent list rendering.
func (a *aiAPI) providerNames(ctx context.Context) map[int]string {
	rows, err := a.svc.ListProviders(ctx)
	if err != nil {
		return map[int]string{}
	}
	out := make(map[int]string, len(rows))
	for i := range rows {
		out[rows[i].ID] = rows[i].Name
	}
	return out
}

func (a *aiAPI) listAgents(w http.ResponseWriter, r *http.Request) {
	rows, err := a.svc.ListAgents(r.Context())
	if err != nil {
		server.WriteError(w, apperrors.Internal("Failed to list agents: %s", err.Error()))
		return
	}
	names := a.providerNames(r.Context())
	out := make([]any, 0, len(rows))
	for i := range rows {
		out = append(out, agentJSON(&rows[i], names[rows[i].ProviderID]))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out})
}

func (a *aiAPI) createAgent(w http.ResponseWriter, r *http.Request) {
	var req agentRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	ag, err := agentFromRequest(req, nil)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.svc.CreateAgent(r.Context(), ag); err != nil {
		server.WriteError(w, apperrors.Internal("Failed to create agent: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": agentJSON(ag, a.providerNames(r.Context())[ag.ProviderID])})
}

func (a *aiAPI) updateAgent(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil || id <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid agent id"))
		return
	}
	existing, err := a.svc.GetAgent(r.Context(), id)
	if err != nil {
		server.WriteError(w, notFoundOr(err, "智能体不存在"))
		return
	}
	if existing.IsBuiltin {
		server.WriteError(w, apperrors.BadRequest("内置智能体不可修改"))
		return
	}
	var req agentRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	ag, err := agentFromRequest(req, existing)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.svc.UpdateAgent(r.Context(), ag); err != nil {
		server.WriteError(w, apperrors.Internal("Failed to update agent: %s", err.Error()))
		return
	}
	ag.CreatedOn = existing.CreatedOn
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": agentJSON(ag, a.providerNames(r.Context())[ag.ProviderID])})
}

func (a *aiAPI) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil || id <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid agent id"))
		return
	}
	if err := a.svc.DeleteAgent(r.Context(), id); err != nil {
		if errors.Is(err, ai.ErrAgentBuiltin) {
			server.WriteError(w, apperrors.BadRequest("内置智能体不可删除"))
			return
		}
		server.WriteError(w, apperrors.Internal("Failed to delete agent: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// notFoundOr maps a missing row to a 404 and anything else to a 500.
func notFoundOr(err error, message string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperrors.NotFound("%s", message)
	}
	return apperrors.Internal("Database error: %s", err.Error())
}

// agentInvokeRequest is the POST /agent/invoke body (design §19.1).
type agentInvokeRequest struct {
	Question  string             `json:"question"`
	SessionID string             `json:"session_id"`
	AgentID   int                `json:"agent_id"`
	Context   registry.AIContext `json:"context"`
}

// agentInvoke runs the Agent once and streams its plan→act→observe→answer
// trajectory as Server-Sent Events. A request without session_id starts a new
// analysis session; the plan frame hands the id back for follow-ups. agent_id
// selects a configured agent; omitted means the default one.
func (a *aiAPI) agentInvoke(w http.ResponseWriter, r *http.Request) {
	var req agentInvokeRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		server.WriteError(w, apperrors.BadRequest("question is required"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		server.WriteError(w, apperrors.Internal("Streaming is not supported"))
		return
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	writeSSEHeaders(w)
	flusher.Flush()

	emit := func(ev ai.Event) { writeSSE(w, flusher, ev) }
	a.svc.Runtime().Run(r.Context(), userIDOf(r), sessionID, req.Question, req.AgentID, req.Context, emit)
}

func writeSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// writeSSE emits one SSE frame and flushes it to the client.
func writeSSE(w http.ResponseWriter, f http.Flusher, ev ai.Event) {
	data, err := json.Marshal(ev.Data)
	if err != nil {
		data = []byte("{}")
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
		return
	}
	f.Flush()
}

// listRuns returns the current user's Agent run history, paginated. An explicit
// `session_id` query param replays one analysis conversation.
func (a *aiAPI) listRuns(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.AIAgentRun{}).
		Where("user_id = ?", userIDOf(r))
	if sessionID := strings.TrimSpace(r.URL.Query().Get("session_id")); sessionID != "" {
		q = q.Where("session_id = ?", sessionID)
	}
	if p.Search != "" {
		q = q.Where("question ILIKE ?", "%"+p.Search+"%")
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "created_on desc", func(run *models.AIAgentRun) any { return aiRunJSON(run) })
}

// listAudit returns the LLM audit trail (ai_audit_log). Admins see every call;
// other users only see their own, mirroring the run-history visibility rule.
func (a *aiAPI) listAudit(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.AIAuditLog{})
	if perms, _ := security.PermissionsFrom(r.Context()); perms == nil || !perms.Admin() {
		q = q.Where("user_id = ?", userIDOf(r))
	}
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("operation ILIKE ? OR model_name ILIKE ?", like, like)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "created_on desc", func(l *models.AIAuditLog) any { return aiAuditJSON(l) })
}

// aiAuditJSON renders one audit row without the (potentially large) prompt
// text; the prompt stays in the database for forensic drill-down.
func aiAuditJSON(l *models.AIAuditLog) map[string]any {
	return map[string]any{
		"id": l.ID, "run_id": l.RunID, "user_id": l.UserID,
		"operation": l.Operation, "model_name": l.ModelName,
		"token_usage": l.TokenUsage, "latency_ms": l.LatencyMS,
		"cache_hit": l.CacheHit, "error_msg": l.ErrorMsg,
		"created_on": l.CreatedOn,
	}
}

// getRun returns one run with its step trajectory (tool call log).
func (a *aiAPI) getRun(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var run models.AIAgentRun
	if err := a.deps.DB.WithContext(r.Context()).
		Where("id = ? AND user_id = ?", id, userIDOf(r)).First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			server.WriteError(w, apperrors.NotFound("Run not found"))
			return
		}
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}

	var calls []models.AIToolCallLog
	_ = a.deps.DB.WithContext(r.Context()).Where("run_id = ?", id).
		Order("step_no asc").Find(&calls).Error
	steps := make([]any, 0, len(calls))
	for i := range calls {
		steps = append(steps, toolCallJSON(&calls[i]))
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": map[string]any{"run": aiRunJSON(&run), "steps": steps},
	})
}

// rollbackRun undoes the write operations a run performed, restoring every
// snapshotted object to its pre-image.
func (a *aiAPI) rollbackRun(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil || id <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid run id"))
		return
	}
	res, err := a.svc.RollbackRun(r.Context(), id)
	if err != nil {
		server.WriteError(w, apperrors.Internal("Rollback failed: %s", err.Error()))
		return
	}
	server.WriteResult(w, res)
}

// getRunResume reports whether a run can be attached to, continued, or is over.
// It is read-only: the panel calls it on open to decide whether to offer
// "继续上次分析" after a refresh or a dropped connection.
func (a *aiAPI) getRunResume(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil || id <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid run id"))
		return
	}
	state, err := a.svc.BuildResumeState(r.Context(), userIDOf(r), id)
	if err != nil {
		server.WriteError(w, notFoundOr(err, "Run not found"))
		return
	}
	server.WriteResult(w, state)
}

// resumeRunRequest is the POST /agent/runs/{id}/resume body. Every field is
// optional: the persisted run supplies the question, the context and the agent.
type resumeRunRequest struct {
	Question string             `json:"question"`
	AgentID  int                `json:"agent_id"`
	Context  registry.AIContext `json:"context"`
}

// resumeRun continues an interrupted run over SSE. The stream replays the
// persisted plan (`resumed: true`) and every completed step (`replayed: true`)
// before emitting live events, so the client rebuilds the transcript it lost
// and then simply carries on.
//
// When the original run is still executing the endpoint emits one `attached`
// frame instead of starting a second run: two concurrent continuations of the
// same question would double the cost for one answer.
func (a *aiAPI) resumeRun(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil || id <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid run id"))
		return
	}
	var req resumeRunRequest
	if r.ContentLength != 0 {
		if err := server.DecodeJSON(r, &req); err != nil {
			server.WriteError(w, err)
			return
		}
	}
	userID := userIDOf(r)
	seed, state, err := a.svc.SeedFor(r.Context(), userID, id)
	if err != nil {
		server.WriteError(w, notFoundOr(err, "Run not found"))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		server.WriteError(w, apperrors.Internal("Streaming is not supported"))
		return
	}
	writeSSEHeaders(w)
	flusher.Flush()
	emit := func(ev ai.Event) { writeSSE(w, flusher, ev) }

	switch {
	case state.Attached:
		emit(ai.Event{Type: ai.EventAttached, Data: ai.AttachedEvent{RunID: id, Status: state.Status}})
		return
	case seed == nil:
		emit(ai.Event{Type: ai.EventError, Data: ai.ErrorEvent{
			RunID:   id,
			Message: resumeRefusal(state),
		}})
		return
	}

	question := strings.TrimSpace(req.Question)
	if question == "" {
		question = state.Question
	}
	actx := req.Context
	if actx.Scope == "" {
		actx.Scope = state.Scope
	}
	if actx.Language == "" {
		actx.Language = "zh"
	}

	a.svc.Runtime().Resume(r.Context(), userID, ai.RunInput{
		Question: question, SessionID: state.SessionID, AgentID: req.AgentID,
		Context: actx, Emit: emit, Resume: seed,
	})
}

// resumeRefusal turns a non-resumable state into an actionable message.
func resumeRefusal(state *ai.ResumeState) string {
	switch state.Reason {
	case "done":
		return "This run already finished; ask a follow-up instead"
	case "no_steps":
		return "This run has no completed steps to continue from; please ask again"
	default:
		return "This run cannot be resumed"
	}
}

// aiRunJSON renders a run for the browser.
func aiRunJSON(run *models.AIAgentRun) map[string]any {
	return map[string]any{
		"id": run.ID, "user_id": run.UserID, "session_id": run.SessionID, "scope": run.Scope,
		"dashboard_id": run.DashboardID, "chart_id": run.ChartID, "dataset_id": run.DatasetID,
		"question": run.Question, "status": run.Status,
		"step_count": run.StepCount, "tool_call_count": run.ToolCallCount,
		"token_usage": run.TokenUsage, "latency_ms": run.LatencyMS,
		"final_answer": run.FinalAnswer, "error_msg": run.ErrorMsg,
		"created_on": run.CreatedOn, "changed_on": run.ChangedOn,
		// Observability + reproducibility (audit §4-6).
		"first_token_ms": run.FirstTokenMS, "tool_error_count": run.ToolErrorCount,
		"empty_result_count": run.EmptyResultCount, "self_check_count": run.SelfCheckCount,
		"model_name": run.ModelName, "temperature": run.Temperature, "seed": run.Seed,
		"generated_sql": run.GeneratedSQL, "sql_hash": run.SQLHash,
		"plan_json": run.PlanJSON, "resumed_from": run.ResumedFrom,
	}
}

func toolCallJSON(c *models.AIToolCallLog) map[string]any {
	return map[string]any{
		"id": c.ID, "run_id": c.RunID, "step_no": c.StepNo,
		"kind": c.Kind, "name": c.Name, "side_effect": c.SideEffect,
		"input_args": c.InputArgs, "result": c.Result, "snapshot": c.Snapshot,
		"target_ids": c.TargetIDs, "status": c.Status, "error_msg": c.ErrorMsg,
		"duration_ms": c.DurationMS, "created_on": c.CreatedOn,
	}
}

/* ------------------------------------------------- Lite metric dictionary */

func aiMetricJSON(m *models.AIMetric) map[string]any {
	return map[string]any{
		"id": m.ID, "name": m.Name, "description": m.Description,
		"expression": m.Expression, "dataset_id": m.DatasetID,
		"dimensions": json.RawMessage(orEmptyJSON(m.Dimensions)),
		"unit":       m.Unit, "status": m.Status, "owner_id": m.OwnerID,
		"created_on": m.CreatedOn, "changed_on": m.ChangedOn,
	}
}

func orEmptyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "[]"
	}
	return s
}

// listMetrics returns the metric dictionary, newest changed first. Optional
// filters: ?dataset_id=, ?status=draft|approved.
func (a *aiAPI) listMetrics(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.AIMetric{})
	if id := r.URL.Query().Get("dataset_id"); id != "" {
		q = q.Where("dataset_id = ?", id)
	}
	if status := r.URL.Query().Get("status"); status != "" {
		q = q.Where("status = ?", status)
	}
	if p.Search != "" {
		q = q.Where("name ILIKE ?", "%"+p.Search+"%")
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "changed_on desc", func(m *models.AIMetric) any { return aiMetricJSON(m) })
}

// metricRequest is the POST/PUT metric dictionary body.
type metricRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Expression  string   `json:"expression"`
	DatasetID   int      `json:"dataset_id"`
	Dimensions  []string `json:"dimensions"`
	Unit        string   `json:"unit"`
	Status      string   `json:"status"`
}

func (a *aiAPI) metricFromRequest(req metricRequest, existing *models.AIMetric) (*models.AIMetric, error) {
	name := strings.TrimSpace(req.Name)
	expr := strings.TrimSpace(req.Expression)
	if name == "" || expr == "" {
		return nil, apperrors.BadRequest("name and expression are required")
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "draft"
	}
	if status != "draft" && status != "approved" {
		return nil, apperrors.BadRequest("status must be draft or approved")
	}
	dims := "[]"
	if len(req.Dimensions) > 0 {
		if b, err := json.Marshal(req.Dimensions); err == nil {
			dims = string(b)
		}
	}
	m := &models.AIMetric{
		Name: name, Description: req.Description, Expression: expr,
		DatasetID: req.DatasetID, Dimensions: dims, Unit: strings.TrimSpace(req.Unit),
		Status: status,
	}
	if existing != nil {
		m.ID = existing.ID
		m.OwnerID = existing.OwnerID
	}
	return m, nil
}

// createMetric adds a dictionary entry (draft by default; approved rows go to
// the Agent prompt).
func (a *aiAPI) createMetric(w http.ResponseWriter, r *http.Request) {
	var req metricRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	m, err := a.metricFromRequest(req, nil)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	m.OwnerID = userIDOf(r)
	if err := a.deps.DB.WithContext(r.Context()).Create(m).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Failed to create metric: %s", err.Error()))
		return
	}
	server.WriteResult(w, aiMetricJSON(m))
}

// updateMetric edits a dictionary entry.
func (a *aiAPI) updateMetric(w http.ResponseWriter, r *http.Request) {
	pk, err := server.URLParamInt(r, "pk")
	if err != nil || pk <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid metric id"))
		return
	}
	var existing models.AIMetric
	if err := a.deps.DB.WithContext(r.Context()).First(&existing, pk).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			server.WriteError(w, apperrors.NotFound("Metric not found"))
			return
		}
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	var req metricRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	m, err := a.metricFromRequest(req, &existing)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.deps.DB.WithContext(r.Context()).Model(&models.AIMetric{}).Where("id = ?", pk).
		Updates(map[string]any{
			"name": m.Name, "description": m.Description, "expression": m.Expression,
			"dataset_id": m.DatasetID, "dimensions": m.Dimensions,
			"unit": m.Unit, "status": m.Status,
		}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Failed to update metric: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(&existing, pk).Error
	server.WriteResult(w, aiMetricJSON(&existing))
}

// deleteMetric removes a dictionary entry.
func (a *aiAPI) deleteMetric(w http.ResponseWriter, r *http.Request) {
	pk, err := server.URLParamInt(r, "pk")
	if err != nil || pk <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid metric id"))
		return
	}
	res := a.deps.DB.WithContext(r.Context()).Delete(&models.AIMetric{}, pk)
	if res.Error != nil {
		server.WriteError(w, apperrors.Internal("Failed to delete metric: %s", res.Error.Error()))
		return
	}
	if res.RowsAffected == 0 {
		server.WriteError(w, apperrors.NotFound("Metric not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

/* ------------------------------------------------ one-click save as chart */

// saveChartRequest is the POST /ai/chart body — usually the `chart_spec`
// artifact of an Agent answer plus the host dashboard.
type saveChartRequest struct {
	ai.SaveChartSpec
}

// saveChart persists an Agent answer as a real chart and optionally appends it
// to the host dashboard's layout. Gated on the write-tools switch so a
// deployment that disabled AI writes keeps this closed too.
func (a *aiAPI) saveChart(w http.ResponseWriter, r *http.Request) {
	if !a.svc.DefaultWriteToolsEnabled(r.Context()) {
		server.WriteError(w, apperrors.Forbidden("AI write tools are disabled"))
		return
	}
	var req saveChartRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	res, err := a.svc.SaveChart(r.Context(), userIDOf(r), req.SaveChartSpec)
	if err != nil {
		server.WriteError(w, apperrors.Internal("Failed to save chart: %s", err.Error()))
		return
	}
	server.WriteResult(w, res)
}

/* ------------------------------------------------------- NL→SQL memory */

// sqlMemoryJSON renders one example for the chat page and the review queue.
func sqlMemoryJSON(m *models.AISQLMemory) map[string]any {
	return map[string]any{
		"id": m.ID, "dataset_id": m.DatasetID, "question": m.Question,
		"sql": m.SQLQuery, "spec": orEmptyJSON(m.SpecJSON),
		"source": m.Source, "status": m.Status, "quality": m.Quality,
		"run_id": m.RunID, "created_by": m.CreatedBy, "hits": m.Hits,
		"last_hit_on": m.LastHitOn, "created_on": m.CreatedOn, "changed_on": m.ChangedOn,
	}
}

// listSQLMemory returns the example library, newest first. `?status=pending`
// is the review queue; `?dataset_id=` narrows to one dataset.
func (a *aiAPI) listSQLMemory(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.AISQLMemory{})
	if id := strings.TrimSpace(r.URL.Query().Get("dataset_id")); id != "" {
		q = q.Where("dataset_id = ?", id)
	}
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		q = q.Where("status = ?", status)
	}
	if source := strings.TrimSpace(r.URL.Query().Get("source")); source != "" {
		q = q.Where("source = ?", source)
	}
	if p.Search != "" {
		q = q.Where("question ILIKE ? OR sql_query ILIKE ?", "%"+p.Search+"%", "%"+p.Search+"%")
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "changed_on desc", func(m *models.AISQLMemory) any { return sqlMemoryJSON(m) })
}

// sqlMemoryRequest is the POST /ai/sql-memory body ("保存为范例").
type sqlMemoryRequest struct {
	DatasetID int             `json:"dataset_id"`
	Question  string          `json:"question"`
	SQL       string          `json:"sql"`
	Spec      json.RawMessage `json:"spec"`
}

// createSQLMemory stores a human-confirmed example. Confirmed means approved:
// a person looked at the SQL, so it is immediately recallable.
func (a *aiAPI) createSQLMemory(w http.ResponseWriter, r *http.Request) {
	var req sqlMemoryRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	// Anything that reaches the library must survive the same guard as an
	// Agent query: a saved example is executed again later.
	if err := ai.ValidateReadOnlySQL(req.SQL); err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	spec := ""
	if len(req.Spec) > 0 {
		spec = string(req.Spec)
	}
	row, err := a.svc.SaveSQLMemory(r.Context(), userIDOf(r), ai.SQLMemoryInput{
		DatasetID: req.DatasetID, Question: req.Question, SQL: req.SQL,
		SpecJSON: spec, Source: ai.MemorySourceConfirmed, Status: ai.MemoryStatusApproved,
	})
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	server.WriteResult(w, sqlMemoryJSON(row))
}

// setSQLMemoryStatus approves or rejects a captured example.
func (a *aiAPI) setSQLMemoryStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := server.URLParamInt(r, "id")
		if err != nil || id <= 0 {
			server.WriteError(w, apperrors.BadRequest("invalid id"))
			return
		}
		res := a.deps.DB.WithContext(r.Context()).Model(&models.AISQLMemory{}).
			Where("id = ?", id).Update("status", status)
		if res.Error != nil {
			server.WriteError(w, apperrors.Internal("Failed to update example: %s", res.Error.Error()))
			return
		}
		if res.RowsAffected == 0 {
			server.WriteError(w, apperrors.NotFound("Example not found"))
			return
		}
		var row models.AISQLMemory
		if err := a.deps.DB.WithContext(r.Context()).First(&row, id).Error; err != nil {
			server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
			return
		}
		server.WriteResult(w, sqlMemoryJSON(&row))
	}
}

// deleteSQLMemory removes an example so it is never recalled again.
func (a *aiAPI) deleteSQLMemory(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "id")
	if err != nil || id <= 0 {
		server.WriteError(w, apperrors.BadRequest("invalid id"))
		return
	}
	res := a.deps.DB.WithContext(r.Context()).Delete(&models.AISQLMemory{}, id)
	if res.Error != nil {
		server.WriteError(w, apperrors.Internal("Failed to delete example: %s", res.Error.Error()))
		return
	}
	if res.RowsAffected == 0 {
		server.WriteError(w, apperrors.NotFound("Example not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// exportSQLMemory renders the library as markdown: the examples become a file
// that can live in git and move between environments.
func (a *aiAPI) exportSQLMemory(w http.ResponseWriter, r *http.Request) {
	datasetID, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("dataset_id")))
	md, err := a.svc.ExportSQLMemory(r.Context(), datasetID)
	if err != nil {
		server.WriteError(w, apperrors.Internal("Export failed: %s", err.Error()))
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="sql-memory.md"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(md))
}
