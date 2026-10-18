package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"superset/internal/ai"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/server"
)

// The skill/tool plugin endpoints back the 技能设置 and 工具设置 tabs. They share
// one shape: list, create, update by code, delete by code. The code is the path
// key because it is the stable identity an agent's allow-list refers to.

func (a *aiAPI) listSkillPlugins(w http.ResponseWriter, r *http.Request) {
	rows, err := a.svc.ListSkillPlugins(r.Context())
	if err != nil {
		server.WriteError(w, pluginAPIError(err))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": rows})
}

func (a *aiAPI) createSkillPlugin(w http.ResponseWriter, r *http.Request) {
	var req ai.SkillPluginInput
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	row, err := a.svc.CreateSkillPlugin(r.Context(), &req)
	if err != nil {
		server.WriteError(w, pluginAPIError(err))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": row})
}

func (a *aiAPI) updateSkillPlugin(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var req ai.SkillPluginInput
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	row, err := a.svc.UpdateSkillPlugin(r.Context(), code, &req)
	if err != nil {
		server.WriteError(w, pluginAPIError(err))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": row})
}

func (a *aiAPI) deleteSkillPlugin(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if err := a.svc.DeleteSkillPlugin(r.Context(), code); err != nil {
		server.WriteError(w, pluginAPIError(err))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"code": code}})
}

func (a *aiAPI) listToolPlugins(w http.ResponseWriter, r *http.Request) {
	rows, err := a.svc.ListToolPlugins(r.Context())
	if err != nil {
		server.WriteError(w, pluginAPIError(err))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": rows})
}

func (a *aiAPI) createToolPlugin(w http.ResponseWriter, r *http.Request) {
	var req ai.ToolPluginInput
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	row, err := a.svc.CreateToolPlugin(r.Context(), &req)
	if err != nil {
		server.WriteError(w, pluginAPIError(err))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": row})
}

func (a *aiAPI) updateToolPlugin(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	var req ai.ToolPluginInput
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	row, err := a.svc.UpdateToolPlugin(r.Context(), code, &req)
	if err != nil {
		server.WriteError(w, pluginAPIError(err))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": row})
}

func (a *aiAPI) deleteToolPlugin(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if err := a.svc.DeleteToolPlugin(r.Context(), code); err != nil {
		server.WriteError(w, pluginAPIError(err))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"code": code}})
}

// pluginAPIError maps the plugin sentinels onto HTTP status codes. Without this
// every validation slip surfaced as a 500 and the operator saw "internal error"
// while the real cause was a one-character code typo.
func pluginAPIError(err error) error {
	switch {
	case errors.Is(err, ai.ErrPluginNotFound):
		return apperrors.NotFound("%s", err.Error())
	case errors.Is(err, ai.ErrPluginCodeTaken):
		return apperrors.Conflict("%s", err.Error())
	case errors.Is(err, ai.ErrPluginBuiltin), errors.Is(err, ai.ErrPluginInvalid):
		return apperrors.BadRequest("%s", err.Error())
	default:
		return apperrors.Internal("能力配置失败: %s", err.Error())
	}
}
