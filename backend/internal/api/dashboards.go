package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
	"superset/internal/thumbnails"
)

// RegisterDashboards mounts /api/v1/dashboard/*.
func RegisterDashboards(r chi.Router, deps *Deps) {
	d := &dashboardAPI{deps: deps}

	r.Route("/dashboard", func(dr chi.Router) {
		dr.Use(security.RequireUser)

		dr.With(security.RequirePermission(security.ActionRead, security.ResDashboard)).Get("/", d.list)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDashboard)).Post("/", d.create)
		dr.Get("/_info", d.info)
		dr.Post("/import/", d.importDashboards)

		dr.Get("/{pk}", d.get)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDashboard)).Put("/{pk}", d.update)
		dr.With(security.RequirePermission(security.ActionDelete, security.ResDashboard)).Delete("/{pk}", d.remove)
		dr.With(security.RequirePermission(security.ActionCopyDash, security.ResDashboard)).Post("/{pk}/copy/", d.copyDashboard)

		dr.Get("/{pk}/charts", d.charts)
		dr.Get("/{pk}/datasets", d.datasets)

		dr.Get("/{pk}/filter_state", d.getFilterState)
		dr.Post("/{pk}/filter_state", d.setFilterState)
		dr.Delete("/{pk}/filter_state/{key}", d.deleteFilterState)

		dr.Get("/{pk}/permalink/{key}", d.getPermalink)
		dr.Post("/{pk}/permalink/", d.createPermalink)
		dr.Delete("/{pk}/permalink/{key}", d.deletePermalink)

		dr.With(security.RequirePermission(security.ActionExport, security.ResDashboard)).Get("/{pk}/export/", d.export)
		dr.With(security.RequirePermission(security.ActionExport, security.ResDashboard)).Get("/{pk}/export_image/", d.exportImage)
		dr.With(security.RequirePermission(security.ActionExport, security.ResDashboard)).Get("/{pk}/export_image", d.exportImage)
		dr.Get("/{pk}/thumbnail/{digest}/", d.thumbnail)
		dr.Get("/{pk}/thumbnail/{digest}", d.thumbnail)
		dr.Get("/{pk}/cache_screenshot/", d.cacheScreenshot)
		dr.Get("/{pk}/cache_screenshot", d.cacheScreenshot)
		dr.Get("/{pk}/embedded", d.embedded)
		dr.Post("/{pk}/favorites/", d.favorite)
	})
}

type dashboardAPI struct{ deps *Deps }

// Payloads -----------------------------------------------------------------

type dashboardPayload struct {
	DashboardTitle       string `json:"dashboard_title"`
	Slug                 string `json:"slug"`
	Published            *bool  `json:"published"`
	PositionJSON         any    `json:"position_json"`
	JSONMetadata         any    `json:"json_metadata"`
	CSS                  string `json:"css"`
	Description          string `json:"description"`
	CertifiedBy          string `json:"certified_by"`
	CertificationDetails string `json:"certification_details"`
	Owners               []int  `json:"owners"`
}

type dashboardUpdatePayload struct {
	DashboardTitle       *string `json:"dashboard_title"`
	Slug                 *string `json:"slug"`
	Published            *bool   `json:"published"`
	PositionJSON         any     `json:"position_json"`
	JSONMetadata         any     `json:"json_metadata"`
	CSS                  *string `json:"css"`
	Description          *string `json:"description"`
	CertifiedBy          *string `json:"certified_by"`
	CertificationDetails *string `json:"certification_details"`
	Owners               []int   `json:"owners"`
}

// dashContext carries the batched associations used when rendering dashboards.
type dashContext struct {
	owners map[int][]map[string]any
	tags   map[int][]map[string]any
	counts map[int]int
	users  map[int]string
}

func newDashContext() *dashContext {
	return &dashContext{
		owners: map[int][]map[string]any{},
		tags:   map[int][]map[string]any{},
		counts: map[int]int{},
		users:  map[int]string{},
	}
}

// List ---------------------------------------------------------------------

