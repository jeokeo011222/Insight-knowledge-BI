package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/queries"
	"superset/internal/security"
	"superset/internal/server"
	"superset/internal/thumbnails"
)

// RegisterCharts mounts /api/v1/chart/*.
func RegisterCharts(r chi.Router, deps *Deps) {
	c := &chartAPI{deps: deps}

	r.Route("/chart", func(cr chi.Router) {
		cr.Use(security.RequireUser)

		cr.With(security.RequirePermission(security.ActionRead, security.ResChart)).Get("/", c.list)
		cr.With(security.RequirePermission(security.ActionWrite, security.ResChart)).Post("/", c.create)
		cr.Get("/_info", c.info)

		// Chart data (superset: /api/v1/chart/data).
		cr.With(security.RequirePermission(security.ActionQueryFormData, security.ResChart)).Post("/data", c.data)

		cr.Post("/import/", c.importCharts)
		cr.Get("/recent_activity/", c.recentActivity)
		cr.With(security.RequirePermission(security.ActionDelete, security.ResChart)).Delete("/", c.bulkDelete)

		cr.Get("/{pk}", c.get)
		cr.With(security.RequirePermission(security.ActionWrite, security.ResChart)).Put("/{pk}", c.update)
		cr.With(security.RequirePermission(security.ActionDelete, security.ResChart)).Delete("/{pk}", c.remove)
		cr.With(security.RequirePermission(security.ActionQueryFormData, security.ResChart)).Get("/{pk}/data", c.chartData)
		cr.Get("/{pk}/thumbnail/{digest}/", c.thumbnail)
		cr.Get("/{pk}/thumbnail/{digest}", c.thumbnail)
		cr.With(security.RequirePermission(security.ActionExport, security.ResChart)).Get("/{pk}/export/", c.export)
		cr.Put("/{pk}/favorite_status/", c.favoriteStatus)
		cr.Get("/{pk}/cache_screenshot/", c.cacheScreenshot)
		cr.Get("/{pk}/cache_screenshot", c.cacheScreenshot)
	})
}

type chartAPI struct {
	deps *Deps

	once sync.Once
	exec *queries.Executor
}

// executor returns the lazily constructed query executor.
func (c *chartAPI) executor() *queries.Executor {
	c.once.Do(func() {
		c.exec = queries.NewFromConfig(c.deps.DB, c.deps.Log, c.deps.Cache, c.deps.Mgr, c.deps.Config)
	})
	return c.exec
}

// Payloads -----------------------------------------------------------------

type chartPayload struct {
	SliceName            string `json:"slice_name"`
	VizType              string `json:"viz_type"`
	DatasourceID         int    `json:"datasource_id"`
	DatasourceType       string `json:"datasource_type"`
	DatasourceName       string `json:"datasource_name"`
	Params               any    `json:"params"`
	QueryContext         any    `json:"query_context"`
	Description          string `json:"description"`
	CacheTimeout         *int   `json:"cache_timeout"`
	CertifiedBy          string `json:"certified_by"`
	CertificationDetails string `json:"certification_details"`
	Owners               []int  `json:"owners"`
}

type chartUpdatePayload struct {
	SliceName            *string `json:"slice_name"`
	VizType              *string `json:"viz_type"`
	DatasourceID         *int    `json:"datasource_id"`
	DatasourceType       *string `json:"datasource_type"`
	Params               any     `json:"params"`
	QueryContext         any     `json:"query_context"`
	Description          *string `json:"description"`
	CacheTimeout         *int    `json:"cache_timeout"`
	CertifiedBy          *string `json:"certified_by"`
	CertificationDetails *string `json:"certification_details"`
	Owners               []int   `json:"owners"`
}

// List ---------------------------------------------------------------------

func (c *chartAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := c.deps.DB.WithContext(r.Context()).Model(&models.Slice{}).Where("deleted_at IS NULL")
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("slice_name ILIKE ? OR description ILIKE ?", like, like)
	}
	if viz := server.QueryStr(r, "viz_type"); viz != "" {
		q = q.Where("viz_type = ?", viz)
	}
	if dsID := server.QueryInt(r, "datasource_id", 0); dsID > 0 {
		q = q.Where("datasource_id = ?", dsID)
	}
	if dsType := server.QueryStr(r, "datasource_type"); dsType != "" {
		q = q.Where("datasource_type = ?", dsType)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}

	order := orderClause(p, map[string]string{
		"id": "id", "slice_name": "slice_name", "viz_type": "viz_type",
		"changed_on": "changed_on", "created_on": "created_on",
	}, "slice_name")

	var rows []models.Slice
	if err := q.Order(order).Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}

	visible := c.visibleCharts(r, rows)
	ids := make([]int, 0, len(visible))
	for _, s := range visible {
		ids = append(ids, s.ID)
	}
	owners := c.loadOwners(r, ids)
	tags := c.loadTags(r, ids)

	total := len(visible)
	start, end := sharedPageBounds(p, total)
	out := make([]any, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, c.chartJSON(r, &visible[i], owners, tags))
	}
	server.WritePaginated(w, int64(total), out, nil, nil)
}

