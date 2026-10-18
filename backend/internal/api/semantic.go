package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/queries"
	"superset/internal/security"
	"superset/internal/semantic"
	"superset/internal/server"
)

// semantic.go mounts the MDL-lite surface (benchmark W-05/W-07/W-08).
//
// Everything hangs off a dataset on purpose. The retired metric dictionary page
// proved that a top-level menu entry for "governance" becomes an orphan: nobody
// opens it, because there is no task that starts there. Model editing belongs
// exactly where the user is already looking at columns.

type semanticAPI struct {
	deps *Deps
	svc  *semantic.Service
}

// RegisterSemantic mounts /api/v1/dataset/{pk}/semantic/* and /api/v1/semantic/*.
func RegisterSemantic(r chi.Router, deps *Deps) {
	a := &semanticAPI{deps: deps, svc: semantic.New(deps.DB, deps.Log)}

	r.Route("/dataset/{pk}/semantic", func(sr chi.Router) {
		sr.Use(security.RequireUser)
		sr.With(security.RequirePermission(security.ActionRead, security.ResSemanticLayer)).
			Get("/models", a.listModels)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Post("/models", a.createModel)
		// Cold start is a write (it creates a model) but is exposed under its own
		// verb so the UI can call it from an empty state without pretend-saving.
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Post("/cold-start", a.coldStart)
		sr.With(security.RequirePermission(security.ActionRead, security.ResSemanticLayer)).
			Get("/rules", a.listRules)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Post("/rules", a.createRule)
		sr.With(security.RequirePermission(security.ActionRead, security.ResSemanticLayer)).
			Get("/export", a.exportBundle)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Post("/import", a.importBundle)
		// Gap scan (W-07). Reading metadata is a read; sampling distinct values
		// is a read of *data*, so it only happens when ?sample=1 is explicit.
		sr.With(security.RequirePermission(security.ActionRead, security.ResSemanticLayer)).
			Get("/gaps", a.scanGaps)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Post("/gaps/apply", a.applyGap)
	})

	r.Route("/semantic", func(sr chi.Router) {
		sr.Use(security.RequireUser)
		sr.With(security.RequirePermission(security.ActionRead, security.ResSemanticLayer)).
			Get("/model/{id}", a.getModel)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Put("/model/{id}", a.updateModel)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Delete("/model/{id}", a.deleteModel)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Post("/model/{id}/publish", a.publishModel)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Post("/model/{id}/relationships", a.createRelationship)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Put("/relationship/{id}", a.updateRelationship)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Delete("/relationship/{id}", a.deleteRelationship)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Put("/rule/{id}", a.updateRule)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSemanticLayer)).
			Delete("/rule/{id}", a.deleteRule)
	})
}

func datasetIDParam(r *http.Request) (int, error) {
	id, err := strconv.Atoi(chi.URLParam(r, "pk"))
	if err != nil || id <= 0 {
		return 0, apperrors.BadRequest("数据集 id 不合法")
	}
	return id, nil
}

func idParam(r *http.Request) (int, error) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id <= 0 {
		return 0, apperrors.BadRequest("id 不合法")
	}
	return id, nil
}

// writeError / writeResult keep the handlers in this file short; the envelope
// ({result: ...}) matches every other endpoint so the frontend client is reused.
func writeError(w http.ResponseWriter, err error) {
	server.WriteError(w, err)
}

func writeResult(w http.ResponseWriter, v any) {
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": v})
}

func (a *semanticAPI) listModels(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	list, err := a.svc.ListModels(r.Context(), datasetID)
	if err != nil {
		writeError(w, err)
		return
	}
	if list == nil {
		list = []models.SemanticModel{}
	}
	writeResult(w, list)
}

func (a *semanticAPI) createModel(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in semantic.ModelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, apperrors.BadRequest("请求体格式错误：%s", err.Error()))
		return
	}
	in.DatasetID = datasetID
	view, err := a.svc.CreateModel(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, view)
}

func (a *semanticAPI) coldStart(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	force := strings.EqualFold(r.URL.Query().Get("force"), "true") || r.URL.Query().Get("force") == "1"
	view, err := a.svc.ColdStart(r.Context(), datasetID, force)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, view)
}

func (a *semanticAPI) getModel(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	view, err := a.svc.GetModel(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, view)
}

func (a *semanticAPI) updateModel(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in semantic.ModelInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, apperrors.BadRequest("请求体格式错误：%s", err.Error()))
		return
	}
	view, err := a.svc.UpdateModel(r.Context(), id, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, view)
}

func (a *semanticAPI) deleteModel(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.svc.DeleteModel(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, map[string]any{"ok": true})
}

func (a *semanticAPI) publishModel(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	view, err := a.svc.PublishModel(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, view)
}

func (a *semanticAPI) createRelationship(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in semantic.RelationshipInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, apperrors.BadRequest("请求体格式错误：%s", err.Error()))
		return
	}
	if in.FromModelID == 0 {
		in.FromModelID = id
	}
	rel, err := a.svc.CreateRelationship(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, rel)
}