func (d *dashboardAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := d.deps.DB.WithContext(r.Context()).Model(&models.Dashboard{}).Where("deleted_at IS NULL")
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("dashboard_title ILIKE ? OR slug ILIKE ? OR description ILIKE ?", like, like, like)
	}
	if slug := server.QueryStr(r, "slug"); slug != "" {
		q = q.Where("slug = ?", slug)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}

	order := orderClause(p, map[string]string{
		"id": "id", "dashboard_title": "dashboard_title", "slug": "slug",
		"changed_on": "changed_on", "created_on": "created_on", "published": "published",
	}, "dashboard_title")
	var rows []models.Dashboard
	if err := q.Order(order).Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}

	ids := make([]int, 0, len(rows))
	userIDs := make([]int, 0, len(rows))
	for _, dm := range rows {
		ids = append(ids, dm.ID)
		if dm.ChangedByFk != nil {
			userIDs = append(userIDs, *dm.ChangedByFk)
		}
	}
	ctx := d.buildContext(r, ids, userIDs)

	total := len(rows)
	start, end := sharedPageBounds(p, total)
	out := make([]any, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, d.dashboardJSON(r, &rows[i], ctx))
	}
	server.WritePaginated(w, int64(total), out, nil, nil)
}

// buildContext loads owners, tags, chart counts and changed_by usernames.
func (d *dashboardAPI) buildContext(r *http.Request, ids []int, userIDs []int) *dashContext {
	ctx := newDashContext()
	if len(ids) == 0 {
		return ctx
	}
	db := d.deps.DB.WithContext(r.Context())

	var ownerRows []struct {
		DashboardID int
		ID          int
		Username    string
		FirstName   string
		LastName    string
	}
	_ = db.Raw(`
		SELECT du.dashboard_id AS dashboard_id, u.id AS id, u.username AS username,
		       u.first_name AS first_name, u.last_name AS last_name
		FROM dashboard_user du
		JOIN ab_user u ON u.id = du.user_id
		WHERE du.dashboard_id IN ?`, ids).Scan(&ownerRows).Error
	for _, o := range ownerRows {
		ctx.owners[o.DashboardID] = append(ctx.owners[o.DashboardID], map[string]any{
			"id": o.ID, "username": o.Username,
			"first_name": o.FirstName, "last_name": o.LastName,
		})
	}

	var tagRows []struct {
		ObjectID int
		ID       int
		Name     string
		Type     int
	}
	_ = db.Raw(`
		SELECT t.object_id AS object_id, tag.id AS id, tag.name AS name, tag.type AS type
		FROM tagged_object t
		JOIN tag ON tag.id = t.tag_id
		WHERE t.object_type = 3 AND t.object_id IN ?`, ids).Scan(&tagRows).Error
	for _, t := range tagRows {
		ctx.tags[t.ObjectID] = append(ctx.tags[t.ObjectID], map[string]any{
			"id": t.ID, "name": t.Name, "type": t.Type,
		})
	}

	var countRows []struct {
		DashboardID int
		Total       int
	}
	_ = db.Raw(`
		SELECT dashboard_id, COUNT(*) AS total FROM dashboard_slices
		WHERE dashboard_id IN ? GROUP BY dashboard_id`, ids).Scan(&countRows).Error
	for _, c := range countRows {
		ctx.counts[c.DashboardID] = c.Total
	}

	if len(userIDs) > 0 {
		var users []struct {
			ID        int
			FirstName string
			LastName  string
			Username  string
		}
		_ = db.Raw(`SELECT id, first_name, last_name, username FROM ab_user WHERE id IN ?`, userIDs).
			Scan(&users).Error
		for _, u := range users {
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			if name == "" {
				name = u.Username
			}
			ctx.users[u.ID] = name
		}
	}
	return ctx
}

// dashboardJSON renders the superset dashboard response shape.
func (d *dashboardAPI) dashboardJSON(r *http.Request, dm *models.Dashboard, ctx *dashContext) map[string]any {
	if ctx == nil {
		userIDs := []int{}
		if dm.ChangedByFk != nil {
			userIDs = append(userIDs, *dm.ChangedByFk)
		}
		ctx = d.buildContext(r, []int{dm.ID}, userIDs)
	}
	owners := ctx.owners[dm.ID]
	if owners == nil {
		owners = []map[string]any{}
	}
	tags := ctx.tags[dm.ID]
	if tags == nil {
		tags = []map[string]any{}
	}
	status := "draft"
	if dm.Published {
		status = "published"
	}
	changedByName := ""
	var changedBy any
	if dm.ChangedByFk != nil {
		changedByName = ctx.users[*dm.ChangedByFk]
		changedBy = map[string]any{"id": *dm.ChangedByFk, "name": changedByName}
	}
	return map[string]any{
		"id":                         dm.ID,
		"uuid":                       dm.UUID,
		"dashboard_title":            dm.DashboardTitle,
		"slug":                       dm.Slug,
		"published":                  dm.Published,
		"status":                     status,
		"description":                nullIfEmpty(dm.Description),
		"position_json":              jsonField(dm.PositionJSON),
		"json_metadata":              jsonField(dm.JSONMetadata),
		"css":                        dm.CSS,
		"owners":                     owners,
		"changed_by":                 changedBy,
		"changed_by_name":            nullIfEmpty(changedByName),
		"changed_on":                 dm.ChangedOn,
		"changed_on_delta_humanized": dashHumanize(dm.ChangedOn),
		"created_on":                 dm.CreatedOn,
		"created_by":                 nil,
		"certified_by":               nullIfEmpty(dm.CertifiedBy),
		"certification_details":      nullIfEmpty(dm.CertificationDetails),
		"thumbnail_url":              dashThumbnailURL(dm),
		"url":                        dashURL(dm),
		"tags":                       tags,
		"chart_count":                ctx.counts[dm.ID],
		"is_managed_externally":      dm.IsManagedExternally,
		"external_url":               nullIfEmpty(dm.ExternalURL),
	}
}