// sharedPageBounds computes the slice window for the requested page.
func sharedPageBounds(p listParams, total int) (int, int) {
	if p.All {
		size := p.Size
		if size <= 0 || size > total {
			size = total
		}
		return 0, size
	}
	start := (p.Page - 1) * p.Size
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + p.Size
	if end > total {
		end = total
	}
	return start, end
}

// visibleCharts hides charts whose datasource the caller may not read.
func (c *chartAPI) visibleCharts(r *http.Request, rows []models.Slice) []models.Slice {
	perms, _ := security.PermissionsFrom(r.Context())
	if perms.Admin() || perms.Has(security.PermAllDatasourceAccess) {
		return rows
	}

	dsIDs := make([]int, 0, len(rows))
	for _, s := range rows {
		if strings.EqualFold(s.DatasourceType, "table") && s.DatasourceID > 0 {
			dsIDs = append(dsIDs, s.DatasourceID)
		}
	}
	datasets := c.loadDatasets(r, dsIDs)
	dbIDs := make([]int, 0, len(datasets))
	for _, ds := range datasets {
		dbIDs = append(dbIDs, ds.DatabaseID)
	}
	databases := c.loadDatabases(r, dbIDs)

	out := make([]models.Slice, 0, len(rows))
	for _, s := range rows {
		ds, ok := datasets[s.DatasourceID]
		if !ok {
			// Charts on non-table (or missing) datasources stay readable; they
			// expose no dataset rows beyond what the chart itself stores.
			out = append(out, s)
			continue
		}
		if chartsDatasourceAllowed(perms, databases[ds.DatabaseID], ds) {
			out = append(out, s)
		}
	}
	return out
}

// chartsDatasourceAllowed applies the datasource/database/schema permission
// chain for a chart's dataset.
func chartsDatasourceAllowed(perms security.PermissionSet, db *models.Database, ds *models.SqlaTable) bool {
	dbName := ""
	if db != nil {
		dbName = db.DatabaseName
	}
	if perms.Has(security.DatasourceAccessPerm(dbName, ds.TableNameStr, ds.ID).Action) {
		return true
	}
	if perms.Has(security.DatabaseAccessPerm(dbName, 0).Action) {
		return true
	}
	if perms.Has(security.SchemaAccessPerm(dbName, ds.Schema).Action) {
		return true
	}
	prefix := security.PermDatasourceAccess + " on [" + dbName + "].[" + ds.TableNameStr + "]"
	for p := range perms {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

func (c *chartAPI) loadDatasets(r *http.Request, ids []int) map[int]*models.SqlaTable {
	out := map[int]*models.SqlaTable{}
	if len(ids) == 0 {
		return out
	}
	var rows []models.SqlaTable
	if err := c.deps.DB.WithContext(r.Context()).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return out
	}
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out
}

func (c *chartAPI) loadDatabases(r *http.Request, ids []int) map[int]*models.Database {
	out := map[int]*models.Database{}
	if len(ids) == 0 {
		return out
	}
	var rows []models.Database
	if err := c.deps.DB.WithContext(r.Context()).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return out
	}
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out
}

// loadOwners returns owners keyed by slice id.
func (c *chartAPI) loadOwners(r *http.Request, ids []int) map[int][]map[string]any {
	out := map[int][]map[string]any{}
	if len(ids) == 0 {
		return out
	}
	var rows []struct {
		SliceID   int
		ID        int
		Username  string
		FirstName string
		LastName  string
	}
	err := c.deps.DB.WithContext(r.Context()).Raw(`
		SELECT su.slice_id AS slice_id, u.id AS id, u.username AS username,
		       u.first_name AS first_name, u.last_name AS last_name
		FROM slice_user su
		JOIN ab_user u ON u.id = su.user_id
		WHERE su.slice_id IN ?`, ids).Scan(&rows).Error
	if err != nil {
		return out
	}
	for _, o := range rows {
		out[o.SliceID] = append(out[o.SliceID], map[string]any{
			"id": o.ID, "username": o.Username,
			"first_name": o.FirstName, "last_name": o.LastName,
		})
	}
	return out
}

