package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterRLS mounts /api/v1/rowlevelsecurity/*
// (superset/row_level_security/api.py).
func RegisterRLS(r chi.Router, deps *Deps) {
	a := &rlsAPI{deps: deps}

	r.Route("/rowlevelsecurity", func(rr chi.Router) {
		rr.Use(security.RequireUser,
			security.RequirePermission(security.ActionRead, security.ResRowLevelSecurity))

		rr.Get("/", a.list)
		rr.With(security.RequirePermission(security.ActionWrite, security.ResRowLevelSecurity)).Post("/", a.create)
		rr.Get("/{pk}", a.get)
		rr.With(security.RequirePermission(security.ActionWrite, security.ResRowLevelSecurity)).Put("/{pk}", a.update)
		rr.With(security.RequirePermission(security.ActionDelete, security.ResRowLevelSecurity)).Delete("/{pk}", a.remove)
		rr.Get("/{pk}/related_objects", a.related)
	})
}

type rlsAPI struct{ deps *Deps }

type rlsPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	FilterType  string `json:"filter_type"`
	GroupKey    string `json:"group_key"`
	Clause      string `json:"clause"`
	Groupby     string `json:"groupby"`
	Roles       []int  `json:"roles"`
	Tables      []int  `json:"tables"`
}

// rlsJSON renders an RLS filter with its role and dataset associations.
func (a *rlsAPI) rlsJSON(r *http.Request, f *models.RowLevelSecurityFilter) map[string]any {
	db := a.deps.DB.WithContext(r.Context())

	var roles []struct {
		ID   int
		Name string
	}
	_ = db.Raw(`
		SELECT ro.id, ro.name FROM ab_role ro
		JOIN rls_filter_roles rr ON rr.role_id = ro.id
		WHERE rr.rls_filter_id = ? ORDER BY ro.name`, f.ID).Scan(&roles).Error
	roleList := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		roleList = append(roleList, map[string]any{"id": role.ID, "name": role.Name})
	}

	var tables []models.SqlaTable
	_ = db.Raw(`
		SELECT t.* FROM tables t
		JOIN rls_filter_tables rt ON rt.table_id = t.id
		WHERE rt.rls_filter_id = ? ORDER BY t.table_name`, f.ID).Scan(&tables).Error
	tableList := make([]map[string]any, 0, len(tables))
	tableIDs := make([]int, 0, len(tables))
	for i := range tables {
		t := &tables[i]
		tableIDs = append(tableIDs, t.ID)
		tableList = append(tableList, map[string]any{
			"id": t.ID, "table_name": t.TableNameStr, "schema": t.Schema,
			"database_id": t.DatabaseID, "uuid": t.UUID,
		})
	}

	return map[string]any{
		"id": f.ID, "name": f.Name, "description": f.Description,
		"filter_type": rlsOrDefault(f.FilterType, "Regular"), "group_key": f.GroupKey,
		"clause": f.Clause, "roles": roleList, "tables": tableList,
		"table_ids":  tableIDs,
		"created_on": f.CreatedOn, "changed_on": f.ChangedOn,
	}
}

func rlsOrDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func (a *rlsAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.RowLevelSecurityFilter{})
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("name ILIKE ? OR clause ILIKE ?", like, like)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "name asc", func(f *models.RowLevelSecurityFilter) any {
		return a.rlsJSON(r, f)
	})
}

func (a *rlsAPI) find(r *http.Request) (*models.RowLevelSecurityFilter, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var f models.RowLevelSecurityFilter
	if err := a.deps.DB.WithContext(r.Context()).First(&f, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("Row level security filter not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &f, nil
}

func (a *rlsAPI) get(w http.ResponseWriter, r *http.Request) {
	f, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.rlsJSON(r, f)})
}

func rlsValidateFilterType(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "Regular", nil
	case "regular":
		return "Regular", nil
	case "base":
		return "Base", nil
	}
	return "", apperrors.BadRequest("filter_type must be Regular or Base")
}

func (a *rlsAPI) create(w http.ResponseWriter, r *http.Request) {
	var p rlsPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Clause) == "" {
		server.WriteError(w, apperrors.BadRequest("name and clause are required"))
		return
	}
	filterType, err := rlsValidateFilterType(p.FilterType)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	f := models.RowLevelSecurityFilter{
		Name: p.Name, Description: p.Description, FilterType: filterType,
		GroupKey: p.GroupKey, Clause: p.Clause,
	}
	if err := a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&f).Error; err != nil {
			return err
		}
		return rlsSyncAssociations(tx, f.ID, p.Roles, p.Tables)
	}); err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create RLS filter: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": a.rlsJSON(r, &f)})
}

func (a *rlsAPI) update(w http.ResponseWriter, r *http.Request) {
	f, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p rlsPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	filterType := f.FilterType
	if strings.TrimSpace(p.FilterType) != "" {
		filterType, err = rlsValidateFilterType(p.FilterType)
		if err != nil {
			server.WriteError(w, err)
			return
		}
	}
	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r)), "filter_type": filterType}
	if p.Name != "" {
		updates["name"] = p.Name
	}
	if p.Description != "" {
		updates["description"] = p.Description
	}
	if p.GroupKey != "" {
		updates["group_key"] = p.GroupKey
	}
	if p.Clause != "" {
		updates["clause"] = p.Clause
	}
	err = a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(f).Updates(updates).Error; err != nil {
			return err
		}
		if p.Roles == nil && p.Tables == nil {
			return nil
		}
		if err := tx.Where("rls_filter_id = ?", f.ID).Delete(&models.RLSFilterRoles{}).Error; err != nil {
			return err
		}
		if err := tx.Where("rls_filter_id = ?", f.ID).Delete(&models.RLSFilterTables{}).Error; err != nil {
			return err
		}
		return rlsSyncAssociations(tx, f.ID, p.Roles, p.Tables)
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update RLS filter: %s", err.Error()))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(f, f.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.rlsJSON(r, f)})
}

// rlsSyncAssociations replaces the role/dataset joins for a filter.
func rlsSyncAssociations(tx *gorm.DB, filterID int, roles, tables []int) error {
	for _, roleID := range roles {
		if err := tx.Create(&models.RLSFilterRoles{RoleID: roleID, RLSFilterID: filterID}).Error; err != nil {
			return err
		}
	}
	for _, tableID := range tables {
		if err := tx.Create(&models.RLSFilterTables{TableID: tableID, RLSFilterID: filterID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (a *rlsAPI) remove(w http.ResponseWriter, r *http.Request) {
	f, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("rls_filter_id = ?", f.ID).Delete(&models.RLSFilterRoles{}).Error; err != nil {
			return err
		}
		if err := tx.Where("rls_filter_id = ?", f.ID).Delete(&models.RLSFilterTables{}).Error; err != nil {
			return err
		}
		return tx.Delete(f).Error
	}); err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// related reports the charts and dashboards affected by the filter's datasets.
func (a *rlsAPI) related(w http.ResponseWriter, r *http.Request) {
	f, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	var tables []models.SqlaTable
	_ = db.Raw(`
		SELECT t.* FROM tables t
		JOIN rls_filter_tables rt ON rt.table_id = t.id
		WHERE rt.rls_filter_id = ?`, f.ID).Scan(&tables).Error

	datasetList := make([]map[string]any, 0, len(tables))
	tableIDs := make([]int, 0, len(tables))
	for i := range tables {
		t := &tables[i]
		tableIDs = append(tableIDs, t.ID)
		datasetList = append(datasetList, map[string]any{
			"id": t.ID, "table_name": t.TableNameStr, "schema": t.Schema,
		})
	}

	var charts []models.Slice
	if len(tableIDs) > 0 {
		_ = db.Where("datasource_type = ? AND datasource_id IN ?", "table", tableIDs).
			Order("slice_name").Find(&charts).Error
	}
	chartList := make([]map[string]any, 0, len(charts))
	chartIDs := make([]int, 0, len(charts))
	for i := range charts {
		c := &charts[i]
		chartIDs = append(chartIDs, c.ID)
		chartList = append(chartList, map[string]any{
			"id": c.ID, "slice_name": c.SliceName, "viz_type": c.VizType,
		})
	}

	var dashboards []models.Dashboard
	if len(chartIDs) > 0 {
		_ = db.Raw(`
			SELECT d.* FROM dashboards d
			JOIN dashboard_slices ds ON ds.dashboard_id = d.id
			WHERE ds.slice_id IN ? GROUP BY d.id ORDER BY d.dashboard_title`, chartIDs).Scan(&dashboards).Error
	}
	dashboardList := make([]map[string]any, 0, len(dashboards))
	for i := range dashboards {
		d := &dashboards[i]
		dashboardList = append(dashboardList, map[string]any{
			"id": d.ID, "dashboard_title": d.DashboardTitle, "slug": d.Slug,
		})
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"charts": chartList, "dashboards": dashboardList, "datasets": datasetList,
	})
}