// dashURL returns the SPA path for a dashboard (slug wins when present).
func dashURL(dm *models.Dashboard) string {
	if dm.Slug != "" {
		return "/superset/dashboard/" + dm.Slug + "/"
	}
	return fmt.Sprintf("/superset/dashboard/%d/", dm.ID)
}

// dashThumbnailURL builds the stable thumbnail path for a dashboard.
func dashThumbnailURL(dm *models.Dashboard) string {
	return fmt.Sprintf("/api/v1/dashboard/%d/thumbnail/%s/", dm.ID, dashDigest(dm))
}

// dashDigest fingerprints the dashboard layout for thumbnail caching.
func dashDigest(dm *models.Dashboard) string {
	h := sha256.New()
	_, _ = h.Write(dm.PositionJSON)
	_, _ = h.Write([]byte(dm.ChangedOn.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// dashHumanize renders a coarse "x ago" string like FAB does.
func dashHumanize(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	delta := time.Since(t)
	if delta < 0 {
		return "just now"
	}
	switch {
	case delta < time.Minute:
		return "now"
	case delta < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(delta.Minutes()))
	case delta < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(delta.Hours()))
	case delta < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(delta.Hours()/24))
	case delta < 365*24*time.Hour:
		return fmt.Sprintf("%d months ago", int(delta.Hours()/(24*30)))
	}
	return fmt.Sprintf("%d years ago", int(delta.Hours()/(24*365)))
}

// CRUD ---------------------------------------------------------------------

func (d *dashboardAPI) info(w http.ResponseWriter, r *http.Request) {
	q := d.deps.DB.WithContext(r.Context()).Model(&models.Dashboard{}).Where("deleted_at IS NULL")
	if ids := idsParam(r); len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	var rows []models.Dashboard
	if err := q.Order("dashboard_title").Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error"))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, dm := range rows {
		out = append(out, map[string]any{
			"id": dm.ID, "text": dm.DashboardTitle,
			"dashboard_title": dm.DashboardTitle, "slug": dm.Slug,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"count": len(out), "result": out})
}

func (d *dashboardAPI) get(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": d.dashboardJSON(r, dm, nil)})
}