// loadTags returns tags keyed by slice id (ObjectType.chart == 2).
func (c *chartAPI) loadTags(r *http.Request, ids []int) map[int][]map[string]any {
	out := map[int][]map[string]any{}
	if len(ids) == 0 {
		return out
	}
	var rows []struct {
		ObjectID int
		ID       int
		Name     string
		Type     int
	}
	err := c.deps.DB.WithContext(r.Context()).Raw(`
		SELECT t.object_id AS object_id, tag.id AS id, tag.name AS name, tag.type AS type
		FROM tagged_object t
		JOIN tag ON tag.id = t.tag_id
		WHERE t.object_type = 2 AND t.object_id IN ?`, ids).Scan(&rows).Error
	if err != nil {
		return out
	}
	for _, t := range rows {
		out[t.ObjectID] = append(out[t.ObjectID], map[string]any{
			"id": t.ID, "name": t.Name, "type": t.Type,
		})
	}
	return out
}

// chartJSON renders the superset chart response shape.
func (c *chartAPI) chartJSON(r *http.Request, s *models.Slice, owners map[int][]map[string]any, tags map[int][]map[string]any) map[string]any {
	dsName := s.DatasourceName
	if dsName == "" {
		var ds models.SqlaTable
		if err := c.deps.DB.WithContext(r.Context()).First(&ds, s.DatasourceID).Error; err == nil {
			dsName = ds.TableNameStr
		}
	}
	ownerList := owners[s.ID]
	if ownerList == nil {
		ownerList = []map[string]any{}
	}
	tagList := tags[s.ID]
	if tagList == nil {
		tagList = []map[string]any{}
	}
	return map[string]any{
		"id":                    s.ID,
		"slice_name":            s.SliceName,
		"viz_type":              s.VizType,
		"datasource_id":         s.DatasourceID,
		"datasource_type":       s.DatasourceType,
		"datasource_name":       dsName,
		"datasource_url":        fmt.Sprintf("/superset/explore/?datasource_type=%s&datasource_id=%d", s.DatasourceType, s.DatasourceID),
		"params":                jsonField(s.Params),
		"description":           nullIfEmpty(s.Description),
		"cache_timeout":         s.CacheTimeout,
		"owners":                ownerList,
		"created_on":            s.CreatedOn,
		"changed_on":            s.ChangedOn,
		"uuid":                  s.UUID,
		"certified_by":          nullIfEmpty(s.CertifiedBy),
		"certification_details": nullIfEmpty(s.CertificationDetails),
		"thumbnail_url":         chartsThumbnailURL(s),
		"tags":                  tagList,
		"url":                   fmt.Sprintf("/explore/?slice_id=%d", s.ID),
	}
}

// chartsThumbnailURL builds the stable thumbnail path for a chart.
func chartsThumbnailURL(s *models.Slice) string {
	return fmt.Sprintf("/api/v1/chart/%d/thumbnail/%s/", s.ID, chartDigest(s))
}