func (a *semanticAPI) updateRelationship(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in semantic.RelationshipInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, apperrors.BadRequest("请求体格式错误：%s", err.Error()))
		return
	}
	rel, err := a.svc.UpdateRelationship(r.Context(), id, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, rel)
}

func (a *semanticAPI) deleteRelationship(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.svc.DeleteRelationship(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, map[string]any{"ok": true})
}

func (a *semanticAPI) listRules(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rules, err := a.svc.ListRules(r.Context(), datasetID)
	if err != nil {
		writeError(w, err)
		return
	}
	if rules == nil {
		rules = []models.SemanticRule{}
	}
	writeResult(w, rules)
}

func (a *semanticAPI) createRule(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in semantic.RuleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, apperrors.BadRequest("请求体格式错误：%s", err.Error()))
		return
	}
	if in.DatasetID == 0 {
		in.DatasetID = datasetID
	}
	rule, err := a.svc.CreateRule(r.Context(), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, rule)
}

func (a *semanticAPI) updateRule(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var in semantic.RuleInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, apperrors.BadRequest("请求体格式错误：%s", err.Error()))
		return
	}
	rule, err := a.svc.UpdateRule(r.Context(), id, in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, rule)
}

func (a *semanticAPI) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := a.svc.DeleteRule(r.Context(), id); err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, map[string]any{"ok": true})
}

// exportBundle streams the manifest as JSON or YAML. The filename is set so the
// browser saves something meaningful into git.
func (a *semanticAPI) exportBundle(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format == "" {
		format = "json"
	}
	body, err := a.svc.Export(r.Context(), datasetID, format)
	if err != nil {
		writeError(w, err)
		return
	}
	ext := format
	if ext == "yaml" {
		ext = "yml"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition",
		"attachment; filename=\"semantic-model-"+strconv.Itoa(datasetID)+"."+ext+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

func (a *semanticAPI) importBundle(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeError(w, apperrors.BadRequest("读取上传内容失败：%s", err.Error()))
		return
	}
	format := strings.ToLower(r.URL.Query().Get("format"))
	res, err := a.svc.Import(r.Context(), datasetID, string(raw), format)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, res)
}

// scanGaps runs the W-07 gap scanner. Sampling is opt-in (`?sample=1`) and the
// sampler is wired here rather than inside the semantic package so the package
// stays free of query-engine and permission concerns.
func (a *semanticAPI) scanGaps(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sample := r.URL.Query().Get("sample") == "1" || strings.EqualFold(r.URL.Query().Get("sample"), "true")
	scanner := semantic.NewScanner(a.deps.DB, a.deps.Log)
	if sample {
		scanner.Sample = a.sampleColumn
	}
	report, err := scanner.Scan(r.Context(), datasetID, sample)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, report)
}

// sampleColumn reads up to n distinct values of one column through the shared
// read-only path, so RLS, row limits and masking all apply.
func (a *semanticAPI) sampleColumn(ctx context.Context, datasetID int, column string, n int) ([]string, error) {
	if a.deps.DB == nil {
		return nil, nil
	}
	qo := &queries.QueryObject{
		Metrics:  []any{queries.AdhocMetric{SQLExpression: "COUNT(*)", Label: "count", HasCustomLabel: true}},
		Groupby:  []any{queries.AdhocColumn{ColumnName: column, Label: column, HasCustomLabel: true}},
		RowLimit: n + 1,
	}
	built, _, err := queries.BuildDatasetSQL(ctx, a.deps.DB, datasetID, qo, queries.BuildOptions{
		DefaultRowLimit: n + 1, MaxRow: n + 1,
	})
	if err != nil {
		return nil, err
	}
	exec := queries.NewExecutor(a.deps.DB, a.deps.Log, a.deps.Cache, a.deps.Mgr)
	res, err := exec.ExecuteReadOnlySQL(ctx, 0, built.SQL, queries.ReadOnlyOptions{
		DatasetID:     datasetID,
		RowLimit:      n + 1,
		ExemptMasking: true,
	})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Data))
	for _, row := range res.Data {
		if v, ok := row[column]; ok && v != nil {
			out = append(out, fmt.Sprint(v))
		}
	}
	return out, nil
}

func (a *semanticAPI) applyGap(w http.ResponseWriter, r *http.Request) {
	datasetID, err := datasetIDParam(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var body struct {
		GapID string `json:"gap_id"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, apperrors.BadRequest("请求体格式错误：%s", err.Error()))
		return
	}
	scanner := semantic.NewScanner(a.deps.DB, a.deps.Log)
	out, err := scanner.ApplyGap(r.Context(), datasetID, body.GapID, body.Value)
	if err != nil {
		writeError(w, err)
		return
	}
	writeResult(w, out)
}