func (d *dashboardAPI) create(w http.ResponseWriter, r *http.Request) {
	var p dashboardPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(p.DashboardTitle) == "" {
		server.WriteError(w, apperrors.BadRequest("dashboard_title is required"))
		return
	}
	if p.Slug != "" {
		if d.slugTaken(r, p.Slug, 0) {
			server.WriteError(w, apperrors.Conflict("Slug %q is already in use", p.Slug))
			return
		}
	}
	dm := models.Dashboard{
		UUID:                 uuid.NewString(),
		DashboardTitle:       p.DashboardTitle,
		Slug:                 p.Slug,
		Published:            boolOr(p.Published, false),
		PositionJSON:         sharedJSONColumn(p.PositionJSON, "{}"),
		JSONMetadata:         sharedJSONColumn(p.JSONMetadata, "{}"),
		CSS:                  p.CSS,
		Description:          p.Description,
		CertifiedBy:          p.CertifiedBy,
		CertificationDetails: p.CertificationDetails,
	}
	err := d.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&dm).Error; err != nil {
			return err
		}
		owners := p.Owners
		if len(owners) == 0 {
			if uid := userIDOf(r); uid > 0 {
				owners = []int{uid}
			}
		}
		for _, uid := range owners {
			if err := tx.Create(&models.DashboardUser{UserID: uid, DashboardID: dm.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create dashboard: %s", err.Error()))
		return
	}
	_ = d.deps.DB.WithContext(r.Context()).First(&dm, dm.ID).Error
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": d.dashboardJSON(r, &dm, nil)})
}

func (d *dashboardAPI) update(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := d.requireOwnership(r, dm); err != nil {
		server.WriteError(w, err)
		return
	}
	var p dashboardUpdatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}

	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.DashboardTitle != nil {
		updates["dashboard_title"] = *p.DashboardTitle
	}
	if p.Slug != nil {
		if *p.Slug != "" && d.slugTaken(r, *p.Slug, dm.ID) {
			server.WriteError(w, apperrors.Conflict("Slug %q is already in use", *p.Slug))
			return
		}
		updates["slug"] = *p.Slug
	}
	if p.Published != nil {
		updates["published"] = *p.Published
	}
	if p.PositionJSON != nil {
		updates["position_json"] = string(sharedJSONColumn(p.PositionJSON, "{}"))
	}
	if p.JSONMetadata != nil {
		updates["json_metadata"] = string(sharedJSONColumn(p.JSONMetadata, "{}"))
	}
	if p.CSS != nil {
		updates["css"] = *p.CSS
	}
	if p.Description != nil {
		updates["description"] = *p.Description
	}
	if p.CertifiedBy != nil {
		updates["certified_by"] = *p.CertifiedBy
	}
	if p.CertificationDetails != nil {
		updates["certification_details"] = *p.CertificationDetails
	}

	db := d.deps.DB.WithContext(r.Context())
	if err := db.Model(dm).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update dashboard: %s", err.Error()))
		return
	}
	if p.Owners != nil {
		db.Where("dashboard_id = ?", dm.ID).Delete(&models.DashboardUser{})
		for _, uid := range p.Owners {
			db.Create(&models.DashboardUser{UserID: uid, DashboardID: dm.ID})
		}
	}
	_ = db.First(dm, dm.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": d.dashboardJSON(r, dm, nil)})
}

func (d *dashboardAPI) remove(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := d.requireOwnership(r, dm); err != nil {
		server.WriteError(w, err)
		return
	}
	db := d.deps.DB.WithContext(r.Context())
	err = db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Model(&models.Dashboard{}).Where("id = ?", dm.ID).
			Updates(map[string]any{"deleted_at": &now}).Error; err != nil {
			return err
		}
		if err := tx.Where("dashboard_id = ?", dm.ID).Delete(&models.DashboardSlices{}).Error; err != nil {
			return err
		}
		return tx.Where("dashboard_id = ?", dm.ID).Delete(&models.DashboardUser{}).Error
	})
	if err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// copyDashboard duplicates the dashboard and its chart associations.
func (d *dashboardAPI) copyDashboard(w http.ResponseWriter, r *http.Request) {
	src, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := d.requireOwnership(r, src); err != nil {
		server.WriteError(w, err)
		return
	}
	var body struct {
		DashboardTitle string `json:"dashboard_title"`
		Slug           string `json:"slug"`
	}
	_ = security.DecodeBody(r, &body)
	title := body.DashboardTitle
	if title == "" {
		title = src.DashboardTitle + " (Copy)"
	}

	clone := *src
	clone.ID = 0
	clone.UUID = uuid.NewString()
	clone.DashboardTitle = title
	clone.Slug = body.Slug
	clone.CreatedOn = time.Time{}
	clone.ChangedOn = time.Time{}
	clone.Published = false
	clone.DeletedAt = nil
	if clone.Slug != "" && d.slugTaken(r, clone.Slug, 0) {
		server.WriteError(w, apperrors.Conflict("Slug %q is already in use", clone.Slug))
		return
	}

	db := d.deps.DB.WithContext(r.Context())
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&clone).Error; err != nil {
			return err
		}
		var links []models.DashboardSlices
		if err := tx.Where("dashboard_id = ?", src.ID).Find(&links).Error; err != nil {
			return err
		}
		for _, link := range links {
			if err := tx.Create(&models.DashboardSlices{DashboardID: clone.ID, SliceID: link.SliceID}).Error; err != nil {
				return err
			}
		}
		var owners []models.DashboardUser
		if err := tx.Where("dashboard_id = ?", src.ID).Find(&owners).Error; err != nil {
			return err
		}
		for _, o := range owners {
			if err := tx.Create(&models.DashboardUser{UserID: o.UserID, DashboardID: clone.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("Could not copy dashboard: %s", err.Error()))
		return
	}
	_ = db.First(&clone, clone.ID).Error
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": d.dashboardJSON(r, &clone, nil)})
}

// Charts / datasets --------------------------------------------------------