// chartDigest fingerprints the chart's rendered content so a changed chart
// gets a fresh thumbnail cache entry.
func chartDigest(s *models.Slice) string {
	h := sha256.New()
	_, _ = h.Write(s.Params)
	_, _ = h.Write([]byte(s.ChangedOn.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// CRUD ---------------------------------------------------------------------

func (c *chartAPI) info(w http.ResponseWriter, r *http.Request) {
	q := c.deps.DB.WithContext(r.Context()).Model(&models.Slice{}).Where("deleted_at IS NULL")
	if ids := idsParam(r); len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	var rows []models.Slice
	if err := q.Order("slice_name").Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, s := range rows {
		out = append(out, map[string]any{
			"id": s.ID, "text": s.SliceName, "slice_name": s.SliceName,
			"viz_type": s.VizType, "datasource_id": s.DatasourceID,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"count": len(out), "result": out})
}

func (c *chartAPI) get(w http.ResponseWriter, r *http.Request) {
	s, err := c.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := c.requireAccess(r, s); err != nil {
		server.WriteError(w, err)
		return
	}
	ids := []int{s.ID}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": c.chartJSON(r, s, c.loadOwners(r, ids), c.loadTags(r, ids)),
	})
}

func (c *chartAPI) create(w http.ResponseWriter, r *http.Request) {
	var p chartPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(p.SliceName) == "" || p.DatasourceID == 0 {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.chart.names_required", "slice_name and datasource_id are required")))
		return
	}
	if p.DatasourceType == "" {
		p.DatasourceType = "table"
	}

	s := models.Slice{
		UUID:                 uuid.NewString(),
		SliceName:            p.SliceName,
		VizType:              p.VizType,
		DatasourceID:         p.DatasourceID,
		DatasourceType:       p.DatasourceType,
		Params:               sharedJSONColumn(p.Params, "{}"),
		QueryContext:         sharedOptionalJSONColumn(p.QueryContext),
		Description:          p.Description,
		CacheTimeout:         p.CacheTimeout,
		CertifiedBy:          p.CertifiedBy,
		CertificationDetails: p.CertificationDetails,
	}
	c.fillDatasourceMeta(r, &s)
	if p.DatasourceName != "" {
		s.DatasourceName = p.DatasourceName
	}

	err := c.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&s).Error; err != nil {
			return err
		}
		owners := p.Owners
		if len(owners) == 0 {
			if uid := userIDOf(r); uid > 0 {
				owners = []int{uid}
			}
		}
		for _, uid := range owners {
			if err := tx.Create(&models.SliceUser{UserID: uid, SliceID: s.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.chart.create_failed", "Could not create chart: %s", err.Error())))
		return
	}
	_ = c.deps.DB.WithContext(r.Context()).First(&s, s.ID).Error
	ids := []int{s.ID}
	server.WriteJSON(w, http.StatusCreated, map[string]any{
		"result": c.chartJSON(r, &s, c.loadOwners(r, ids), c.loadTags(r, ids)),
	})
}

// fillDatasourceMeta copies the dataset name and permission strings onto the
// chart, mirroring superset's pre-save hooks.
func (c *chartAPI) fillDatasourceMeta(r *http.Request, s *models.Slice) {
	if !strings.EqualFold(s.DatasourceType, "table") {
		return
	}
	var ds models.SqlaTable
	if err := c.deps.DB.WithContext(r.Context()).First(&ds, s.DatasourceID).Error; err != nil {
		return
	}
	if s.DatasourceName == "" {
		s.DatasourceName = ds.TableNameStr
	}
	var db models.Database
	dbName := ""
	if err := c.deps.DB.WithContext(r.Context()).First(&db, ds.DatabaseID).Error; err == nil {
		dbName = db.DatabaseName
	}
	s.Perm = fmt.Sprintf("[%s].[%s](id:%d)", dbName, ds.TableNameStr, ds.ID)
	s.SchemaPerm = fmt.Sprintf("[%s].[%s]", dbName, ds.Schema)
}

func (c *chartAPI) update(w http.ResponseWriter, r *http.Request) {
	s, err := c.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := c.requireAccess(r, s); err != nil {
		server.WriteError(w, err)
		return
	}
	var p chartUpdatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}

	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.SliceName != nil {
		updates["slice_name"] = *p.SliceName
	}
	if p.VizType != nil {
		updates["viz_type"] = *p.VizType
	}
	if p.Description != nil {
		updates["description"] = *p.Description
	}
	if p.DatasourceID != nil && *p.DatasourceID > 0 {
		updates["datasource_id"] = *p.DatasourceID
	}
	if p.DatasourceType != nil && *p.DatasourceType != "" {
		updates["datasource_type"] = *p.DatasourceType
	}
	if p.CacheTimeout != nil {
		updates["cache_timeout"] = *p.CacheTimeout
	}
	if p.CertifiedBy != nil {
		updates["certified_by"] = *p.CertifiedBy
	}
	if p.CertificationDetails != nil {
		updates["certification_details"] = *p.CertificationDetails
	}
	if p.Params != nil {
		updates["params"] = string(sharedJSONColumn(p.Params, "{}"))
	}
	if p.QueryContext != nil {
		updates["query_context"] = string(sharedOptionalJSONColumn(p.QueryContext))
	}

	db := c.deps.DB.WithContext(r.Context())
	if err := db.Model(s).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.chart.update_failed", "Could not update chart: %s", err.Error())))
		return
	}
	if p.Owners != nil {
		db.Where("slice_id = ?", s.ID).Delete(&models.SliceUser{})
		for _, uid := range p.Owners {
			db.Create(&models.SliceUser{UserID: uid, SliceID: s.ID})
		}
	}
	_ = db.First(s, s.ID).Error
	ids := []int{s.ID}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": c.chartJSON(r, s, c.loadOwners(r, ids), c.loadTags(r, ids)),
	})
}

