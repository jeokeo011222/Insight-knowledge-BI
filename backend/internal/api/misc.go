package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterMisc mounts the remaining model endpoints: audit log, key/value,
// favorites, advanced data types, info, menu and time range.
func RegisterMisc(r chi.Router, deps *Deps) {
	a := &miscAPI{deps: deps}

	r.Route("/log", func(lr chi.Router) {
		lr.Use(security.RequireUser)
		lr.With(security.RequirePermission(security.ActionRead, security.ResLog)).Get("/", a.listLogs)
		lr.With(security.RequirePermission(security.ActionWrite, security.ResLog)).Post("/", a.createLog)
		// Declared before /{pk}/ so "export" is not read as a log id.
		lr.With(security.RequirePermission(security.ActionExport, security.ResLog)).Get("/export/", a.exportLogs)
		lr.With(security.RequirePermission(security.ActionExport, security.ResLog)).Get("/export", a.exportLogs)
		lr.With(security.RequirePermission(security.ActionRead, security.ResLog)).Get("/{pk}/", a.getLog)
	})

	r.Route("/keyvalue", func(kr chi.Router) {
		kr.Use(security.RequireUser)
		kr.With(security.RequirePermission(security.ActionRead, security.ResKeyValue)).Get("/{pk}", a.getKeyValue)
		kr.With(security.RequirePermission(security.ActionWrite, security.ResKeyValue)).Post("/", a.createKeyValue)
		kr.With(security.RequirePermission(security.ActionWrite, security.ResKeyValue)).Put("/{pk}", a.updateKeyValue)
		kr.With(security.RequirePermission(security.ActionDelete, security.ResKeyValue)).Delete("/{pk}", a.deleteKeyValue)
	})

	r.Route("/favstar", func(fr chi.Router) {
		fr.Use(security.RequireUser)
		fr.With(security.RequirePermission(security.ActionRead, security.ResFavStar)).Get("/", a.listFavStars)
		fr.With(security.RequirePermission(security.ActionWrite, security.ResFavStar)).Post("/", a.createFavStar)
		fr.With(security.RequirePermission(security.ActionDelete, security.ResFavStar)).Delete("/{pk}", a.deleteFavStar)
	})

	r.Route("/advanced_data_type", func(ar chi.Router) {
		ar.Use(security.RequireUser)
		ar.With(security.RequirePermission(security.ActionRead, security.ResAdvancedDataType)).
			Get("/", a.listAdvancedDataTypes)
	})

	r.Get("/info/", a.info)
	r.Get("/menu/", a.menu)
	r.Get("/time_range/", a.timeRange)
}

type miscAPI struct{ deps *Deps }

// Audit log ---------------------------------------------------------------

type miscLogPayload struct {
	Action      string `json:"action"`
	DashboardID *int   `json:"dashboard_id"`
	SliceID     *int   `json:"slice_id"`
	JSON        any    `json:"json"`
	Dttm        string `json:"dttm"`
	UserID      *int   `json:"user_id"`
	DurationMs  *int   `json:"duration_ms"`
	Referrer    string `json:"referrer"`
}

func miscLogJSON(l *models.Log) map[string]any {
	return map[string]any{
		"id": l.ID, "action": l.Action, "user_id": l.UserID,
		"dashboard_id": l.DashboardID, "slice_id": l.SliceID,
		"json": jsonField([]byte(l.JSON)), "dttm": l.Dttm,
		"duration_ms": l.DurationMs, "referrer": nullIfEmpty(l.Referrer),
	}
}

func (a *miscAPI) listLogs(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.Log{})
	if p.Search != "" {
		q = q.Where("action ILIKE ?", "%"+p.Search+"%")
	}
	if uid := server.QueryInt(r, "user_id", 0); uid > 0 {
		q = q.Where("user_id = ?", uid)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "id desc", func(l *models.Log) any { return miscLogJSON(l) })
}