// charts returns every chart placed on the dashboard with full params.
func (d *dashboardAPI) charts(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var rows []models.Slice
	err = d.deps.DB.WithContext(r.Context()).Raw(`
		SELECT s.* FROM slices s
		JOIN dashboard_slices ds ON ds.slice_id = s.id
		WHERE ds.dashboard_id = ? AND s.deleted_at IS NULL
		ORDER BY s.slice_name`, dm.ID).Scan(&rows).Error
	if err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		s := &rows[i]
		params := jsonMap(s.Params)
		out = append(out, map[string]any{
			"id":                    s.ID,
			"uuid":                  s.UUID,
			"slice_id":              s.ID,
			"slice_name":            s.SliceName,
			"viz_type":              s.VizType,
			"datasource_id":         s.DatasourceID,
			"datasource_type":       s.DatasourceType,
			"datasource_name":       s.DatasourceName,
			"params":                params,
			"form_data":             params,
			"query_context":         jsonField(s.QueryContext),
			"description":           nullIfEmpty(s.Description),
			"cache_timeout":         s.CacheTimeout,
			"certified_by":          nullIfEmpty(s.CertifiedBy),
			"certification_details": nullIfEmpty(s.CertificationDetails),
			"slice_url":             fmt.Sprintf("/explore/?slice_id=%d", s.ID),
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": out,
		"count":  len(out),
		"ids":    dashSliceIDs(rows),
		"metadata": map[string]any{
			"dashboard_id":    dm.ID,
			"dashboard_title": dm.DashboardTitle,
		},
	})
}

func dashSliceIDs(rows []models.Slice) []int {
	out := make([]int, 0, len(rows))
	for _, s := range rows {
		out = append(out, s.ID)
	}
	return out
}

// datasets returns the distinct datasets used by the dashboard's charts.
func (d *dashboardAPI) datasets(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var rows []struct {
		ID           int
		TableName    string
		Schema       string
		DatabaseID   int
		DatabaseName string
		Description  string
	}
	err = d.deps.DB.WithContext(r.Context()).Raw(`
		SELECT DISTINCT t.id AS id, t.table_name AS table_name, t.schema AS schema,
		       t.database_id AS database_id, db.database_name AS database_name,
		       t.description AS description
		FROM tables t
		JOIN slices s ON s.datasource_id = t.id AND s.datasource_type = 'table'
		JOIN dashboard_slices ds ON ds.slice_id = s.id
		JOIN dbs db ON db.id = t.database_id
		WHERE ds.dashboard_id = ? AND t.deleted_at IS NULL
		ORDER BY t.table_name`, dm.ID).Scan(&rows).Error
	if err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id": row.ID, "table_name": row.TableName, "schema": row.Schema,
			"database_id": row.DatabaseID, "database_name": row.DatabaseName,
			"description":     nullIfEmpty(row.Description),
			"datasource_type": "table", "kind": "physical",
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out, "count": len(out)})
}

// Filter state -------------------------------------------------------------

func (d *dashboardAPI) getFilterState(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	uid := userIDOf(r)
	entry, ok, err := d.kvGet(r, dashFilterStateKey(dm.ID, uid))
	if err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	result := []any{}
	if ok {
		result = append(result, map[string]any{
			"id":    entry.Key,
			"value": entry.Value,
			"label": entry.Label,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": result, "count": len(result)})
}

func (d *dashboardAPI) setFilterState(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var body map[string]any
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	uid := userIDOf(r)
	key := dashFilterStateKey(dm.ID, uid)
	entry := dashKVEntry{
		Key:         key,
		DashboardID: dm.ID,
		UserID:      uid,
		Label:       firstMapString(body, "label"),
		Value:       body,
		CreatedOn:   time.Now().UTC(),
	}
	if err := d.kvPut(r, entry); err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": map[string]any{"id": key, "value": body, "label": entry.Label},
	})
}

func (d *dashboardAPI) deleteFilterState(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if key == "" {
		server.WriteError(w, apperrors.BadRequest("Filter state key is required"))
		return
	}
	expected := dashFilterStateKey(dm.ID, userIDOf(r))
	if key != expected {
		server.WriteError(w, apperrors.NotFound("Filter state not found"))
		return
	}
	if err := d.kvDelete(r, key); err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Permalinks ---------------------------------------------------------------

func (d *dashboardAPI) getPermalink(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	entry, ok, err := d.kvGet(r, dashPermalinkKey(dm.ID, key))
	if err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	if !ok {
		server.WriteError(w, apperrors.NotFound("Permalink not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": map[string]any{"key": entry.Key, "value": entry.Value},
	})
}

func (d *dashboardAPI) createPermalink(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var body map[string]any
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	state := body
	if v, ok := body["state"]; ok {
		state = map[string]any{"state": v}
	}
	raw, err := json.Marshal(state)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("Invalid permalink payload"))
		return
	}
	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:])[:16]
	entry := dashKVEntry{
		Key:         key,
		DashboardID: dm.ID,
		UserID:      userIDOf(r),
		Value:       state,
		CreatedOn:   time.Now().UTC(),
	}
	if err := d.kvPut(r, entry); err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{
		"result": map[string]any{
			"key": key,
			"url": fmt.Sprintf("%s?permalink_key=%s", dashURL(dm), key),
		},
	})
}