func (c *chartAPI) remove(w http.ResponseWriter, r *http.Request) {
	s, err := c.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := c.requireAccess(r, s); err != nil {
		server.WriteError(w, err)
		return
	}
	if err := c.deleteCharts(r, []int{s.ID}); err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// bulkDelete implements DELETE /chart/?q=[1,2,3].
func (c *chartAPI) bulkDelete(w http.ResponseWriter, r *http.Request) {
	ids := chartsIDsFromQuery(r)
	if len(ids) == 0 {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.chart.ids_required", "No chart ids provided")))
		return
	}
	for _, id := range ids {
		var s models.Slice
		if err := c.deps.DB.WithContext(r.Context()).First(&s, id).Error; err == nil {
			if err := c.requireAccess(r, &s); err != nil {
				server.WriteError(w, err)
				return
			}
		}
	}
	if err := c.deleteCharts(r, ids); err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Deleted %d charts", len(ids)),
	})
}

// deleteCharts soft deletes charts and drops their associations.
func (c *chartAPI) deleteCharts(r *http.Request, ids []int) error {
	db := c.deps.DB.WithContext(r.Context())
	return db.Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Model(&models.Slice{}).Where("id IN ?", ids).
			Updates(map[string]any{"deleted_at": &now}).Error; err != nil {
			return err
		}
		if err := tx.Where("slice_id IN ?", ids).Delete(&models.DashboardSlices{}).Error; err != nil {
			return err
		}
		return tx.Where("slice_id IN ?", ids).Delete(&models.SliceUser{}).Error
	})
}

// chartsIDsFromQuery parses ?q=[1,2,3] (FAB bulk delete) and ?ids=1,2.
func chartsIDsFromQuery(r *http.Request) []int {
	raw := strings.TrimSpace(server.QueryStr(r, "q"))
	if raw != "" {
		var ids []int
		if err := json.Unmarshal([]byte(raw), &ids); err == nil {
			return ids
		}
		var generic []any
		if err := json.Unmarshal([]byte(raw), &generic); err == nil {
			out := make([]int, 0, len(generic))
			for _, v := range generic {
				switch t := v.(type) {
				case float64:
					out = append(out, int(t))
				case string:
					var n int
					if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
						out = append(out, n)
					}
				}
			}
			return out
		}
	}
	return idsParam(r)
}

// Data ---------------------------------------------------------------------

// chartData serves GET /chart/{pk}/data using the chart's stored query context
// (or its params when only form_data was saved).
func (c *chartAPI) chartData(w http.ResponseWriter, r *http.Request) {
	s, err := c.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := c.requireAccess(r, s); err != nil {
		server.WriteError(w, err)
		return
	}
	qc, err := c.queryContextFor(r, s)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if server.QueryBool(r, "force", false) {
		qc.Force = true
	}
	c.runQueryContext(w, r, qc)
}

// data serves POST /chart/data (superset's /api/v1/chart/data).
func (c *chartAPI) data(w http.ResponseWriter, r *http.Request) {
	body, err := chartsReadBody(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	qc, err := queries.ParseQueryContext(body)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	c.runQueryContext(w, r, qc)
}

// chartsReadBody reads a JSON body, also accepting the form encoded variant
// used by CSV exports (form_data field).
func chartsReadBody(r *http.Request) ([]byte, error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			return nil, apperrors.BadRequest("%s", localize(r, "error.common.parse_form_failed", "Could not parse form body"))
		}
		if fd := r.PostFormValue("form_data"); fd != "" {
			return []byte(fd), nil
		}
		return nil, apperrors.BadRequest("%s", localize(r, "error.chart.form_data_required", "form_data is required"))
	}
	if r.Body == nil {
		return nil, apperrors.BadRequest("%s", localize(r, "error.common.empty_body", "Empty request body"))
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return nil, apperrors.BadRequest("%s", localize(r, "error.common.read_body_failed", "Could not read request body"))
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, apperrors.BadRequest("%s", localize(r, "error.common.empty_body", "Empty request body"))
	}
	return body, nil
}

func (c *chartAPI) runQueryContext(w http.ResponseWriter, r *http.Request, qc *queries.QueryContext) {
	result, err := c.executor().ExecuteQueryContext(r.Context(), userIDOf(r), qc)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	// Column-level masking runs on the way out so the cached rows stay raw.
	c.applyMasking(r, qc, result)
	out := result.Response(qc)
	// Annotations ride along with the same request: the events are small, they
	// are not part of the cached payload, and a failed lookup must not take the
	// chart down with it. They belong to every result item because a renderer
	// receives one result and knows nothing about the others.
	if data := c.annotationData(r.Context(), qc); len(data) > 0 {
		if items, ok := out["result"].([]map[string]any); ok {
			for _, item := range items {
				item["annotation_data"] = data
			}
		}
	}
	server.WriteJSON(w, http.StatusOK, out)
}