func (a *miscAPI) getLog(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var l models.Log
	if err := a.deps.DB.WithContext(r.Context()).First(&l, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Log not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": miscLogJSON(&l)})
}

func (a *miscAPI) createLog(w http.ResponseWriter, r *http.Request) {
	var p miscLogPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(p.Action) == "" {
		server.WriteError(w, apperrors.BadRequest("action is required"))
		return
	}
	l := models.Log{
		Action: p.Action, DashboardID: p.DashboardID, SliceID: p.SliceID,
		JSON: jsonString(p.JSON), UserID: p.UserID, DurationMs: p.DurationMs,
		Referrer: p.Referrer,
	}
	if l.UserID == nil {
		l.UserID = intPtrNil(userIDOf(r))
	}
	l.Dttm = annotationParseTime(p.Dttm)
	if l.Dttm == nil {
		l.Dttm = nowPtr()
	}
	if err := a.deps.DB.WithContext(r.Context()).Create(&l).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not write log: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": miscLogJSON(&l)})
}

// Key/value ---------------------------------------------------------------

type miscKeyValuePayload struct {
	ID    int    `json:"id"`
	Value string `json:"value"`
}

func miscKeyValueJSON(k *models.KeyValue) map[string]any {
	return map[string]any{"id": k.ID, "value": k.Value}
}

func (a *miscAPI) getKeyValue(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var k models.KeyValue
	if err := a.deps.DB.WithContext(r.Context()).First(&k, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Key/value pair not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": miscKeyValueJSON(&k)})
}

func (a *miscAPI) createKeyValue(w http.ResponseWriter, r *http.Request) {
	var p miscKeyValuePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.ID == 0 {
		server.WriteError(w, apperrors.BadRequest("id is required"))
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	var existing models.KeyValue
	err := db.First(&existing, p.ID).Error
	if err == nil {
		if err := db.Model(&existing).Update("value", p.Value).Error; err != nil {
			server.WriteError(w, apperrors.Conflict("Could not update key/value pair: %s", err.Error()))
			return
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": miscKeyValueJSON(&existing)})
		return
	}
	if err != gorm.ErrRecordNotFound {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	k := models.KeyValue{ID: p.ID, Value: p.Value}
	if err := db.Create(&k).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create key/value pair: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": miscKeyValueJSON(&k)})
}

func (a *miscAPI) updateKeyValue(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p miscKeyValuePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	var k models.KeyValue
	db := a.deps.DB.WithContext(r.Context())
	if err := db.First(&k, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Key/value pair not found"))
		return
	}
	if err := db.Model(&k).Update("value", p.Value).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update key/value pair: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": miscKeyValueJSON(&k)})
}

func (a *miscAPI) deleteKeyValue(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	res := a.deps.DB.WithContext(r.Context()).Delete(&models.KeyValue{}, id)
	if res.Error != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", res.Error.Error()))
		return
	}
	if res.RowsAffected == 0 {
		server.WriteError(w, apperrors.NotFound("Key/value pair not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Favorites ---------------------------------------------------------------

type miscFavStarPayload struct {
	ClassName string `json:"class_name"`
	ObjID     int    `json:"obj_id"`
}

func miscFavStarJSON(f *models.FavStar) map[string]any {
	return map[string]any{
		"id": f.ID, "uuid": f.UUID, "user_id": f.UserID,
		"class_name": f.ClassName, "obj_id": f.ObjID, "dttm": f.Dttm,
	}
}

func (a *miscAPI) listFavStars(w http.ResponseWriter, r *http.Request) {
	var rows []models.FavStar
	q := a.deps.DB.WithContext(r.Context()).Where("user_id = ?", userIDOf(r))
	if className := server.QueryStr(r, "class_name"); className != "" {
		q = q.Where("class_name = ?", className)
	}
	if err := q.Order("id desc").Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, miscFavStarJSON(&rows[i]))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out, "count": len(out)})
}

func (a *miscAPI) createFavStar(w http.ResponseWriter, r *http.Request) {
	var p miscFavStarPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(p.ClassName) == "" || p.ObjID == 0 {
		server.WriteError(w, apperrors.BadRequest("class_name and obj_id are required"))
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	var existing models.FavStar
	err := db.Where("user_id = ? AND class_name = ? AND obj_id = ?",
		userIDOf(r), p.ClassName, p.ObjID).First(&existing).Error
	if err == nil {
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": miscFavStarJSON(&existing)})
		return
	}
	f := models.FavStar{
		UUID: uuid.NewString(), UserID: userIDOf(r), ClassName: p.ClassName,
		ObjID: p.ObjID, Dttm: nowPtr(),
	}
	if err := db.Create(&f).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create favorite: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": miscFavStarJSON(&f)})
}

func (a *miscAPI) deleteFavStar(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	res := db.Where("id = ? AND user_id = ?", id, userIDOf(r)).Delete(&models.FavStar{})
	if res.Error != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", res.Error.Error()))
		return
	}
	if res.RowsAffected == 0 {
		server.WriteError(w, apperrors.NotFound("Favorite not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Advanced data types -----------------------------------------------------

func (a *miscAPI) listAdvancedDataTypes(w http.ResponseWriter, r *http.Request) {
	// The table is optional in the metadata schema; an absent table means the
	// feature is not installed and the list is empty.
	var rows []map[string]any
	if err := a.deps.DB.WithContext(r.Context()).Raw("SELECT * FROM advanced_data_type").Scan(&rows).Error; err != nil {
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": []any{}})
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": rows})
}

// Info / menu / time range ------------------------------------------------

func (a *miscAPI) info(w http.ResponseWriter, r *http.Request) {
	cfg := a.deps.Config
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"version":          server.Version,
		"database_version": "postgresql",
		"features": map[string]any{
			"SQL_LAB":              true,
			"TAGGING":              true,
			"ALERT_REPORTS":        true,
			"GUEST_TOKEN":          cfg.GuestTokenJWTSecret != "",
			"GLOBAL_ASYNC_QUERIES": cfg.GLOBAL_ASYNC_QUERIES,
			"SQLLAB_ASYNC":         cfg.SqllabAsyncTimeLimitSec > 0,
		},
	}})
}

type miscMenuItem struct {
	Name     string         `json:"name"`
	Label    string         `json:"label"`
	Icon     string         `json:"icon"`
	URL      string         `json:"url"`
	Children []miscMenuItem `json:"children,omitempty"`
}

// miscMenuLeaf builds a childless menu entry.
func miscMenuLeaf(name, icon, url string) miscMenuItem {
	return miscMenuItem{Name: name, Label: name, Icon: icon, URL: url}
}

func (a *miscAPI) menu(w http.ResponseWriter, r *http.Request) {
	items := make([]miscMenuItem, 0, 8)
	read := func(res string) bool { return security.HasPerm(r, security.ActionRead, res) }

	if read(security.ResDashboard) {
		items = append(items, miscMenuLeaf("Dashboards", "dashboard", "/dashboard/list/"))
	}
	if read(security.ResChart) {
		items = append(items, miscMenuLeaf("Charts", "bar_chart", "/chart/list/"))
	}
	if read(security.ResDataset) {
		items = append(items, miscMenuLeaf("Datasets", "table", "/tablemodelview/list/"))
	}
	if read(security.ResDatabase) {
		items = append(items, miscMenuLeaf("Databases", "database", "/databaseview/list/"))
	}
	if security.HasPerm(r, security.ActionSqllabExport, security.ResSqllab) ||
		security.HasPerm(r, security.ActionWrite, security.ResQuery) {
		items = append(items, miscMenuLeaf("SQL Lab", "sql_lab", "/sqllab/"))
	}
	if read(security.ResReportSchedule) {
		items = append(items, miscMenuLeaf("Alerts & Reports", "bell", "/alert/list/"))
	}

	securityChildren := make([]miscMenuItem, 0, 4)
	if read(security.ResUser) {
		securityChildren = append(securityChildren, miscMenuLeaf("Users", "person", "/users/list/"))
	}
	if read(security.ResRole) {
		securityChildren = append(securityChildren, miscMenuLeaf("Roles", "security", "/roles/list/"))
	}
	if read(security.ResGroup) {
		securityChildren = append(securityChildren, miscMenuLeaf("Groups", "group", "/groups/list/"))
	}
	if read(security.ResRowLevelSecurity) {
		securityChildren = append(securityChildren,
			miscMenuLeaf("Row Level Security", "lock", "/rowlevelsecurity/list/"))
	}
	if len(securityChildren) > 0 {
		items = append(items, miscMenuItem{
			Name: "Security", Label: "Security", Icon: "security",
			URL: "/security/", Children: securityChildren,
		})
	}

	settingsChildren := make([]miscMenuItem, 0, 3)
	if read(security.ResTheme) {
		settingsChildren = append(settingsChildren, miscMenuLeaf("Themes", "palette", "/themes/list/"))
	}
	if read(security.ResCssTemplate) {
		settingsChildren = append(settingsChildren,
			miscMenuLeaf("CSS Templates", "code", "/csstemplatemodelview/list/"))
	}
	if read(security.ResImportExport) {
		settingsChildren = append(settingsChildren,
			miscMenuLeaf("Import Dashboards", "upload", "/dashboard/import/"))
	}
	if len(settingsChildren) > 0 {
		items = append(items, miscMenuItem{
			Name: "Settings", Label: "Settings", Icon: "settings",
			URL: "/settings/", Children: settingsChildren,
		})
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{"result": items})
}

func (a *miscAPI) timeRange(w http.ResponseWriter, r *http.Request) {
	// Relative ranges understood by superset.utils.date_parser.
	type option struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Since string `json:"since"`
		Until string `json:"until"`
	}
	options := []option{
		{"Last day", "Last day", "1 day ago", ""},
		{"Last 7 days", "Last 7 days", "7 days ago", ""},
		{"Last 30 days", "Last 30 days", "30 days ago", ""},
		{"Last 90 days", "Last 90 days", "90 days ago", ""},
		{"Last week", "Last week", "1 week ago", ""},
		{"Last month", "Last month", "1 month ago", ""},
		{"Last quarter", "Last quarter", "3 months ago", ""},
		{"Last year", "Last year", "1 year ago", ""},
		{"Last 12 months", "Last 12 months", "12 months ago", ""},
		{"Previous day", "Previous day", "1 day ago", "1 day ago"},
		{"Previous week", "Previous week", "1 week ago", "1 week ago"},
		{"Previous month", "Previous month", "1 month ago", "1 month ago"},
		{"Previous quarter", "Previous quarter", "3 months ago", "3 months ago"},
		{"Previous year", "Previous year", "1 year ago", "1 year ago"},
		{"No filter", "No filter", "", ""},
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": options})
}
