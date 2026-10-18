package api

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"superset/internal/ai"
	"superset/internal/ai/registry"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// dataset_read.go exposes the dataset-scoped read path to non-agent callers.
//
// Why a separate endpoint instead of reusing SQL Lab: SQL Lab is an editor
// session (it takes CSRF-protected writes, keeps tab state, is not scoped to a
// dataset). An external consumer — the MCP server, a notebook, a scheduled
// exporter — needs one thing: "run this read-only SQL against this dataset with
// my permissions". Making that a first-class endpoint means the guard, RLS and
// masking have exactly one implementation to review.

type datasetReadAPI struct {
	deps *Deps
}

// RegisterDatasetRead mounts POST /api/v1/dataset/{pk}/query.
func RegisterDatasetRead(r chi.Router, deps *Deps) {
	a := &datasetReadAPI{deps: deps}
	r.With(
		security.RequireUser,
		security.RequirePermission(security.ActionRead, security.ResQuery),
	).Post("/dataset/{pk}/query", a.run)
	// Guard-only check: answers "would this statement be allowed?" without
	// touching the warehouse. The MCP dry_plan tool uses it.
	r.With(
		security.RequireUser,
		security.RequirePermission(security.ActionRead, security.ResQuery),
	).Post("/dataset/{pk}/query/validate", a.validate)
}

func (a *datasetReadAPI) validate(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	body, err := readSQLBody(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	svc := ai.NewService(a.deps.Config, a.deps.Log, a.deps.DB, a.deps.Cache, a.deps.Mgr)
	if err := ai.ValidateDatasetSQL(r.Context(), svc, datasetID, body.SQL); err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"valid":      true,
		"dataset_id": datasetID,
		"note":       "已通过表/列白名单、函数黑名单与通配符投影检查；未执行",
	}})
}

type sqlBody struct {
	SQL   string `json:"sql"`
	Limit int    `json:"limit"`
}

func readSQLBody(r *http.Request) (sqlBody, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return sqlBody{}, apperrors.BadRequest("读取请求内容失败：%s", err.Error())
	}
	var body sqlBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return sqlBody{}, apperrors.BadRequest("请求体不是合法 JSON：%s", err.Error())
	}
	return body, nil
}

func (a *datasetReadAPI) run(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	body, err := readSQLBody(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	userID := 0
	if u := security.UserFrom(r.Context()); u != nil {
		userID = u.ID
	}
	svc := ai.NewService(a.deps.Config, a.deps.Log, a.deps.DB, a.deps.Cache, a.deps.Mgr)
	out, err := ai.RunDatasetSQL(registry.WithUserID(r.Context(), userID), svc, datasetID, body.SQL, body.Limit)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out})
}