// queryContextFor prefers the stored query_context; otherwise it synthesises a
// context from form_data/params.
func (c *chartAPI) queryContextFor(r *http.Request, s *models.Slice) (*queries.QueryContext, error) {
	if len(s.QueryContext) > 0 {
		var probe map[string]any
		if err := json.Unmarshal(s.QueryContext, &probe); err == nil {
			if _, ok := probe["queries"]; ok {
				qc, err := queries.ParseQueryContext(json.RawMessage(s.QueryContext))
				if err == nil {
					c.ensureDatasource(qc, s)
					return qc, nil
				}
			}
		}
	}
	body := map[string]any{
		"datasource": map[string]any{
			"id":   s.DatasourceID,
			"type": orDefault(s.DatasourceType, "table"),
		},
		"form_data": jsonMap(s.Params),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, apperrors.BadRequest("%s", localize(r, "error.chart.invalid_params", "Invalid chart params: %s", err.Error()))
	}
	qc, err := queries.ParseQueryContext(raw)
	if err != nil {
		return nil, err
	}
	c.ensureDatasource(qc, s)
	return qc, nil
}

// ensureDatasource makes sure the query context points at the chart's dataset.
func (c *chartAPI) ensureDatasource(qc *queries.QueryContext, s *models.Slice) {
	if qc == nil {
		return
	}
	if id, _ := qc.DatasourceID(); id == 0 {
		qc.Datasource = map[string]any{
			"id":   s.DatasourceID,
			"type": orDefault(s.DatasourceType, "table"),
		}
	}
}

// Misc endpoints -----------------------------------------------------------

func (c *chartAPI) thumbnail(w http.ResponseWriter, r *http.Request) {
	s, err := c.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	key := thumbnailCacheKey("chart", s.ID, chi.URLParam(r, "digest"), chartDigest(s))
	digest, data, err := captureThumbnail(r.Context(), c.deps.Thumbs, key, requestAccessToken(r), false, func(token string) thumbnails.Target {
		return thumbnails.Target{
			URL:      fmt.Sprintf("%s/explore/?slice_id=%d", c.deps.Thumbs.BaseURL(), s.ID),
			Selector: ".chart-canvas, .chart-preview, .chart-card, main",
			Token:    token,
		}
	})
	if err != nil {
		writeThumbnailError(w, err)
		return
	}
	writeThumbnail(w, r, digest, data)
}

func (c *chartAPI) cacheScreenshot(w http.ResponseWriter, r *http.Request) {
	s, err := c.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	contentDigest := chartDigest(s)
	key := fmt.Sprintf("chart:%d:%s", s.ID, contentDigest)
	_, data, err := captureThumbnail(r.Context(), c.deps.Thumbs, key, requestAccessToken(r), true, func(token string) thumbnails.Target {
		return thumbnails.Target{
			URL:      fmt.Sprintf("%s/explore/?slice_id=%d", c.deps.Thumbs.BaseURL(), s.ID),
			Selector: ".chart-canvas, .chart-preview, .chart-card, main",
			Token:    token,
		}
	})
	if err != nil {
		writeThumbnailError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"digest":    contentDigest,
		"image_url": fmt.Sprintf("/api/v1/chart/%d/thumbnail/%s/", s.ID, contentDigest),
		"bytes":     len(data),
	})
}

// favoriteStatus toggles the caller's favorite flag for a chart.
func (c *chartAPI) favoriteStatus(w http.ResponseWriter, r *http.Request) {
	s, err := c.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := c.requireAccess(r, s); err != nil {
		server.WriteError(w, err)
		return
	}
	uid := userIDOf(r)
	if uid == 0 {
		server.WriteError(w, apperrors.Unauthorized("%s", localize(r, "error.security.unauthorized", "Unauthorized")))
		return
	}
	db := c.deps.DB.WithContext(r.Context())
	var fav models.FavStar
	err = db.Where("user_id = ? AND class_name = ? AND obj_id = ?", uid, "slice", s.ID).First(&fav).Error
	active := true
	switch {
	case err == nil:
		if derr := db.Delete(&fav).Error; derr != nil {
			server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", derr.Error())))
			return
		}
		active = false
	case errors.Is(err, gorm.ErrRecordNotFound):
		now := time.Now().UTC()
		fav = models.FavStar{
			UUID: uuid.NewString(), UserID: uid, ClassName: "slice", ObjID: s.ID, Dttm: &now,
		}
		if cerr := db.Create(&fav).Error; cerr != nil {
			server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", cerr.Error())))
			return
		}
	default:
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": map[string]any{"favorite_status": active},
	})
}

