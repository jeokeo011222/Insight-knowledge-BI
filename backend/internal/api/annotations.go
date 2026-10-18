package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterAnnotations mounts /api/v1/annotation_layer/* and /api/v1/annotation/*.
func RegisterAnnotations(r chi.Router, deps *Deps) {
	a := &annotationAPI{deps: deps}

	r.Route("/annotation_layer", func(ar chi.Router) {
		ar.Use(security.RequireUser)

		ar.With(security.RequirePermission(security.ActionRead, security.ResAnnotationLayer)).Get("/", a.listLayers)
		ar.With(security.RequirePermission(security.ActionWrite, security.ResAnnotationLayer)).Post("/", a.createLayer)
		ar.With(security.RequirePermission(security.ActionRead, security.ResAnnotationLayer)).Get("/{pk}", a.getLayer)
		ar.With(security.RequirePermission(security.ActionWrite, security.ResAnnotationLayer)).Put("/{pk}", a.updateLayer)
		ar.With(security.RequirePermission(security.ActionDelete, security.ResAnnotationLayer)).Delete("/{pk}", a.deleteLayer)

		ar.With(security.RequirePermission(security.ActionRead, security.ResAnnotation)).Get("/{pk}/annotation/", a.listLayerAnnotations)
		ar.With(security.RequirePermission(security.ActionWrite, security.ResAnnotation)).Post("/{pk}/annotation/", a.createLayerAnnotation)
	})

	r.Route("/annotation", func(ar chi.Router) {
		ar.Use(security.RequireUser)

		ar.With(security.RequirePermission(security.ActionRead, security.ResAnnotation)).Get("/", a.listAnnotations)
		ar.With(security.RequirePermission(security.ActionWrite, security.ResAnnotation)).Post("/", a.createAnnotation)
		ar.With(security.RequirePermission(security.ActionRead, security.ResAnnotation)).Get("/{pk}", a.getAnnotation)
		ar.With(security.RequirePermission(security.ActionWrite, security.ResAnnotation)).Put("/{pk}", a.updateAnnotation)
		ar.With(security.RequirePermission(security.ActionDelete, security.ResAnnotation)).Delete("/{pk}", a.deleteAnnotation)
	})
}

type annotationAPI struct{ deps *Deps }

type annotationLayerPayload struct {
	Name    string `json:"name"`
	Descr   string `json:"descr"`
	Color   string `json:"color"`
	Opacity string `json:"opacity"`
}

type annotationPayload struct {
	ShortDescr   string `json:"short_descr"`
	LongDescr    string `json:"long_descr"`
	LayerID      int    `json:"layer_id"`
	StartDttm    string `json:"start_dttm"`
	EndDttm      string `json:"end_dttm"`
	JSONMetadata any    `json:"json_metadata"`
}

// annotationLayerJSON renders a layer. The metadata schema has no color or
// opacity columns, so both are reported as null.
func annotationLayerJSON(l *models.AnnotationLayer) map[string]any {
	return map[string]any{
		"id": l.ID, "uuid": l.UUID, "name": l.Name, "descr": l.Descr,
		"color": nil, "opacity": nil,
		"created_on": l.CreatedOn, "changed_on": l.ChangedOn,
	}
}

func annotationJSON(a *models.Annotation) map[string]any {
	return map[string]any{
		"id": a.ID, "uuid": a.UUID, "short_descr": a.ShortDescr,
		"long_descr": a.LongDescr, "layer_id": a.LayerID,
		"start_dttm": a.StartDttm, "end_dttm": a.EndDttm,
		"json_metadata": jsonField([]byte(a.JSONMetadata)),
		"created_on":    a.CreatedOn, "changed_on": a.ChangedOn,
	}
}

// annotationParseTime accepts the date formats used by the frontend.
func annotationParseTime(raw string) *time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02T15:04:05", "2006-01-02T15:04",
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// Layers ------------------------------------------------------------------

func (a *annotationAPI) listLayers(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.AnnotationLayer{})
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("name ILIKE ? OR descr ILIKE ?", like, like)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "name asc", func(l *models.AnnotationLayer) any { return annotationLayerJSON(l) })
}

func (a *annotationAPI) findLayer(r *http.Request) (*models.AnnotationLayer, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var l models.AnnotationLayer
	if err := a.deps.DB.WithContext(r.Context()).First(&l, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("Annotation layer not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &l, nil
}

func (a *annotationAPI) getLayer(w http.ResponseWriter, r *http.Request) {
	l, err := a.findLayer(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": annotationLayerJSON(l)})
}

func (a *annotationAPI) createLayer(w http.ResponseWriter, r *http.Request) {
	var p annotationLayerPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.Name == "" {
		server.WriteError(w, apperrors.BadRequest("name is required"))
		return
	}
	l := models.AnnotationLayer{UUID: uuid.NewString(), Name: p.Name, Descr: p.Descr}
	if err := a.deps.DB.WithContext(r.Context()).Create(&l).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create annotation layer: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": annotationLayerJSON(&l)})
}

func (a *annotationAPI) updateLayer(w http.ResponseWriter, r *http.Request) {
	l, err := a.findLayer(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p annotationLayerPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.Name != "" {
		updates["name"] = p.Name
	}
	if p.Descr != "" {
		updates["descr"] = p.Descr
	}
	if err := a.deps.DB.WithContext(r.Context()).Model(l).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update annotation layer: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(l, l.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": annotationLayerJSON(l)})
}

func (a *annotationAPI) deleteLayer(w http.ResponseWriter, r *http.Request) {
	l, err := a.findLayer(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	if err := db.Where("layer_id = ?", l.ID).Delete(&models.Annotation{}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	if err := db.Delete(l).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Annotations -------------------------------------------------------------

func (a *annotationAPI) listLayerAnnotations(w http.ResponseWriter, r *http.Request) {
	l, err := a.findLayer(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.Annotation{}).Where("layer_id = ?", l.ID)
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "start_dttm desc", func(x *models.Annotation) any { return annotationJSON(x) })
}

func (a *annotationAPI) listAnnotations(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.Annotation{})
	if layerID := server.QueryInt(r, "layer_id", 0); layerID > 0 {
		q = q.Where("layer_id = ?", layerID)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "start_dttm desc", func(x *models.Annotation) any { return annotationJSON(x) })
}

func (a *annotationAPI) findAnnotation(r *http.Request) (*models.Annotation, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var x models.Annotation
	if err := a.deps.DB.WithContext(r.Context()).First(&x, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("Annotation not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &x, nil
}

func (a *annotationAPI) getAnnotation(w http.ResponseWriter, r *http.Request) {
	x, err := a.findAnnotation(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": annotationJSON(x)})
}

func (a *annotationAPI) createLayerAnnotation(w http.ResponseWriter, r *http.Request) {
	l, err := a.findLayer(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p annotationPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	p.LayerID = l.ID
	a.insertAnnotation(w, r, &p)
}

func (a *annotationAPI) createAnnotation(w http.ResponseWriter, r *http.Request) {
	var p annotationPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.LayerID == 0 {
		server.WriteError(w, apperrors.BadRequest("layer_id is required"))
		return
	}
	var l models.AnnotationLayer
	if err := a.deps.DB.WithContext(r.Context()).First(&l, p.LayerID).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Annotation layer not found"))
		return
	}
	a.insertAnnotation(w, r, &p)
}

func (a *annotationAPI) insertAnnotation(w http.ResponseWriter, r *http.Request, p *annotationPayload) {
	if p.ShortDescr == "" {
		server.WriteError(w, apperrors.BadRequest("short_descr is required"))
		return
	}
	x := models.Annotation{
		UUID: uuid.NewString(), ShortDescr: p.ShortDescr, LongDescr: p.LongDescr,
		LayerID: p.LayerID, StartDttm: annotationParseTime(p.StartDttm),
		EndDttm: annotationParseTime(p.EndDttm), JSONMetadata: jsonString(p.JSONMetadata),
	}
	if err := a.deps.DB.WithContext(r.Context()).Create(&x).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create annotation: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": annotationJSON(&x)})
}

func (a *annotationAPI) updateAnnotation(w http.ResponseWriter, r *http.Request) {
	x, err := a.findAnnotation(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p annotationPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.ShortDescr != "" {
		updates["short_descr"] = p.ShortDescr
	}
	if p.LongDescr != "" {
		updates["long_descr"] = p.LongDescr
	}
	if p.LayerID > 0 {
		updates["layer_id"] = p.LayerID
	}
	if p.StartDttm != "" {
		updates["start_dttm"] = annotationParseTime(p.StartDttm)
	}
	if p.EndDttm != "" {
		updates["end_dttm"] = annotationParseTime(p.EndDttm)
	}
	if p.JSONMetadata != nil {
		updates["json_metadata"] = jsonString(p.JSONMetadata)
	}
	if err := a.deps.DB.WithContext(r.Context()).Model(x).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update annotation: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(x, x.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": annotationJSON(x)})
}

func (a *annotationAPI) deleteAnnotation(w http.ResponseWriter, r *http.Request) {
	x, err := a.findAnnotation(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.deps.DB.WithContext(r.Context()).Delete(x).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}