func (d *dashboardAPI) deletePermalink(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	if err := d.kvDelete(r, dashPermalinkKey(dm.ID, key)); err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Embedded / favorites -----------------------------------------------------

func (d *dashboardAPI) embedded(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var row models.EmbeddedDashboard
	err = d.deps.DB.WithContext(r.Context()).Where("dashboard_id = ?", dm.ID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{}})
		return
	}
	if err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"uuid":              row.UUID,
		"dashboard_id":      row.DashboardID,
		"allow_domain_list": parseCSVList(row.AllowDomainList),
	}})
}

func (d *dashboardAPI) favorite(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	uid := userIDOf(r)
	if uid == 0 {
		server.WriteError(w, apperrors.Unauthorized("Unauthorized"))
		return
	}
	db := d.deps.DB.WithContext(r.Context())
	var fav models.FavStar
	err = db.Where("user_id = ? AND class_name = ? AND obj_id = ?", uid, "dashboard", dm.ID).First(&fav).Error
	switch {
	case err == nil:
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"result": map[string]any{"favorite_status": true},
		})
	case errors.Is(err, gorm.ErrRecordNotFound):
		now := time.Now().UTC()
		fav = models.FavStar{
			UUID: uuid.NewString(), UserID: uid, ClassName: "dashboard", ObjID: dm.ID, Dttm: &now,
		}
		if cerr := db.Create(&fav).Error; cerr != nil {
			server.WriteError(w, apperrors.Internal("Database error: %s", cerr.Error()))
			return
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"result": map[string]any{"favorite_status": true},
		})
	default:
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
	}
}

// Export / import / thumbnail ----------------------------------------------