// recentActivity lists the charts the caller opened most recently.
func (c *chartAPI) recentActivity(w http.ResponseWriter, r *http.Request) {
	uid := userIDOf(r)
	if uid == 0 {
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": []any{}, "count": 0})
		return
	}
	var rows []struct {
		SliceID int
		Dttm    *time.Time
	}
	err := c.deps.DB.WithContext(r.Context()).Raw(`
		SELECT slice_id, MAX(dttm) AS dttm
		FROM logs
		WHERE user_id = ? AND slice_id IS NOT NULL
		GROUP BY slice_id
		ORDER BY MAX(dttm) DESC
		LIMIT 20`, uid).Scan(&rows).Error
	if err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}
	if len(rows) == 0 {
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": []any{}, "count": 0})
		return
	}
	ids := make([]int, 0, len(rows))
	lastSeen := map[int]*time.Time{}
	for _, row := range rows {
		ids = append(ids, row.SliceID)
		lastSeen[row.SliceID] = row.Dttm
	}
	var slices []models.Slice
	if err := c.deps.DB.WithContext(r.Context()).Where("id IN ? AND deleted_at IS NULL", ids).
		Find(&slices).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}
	byID := map[int]models.Slice{}
	for _, s := range slices {
		byID[s.ID] = s
	}
	owners := c.loadOwners(r, ids)
	tags := c.loadTags(r, ids)
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		s, ok := byID[row.SliceID]
		if !ok {
			continue
		}
		item := c.chartJSON(r, &s, owners, tags)
		item["last_viewed"] = lastSeen[row.SliceID]
		out = append(out, item)
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out, "count": len(out)})
}

// Export / import ----------------------------------------------------------

func (c *chartAPI) export(w http.ResponseWriter, r *http.Request) {
	s, err := c.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := c.requireAccess(r, s); err != nil {
		server.WriteError(w, err)
		return
	}
	ids := []int{s.ID}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=chart_%d_export.json", s.ID))
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"__version": "1.0.0",
		"type":      "chart",
		"chart":     c.chartJSON(r, s, c.loadOwners(r, ids), c.loadTags(r, ids)),
	})
}

func (c *chartAPI) importCharts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Charts []chartPayload `json:"charts"`
		Chart  *chartPayload  `json:"chart"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	payloads := body.Charts
	if body.Chart != nil {
		payloads = append(payloads, *body.Chart)
	}
	if len(payloads) == 0 {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.chart.import_empty", "No charts to import")))
		return
	}
	created := make([]int, 0, len(payloads))
	for _, p := range payloads {
		if strings.TrimSpace(p.SliceName) == "" || p.DatasourceID == 0 {
			continue
		}
		if p.DatasourceType == "" {
			p.DatasourceType = "table"
		}
		s := models.Slice{
			UUID:                 uuid.NewString(),
			SliceName:            p.SliceName,
			VizType:              p.VizType,
			DatasourceID:         p.DatasourceID,
			DatasourceType:       p.DatasourceType,
			DatasourceName:       p.DatasourceName,
			Params:               sharedJSONColumn(p.Params, "{}"),
			QueryContext:         sharedOptionalJSONColumn(p.QueryContext),
			Description:          p.Description,
			CacheTimeout:         p.CacheTimeout,
			CertifiedBy:          p.CertifiedBy,
			CertificationDetails: p.CertificationDetails,
		}
		if err := c.deps.DB.WithContext(r.Context()).Create(&s).Error; err == nil {
			created = append(created, s.ID)
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Imported %d charts", len(created)),
		"result":  created,
	})
}

// Helpers ------------------------------------------------------------------

// find resolves the {pk} chart.
func (c *chartAPI) find(r *http.Request) (*models.Slice, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var s models.Slice
	if err := c.deps.DB.WithContext(r.Context()).First(&s, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("%s", localize(r, "error.chart.not_found", "Chart not found"))
		}
		return nil, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error()))
	}
	if s.DeletedAt != nil {
		return nil, apperrors.NotFound("%s", localize(r, "error.chart.not_found", "Chart not found"))
	}
	return &s, nil
}

// requireAccess enforces ownership or datasource access for charts.
func (c *chartAPI) requireAccess(r *http.Request, s *models.Slice) error {
	u := security.UserFrom(r.Context())
	if u == nil {
		return apperrors.Unauthorized("%s", localize(r, "error.security.unauthorized", "Unauthorized"))
	}
	perms, _ := security.PermissionsFrom(r.Context())
	if perms.Admin() {
		return nil
	}
	// A guest token is minted by the server after it has already decided what
	// the holder may see (embedded dashboard or public share), and the guest
	// user has no database roles to resolve datasource grants against.
	if u.IsGuest {
		return nil
	}
	if c.isOwner(r, s.ID, u.ID) {
		return nil
	}
	if !strings.EqualFold(s.DatasourceType, "table") || s.DatasourceID == 0 {
		return apperrors.ChartSecurityAccess(fmt.Errorf("You don't have access to the chart %s", s.SliceName))
	}
	var ds models.SqlaTable
	if err := c.deps.DB.WithContext(r.Context()).First(&ds, s.DatasourceID).Error; err != nil {
		return apperrors.ChartSecurityAccess(fmt.Errorf("You don't have access to the chart %s", s.SliceName))
	}
	var db models.Database
	dbName := ""
	if err := c.deps.DB.WithContext(r.Context()).First(&db, ds.DatabaseID).Error; err == nil {
		dbName = db.DatabaseName
	}
	if c.deps.Mgr != nil && c.deps.Mgr.CanAccessDatasource(r.Context(), u.ID, dbName, ds.Schema, ds.TableNameStr, ds.ID) {
		return nil
	}
	return apperrors.ChartSecurityAccess(fmt.Errorf("You don't have access to the chart %s", s.SliceName))
}

func (c *chartAPI) isOwner(r *http.Request, sliceID, userID int) bool {
	if userID == 0 {
		return false
	}
	var count int64
	_ = c.deps.DB.WithContext(r.Context()).Model(&models.SliceUser{}).
		Where("slice_id = ? AND user_id = ?", sliceID, userID).Count(&count).Error
	return count > 0
}

// sharedJSONColumn marshals params/query_context to a JSON column value.
func sharedJSONColumn(v any, fallback string) []byte {
	if v == nil {
		return []byte(fallback)
	}
	if s, ok := v.(string); ok {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return []byte(fallback)
		}
		if json.Valid([]byte(trimmed)) {
			return []byte(trimmed)
		}
		return []byte(fallback)
	}
	if b, err := json.Marshal(v); err == nil {
		return b
	}
	return []byte(fallback)
}

// sharedOptionalJSONColumn marshals an optional JSON column, returning nil when the
// value is absent so that GORM skips the column.
func sharedOptionalJSONColumn(v any) []byte {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" || !json.Valid([]byte(trimmed)) {
			return nil
		}
		return []byte(trimmed)
	}
	if b, err := json.Marshal(v); err == nil {
		return b
	}
	return nil
}

// Thumbnail helpers --------------------------------------------------------

// thumbDigestPattern accepts the digest path parameter shapes used by the UI.
var thumbDigestPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{4,64}$`)

// thumbnailCacheKey builds the logical cache key for an entity version.
func thumbnailCacheKey(entity string, id int, digest, fallback string) string {
	digest = strings.TrimSpace(digest)
	if !thumbDigestPattern.MatchString(digest) {
		digest = fallback
	}
	return fmt.Sprintf("%s:%d:%s", entity, id, digest)
}

// requestAccessToken returns the caller's bearer token, falling back to the
// session cookie used by the SPA.
func requestAccessToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}
	if c, err := r.Cookie("access_token_cookie"); err == nil {
		return c.Value
	}
	return ""
}

// captureThumbnail serves the cached PNG for key or renders a new one.
func captureThumbnail(
	ctx context.Context,
	service *thumbnails.Service,
	key, token string,
	force bool,
	target func(token string) thumbnails.Target,
) (string, []byte, error) {
	if service == nil {
		return "", nil, errors.New("thumbnail service is not available")
	}
	if strings.TrimSpace(token) == "" {
		return "", nil, errors.New("no access token available; sign in before requesting a thumbnail")
	}
	build := func() thumbnails.Target { return target(token) }
	if force {
		return service.Force(ctx, key, build)
	}
	return service.Ensure(ctx, key, build)
}

// writeThumbnail streams PNG bytes with an ETag.
func writeThumbnail(w http.ResponseWriter, r *http.Request, digest string, data []byte) {
	etag := `"` + digest + `"`
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", etag)
	_, _ = w.Write(data)
}

// writeThumbnailError reports a capture failure instead of serving a fake image.
func writeThumbnailError(w http.ResponseWriter, err error) {
	server.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
		"message": "thumbnail generation failed: " + err.Error(),
	})
}