func (d *dashboardAPI) export(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var charts []models.Slice
	if err := d.deps.DB.WithContext(r.Context()).Raw(`
		SELECT s.* FROM slices s
		JOIN dashboard_slices ds ON ds.slice_id = s.id
		WHERE ds.dashboard_id = ? AND s.deleted_at IS NULL`, dm.ID).Scan(&charts).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	chartList := make([]map[string]any, 0, len(charts))
	for i := range charts {
		chartList = append(chartList, map[string]any{
			"id": charts[i].ID, "uuid": charts[i].UUID,
			"slice_name": charts[i].SliceName, "viz_type": charts[i].VizType,
			"datasource_id": charts[i].DatasourceID, "datasource_type": charts[i].DatasourceType,
			"params": jsonField(charts[i].Params), "description": charts[i].Description,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=dashboard_%d_export.json", dm.ID))
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"__version": "1.0.0",
		"type":      "dashboard",
		"dashboard": d.dashboardJSON(r, dm, nil),
		"charts":    chartList,
	})
}

func (d *dashboardAPI) importDashboards(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dashboards []dashboardPayload `json:"dashboards"`
		Dashboard  *dashboardPayload  `json:"dashboard"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	payloads := body.Dashboards
	if body.Dashboard != nil {
		payloads = append(payloads, *body.Dashboard)
	}
	if len(payloads) == 0 {
		server.WriteError(w, apperrors.BadRequest("No dashboards to import"))
		return
	}
	db := d.deps.DB.WithContext(r.Context())
	created := make([]int, 0, len(payloads))
	for _, p := range payloads {
		title := strings.TrimSpace(p.DashboardTitle)
		if title == "" {
			continue
		}
		dm := models.Dashboard{
			UUID:                 uuid.NewString(),
			DashboardTitle:       title,
			Slug:                 p.Slug,
			Published:            boolOr(p.Published, false),
			PositionJSON:         sharedJSONColumn(p.PositionJSON, "{}"),
			JSONMetadata:         sharedJSONColumn(p.JSONMetadata, "{}"),
			CSS:                  p.CSS,
			Description:          p.Description,
			CertifiedBy:          p.CertifiedBy,
			CertificationDetails: p.CertificationDetails,
		}
		if err := db.Create(&dm).Error; err == nil {
			created = append(created, dm.ID)
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Imported %d dashboards", len(created)),
		"result":  created,
	})
}

func (d *dashboardAPI) thumbnail(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	key := thumbnailCacheKey("dashboard", dm.ID, chi.URLParam(r, "digest"), dashDigest(dm))
	digest, data, err := captureThumbnail(r.Context(), d.deps.Thumbs, key, requestAccessToken(r), false,
		func(token string) thumbnails.Target {
			return dashScreenshotTarget(d.deps.Thumbs.BaseURL(), dm.ID, token)
		})
	if err != nil {
		writeThumbnailError(w, err)
		return
	}
	writeThumbnail(w, r, digest, data)
}

// cacheScreenshot forces a fresh dashboard render and returns its URL.
func (d *dashboardAPI) cacheScreenshot(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	contentDigest := dashDigest(dm)
	key := fmt.Sprintf("dashboard:%d:%s", dm.ID, contentDigest)
	_, data, err := captureThumbnail(r.Context(), d.deps.Thumbs, key, requestAccessToken(r), true,
		func(token string) thumbnails.Target {
			return dashScreenshotTarget(d.deps.Thumbs.BaseURL(), dm.ID, token)
		})
	if err != nil {
		writeThumbnailError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"digest":    contentDigest,
		"image_url": fmt.Sprintf("/api/v1/dashboard/%d/thumbnail/%s/", dm.ID, contentDigest),
		"bytes":     len(data),
	})
}

// dashScreenshotTarget describes the headless render of a dashboard.
func dashScreenshotTarget(base string, id int, token string) thumbnails.Target {
	return thumbnails.Target{
		URL:           fmt.Sprintf("%s/dashboard/%d/", base, id),
		Selector:      ".dashboard-page, .chart-card, main",
		Width:         1600,
		Height:        1000,
		Token:         token,
		WaitSelectors: []string{".dashboard-page"},
	}
}

// exportImage serves GET /dashboard/{pk}/export_image?format=png|pdf.
//
// It is a *rendering* export, not a data export: a headless browser opens the
// board, waits for the tiles, and writes a PNG or a paginated PDF. The
// response is streamed rather than cached, because a print of a live dashboard
// must reflect the current filter state and never a stale thumbnail.
func (d *dashboardAPI) exportImage(w http.ResponseWriter, r *http.Request) {
	dm, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	format := strings.ToLower(strings.TrimSpace(server.QueryStr(r, "format")))
	if format == "" {
		format = "png"
	}
	width := clampInt(intQueryParam(r, "width", 1920), 640, 3840)
	height := clampInt(intQueryParam(r, "height", 1080), 480, 3840)

	base := strings.TrimRight(d.deps.Thumbs.BaseURL(), "/")
	target := thumbnails.Target{
		URL:           fmt.Sprintf("%s/dashboard/%d/", base, dm.ID),
		Selector:      ".dashboard-page, .chart-card, main",
		Width:         width,
		Height:        height,
		Token:         requestAccessToken(r),
		WaitSelectors: []string{".dashboard-page"},
		// The export is meant to be printed/shared, so the URL bar, sider and
		// agent launcher are dropped from the capture.
		HideSelectors: []string{".dashboard-toolbar", ".agent-panel__launcher", ".ai-fab"},
	}

	var data []byte
	var contentType string
	switch format {
	case "png":
		data, err = thumbnails.Capture(r.Context(), target)
		contentType = "image/png"
	case "pdf":
		data, err = thumbnails.CapturePDF(r.Context(), target, true)
		contentType = "application/pdf"
	default:
		server.WriteError(w, apperrors.BadRequest("Unsupported export format: %s", format))
		return
	}
	if err != nil {
		writeThumbnailError(w, err)
		return
	}

	name := dashExportFilename(dm, format)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Content-Disposition", name)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// dashExportFilename builds the Content-Disposition value for an export.
//
// ASCII is used for the plain `filename` (old clients) while the UTF-8 title
// rides along in the RFC 5987 `filename*` parameter, so a Chinese dashboard
// name survives the download instead of turning into hex escapes.
func dashExportFilename(dm *models.Dashboard, ext string) string {
	title := strings.TrimSpace(dm.DashboardTitle)
	if title == "" {
		title = strings.TrimSpace(dm.Slug)
	}
	if title == "" {
		title = fmt.Sprintf("dashboard-%d", dm.ID)
	}
	if len(title) > 80 {
		title = title[:80]
	}
	full := fmt.Sprintf("%s-%s.%s", title, time.Now().Format("20060102"), ext)

	var ascii strings.Builder
	for _, r := range full {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			ascii.WriteRune(r)
		case r == ' ':
			ascii.WriteRune('-')
		default:
			ascii.WriteRune('_')
		}
	}
	fallback := ascii.String()
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
		fallback, url.PathEscape(full))
}

// clampInt bounds v into [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// intQueryParam reads an integer query parameter, falling back to def when the
// value is missing or not a positive number.
func intQueryParam(r *http.Request, key string, def int) int {
	raw := strings.TrimSpace(server.QueryStr(r, key))
	if raw == "" {
		return def
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return def
	}
	return parsed
}

// Key/value storage --------------------------------------------------------

// dashKVEntry is the value stored in the keyvalue table for filter state and
// permalinks.
type dashKVEntry struct {
	Key         string    `json:"key"`
	DashboardID int       `json:"dashboard_id,omitempty"`
	UserID      int       `json:"user_id,omitempty"`
	Label       string    `json:"label,omitempty"`
	Value       any       `json:"value,omitempty"`
	CreatedOn   time.Time `json:"created_on"`
}

// dashKeyValueID maps a string key onto the keyvalue integer primary key.
func dashKeyValueID(key string) int {
	return int(crc32.ChecksumIEEE([]byte(key)) & 0x7fffffff)
}

func dashFilterStateKey(dashboardID, userID int) string {
	return fmt.Sprintf("filter_state:%d:%d", dashboardID, userID)
}

func dashPermalinkKey(dashboardID int, key string) string {
	return fmt.Sprintf("permalink:%d:%s", dashboardID, key)
}

// kvGet reads a JSON entry, verifying the embedded key when present.
func (d *dashboardAPI) kvGet(r *http.Request, key string) (*dashKVEntry, bool, error) {
	var row models.KeyValue
	err := d.deps.DB.WithContext(r.Context()).First(&row, dashKeyValueID(key)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var entry dashKVEntry
	if err := json.Unmarshal([]byte(row.Value), &entry); err != nil {
		return nil, false, nil
	}
	if entry.Key != key {
		return nil, false, nil
	}
	return &entry, true, nil
}

// kvPut upserts a JSON entry.
func (d *dashboardAPI) kvPut(r *http.Request, entry dashKVEntry) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	row := models.KeyValue{ID: dashKeyValueID(entry.Key), Value: string(raw)}
	return d.deps.DB.WithContext(r.Context()).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&row).Error
}

func (d *dashboardAPI) kvDelete(r *http.Request, key string) error {
	return d.deps.DB.WithContext(r.Context()).
		Where("id = ?", dashKeyValueID(key)).Delete(&models.KeyValue{}).Error
}

// Helpers ------------------------------------------------------------------

func (d *dashboardAPI) find(r *http.Request) (*models.Dashboard, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var dm models.Dashboard
	if err := d.deps.DB.WithContext(r.Context()).First(&dm, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("Dashboard not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	if dm.DeletedAt != nil {
		return nil, apperrors.NotFound("Dashboard not found")
	}
	return &dm, nil
}

// requireOwnership allows admins and dashboard owners to mutate the object.
func (d *dashboardAPI) requireOwnership(r *http.Request, dm *models.Dashboard) error {
	u := security.UserFrom(r.Context())
	if u == nil {
		return apperrors.Unauthorized("Unauthorized")
	}
	perms, _ := security.PermissionsFrom(r.Context())
	if perms.Admin() {
		return nil
	}
	var count int64
	_ = d.deps.DB.WithContext(r.Context()).Model(&models.DashboardUser{}).
		Where("dashboard_id = ? AND user_id = ?", dm.ID, u.ID).Count(&count).Error
	if count > 0 {
		return nil
	}
	return apperrors.DashboardSecurityAccess(
		fmt.Errorf("You don't have access to the dashboard %s", dm.DashboardTitle))
}

// slugTaken reports whether another dashboard already uses the slug.
func (d *dashboardAPI) slugTaken(r *http.Request, slug string, excludeID int) bool {
	if slug == "" {
		return false
	}
	var count int64
	q := d.deps.DB.WithContext(r.Context()).Model(&models.Dashboard{}).
		Where("slug = ? AND deleted_at IS NULL", slug)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	_ = q.Count(&count).Error
	return count > 0
}

// firstMapString reads the first non-empty string value of the given keys.
func firstMapString(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}
