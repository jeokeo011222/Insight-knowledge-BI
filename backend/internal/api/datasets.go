package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"superset/internal/connectors"
	"superset/internal/datasets"
	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/queries"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterDatasets mounts /api/v1/dataset/*.
func RegisterDatasets(r chi.Router, deps *Deps) {
	d := &datasetAPI{deps: deps}

	r.Route("/dataset", func(dr chi.Router) {
		dr.Use(security.RequireUser)

		dr.With(security.RequirePermission(security.ActionRead, security.ResDataset)).Get("/", d.list)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDataset)).Post("/", d.create)
		dr.With(security.RequirePermission(security.ActionRead, security.ResDataset)).Get("/_info", d.info)

		dr.Get("/{pk}", d.get)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDataset)).Put("/{pk}", d.update)
		dr.With(security.RequirePermission(security.ActionDelete, security.ResDataset)).Delete("/{pk}", d.remove)
		dr.Get("/{pk}/related_objects", d.relatedObjects)
		// samples lets the dataset screen read actual rows from the linked
		// database instead of only showing metadata.
		dr.With(security.RequirePermission(security.ActionRead, security.ResDataset)).Get("/{pk}/samples", d.samples)
		// refresh re-syncs the dataset (external sources are re-pulled) and
		// records the outcome; refresh_config stores the auto-refresh period.
		dr.Post("/{pk}/refresh", d.refresh)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDataset)).
			Put("/{pk}/refresh_config", d.updateRefreshConfig)
		dr.Post("/{pk}/duplicate", d.duplicate)
		dr.Get("/{pk}/export/", d.export)

		dr.Get("/{pk}/column/", d.listColumns)
		dr.Post("/{pk}/column/", d.createColumn)
		dr.Get("/{pk}/column/{column_id}", d.getColumn)
		dr.Put("/{pk}/column/{column_id}", d.updateColumn)
		dr.Delete("/{pk}/column/{column_id}", d.deleteColumn)

		dr.Get("/{pk}/metric/", d.listMetrics)
		dr.Post("/{pk}/metric/", d.createMetric)
		dr.Get("/{pk}/metric/{metric_id}", d.getMetric)
		dr.Put("/{pk}/metric/{metric_id}", d.updateMetric)
		dr.Delete("/{pk}/metric/{metric_id}", d.deleteMetric)

		dr.Post("/validate_sql", d.validateSQL)
		dr.Post("/get_or_create", d.getOrCreate)
		dr.Post("/import/", d.importDataset)

		// External data sources: uploaded files and REST endpoints are
		// materialised into the managed SQLite store and surfaced as datasets.
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDataset)).Post("/upload/", d.uploadDataset)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDataset)).Post("/upload/sheets/", d.listUploadSheets)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDataset)).Post("/api_source/", d.createAPISourceDataset)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDataset)).Post("/api_source/test/", d.testAPISource)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDataset)).Post("/{pk}/refresh_source/", d.refreshSource)
	})
}

type datasetAPI struct {
	deps *Deps

	once sync.Once
	exec *queries.Executor

	refreshOnce sync.Once
	refresher   *datasets.Service
}

// refresherService lazily builds the shared data-refresh service. It is the
// same implementation the beat scheduler uses, so a manual refresh and an
// automatic one can never diverge.
func (d *datasetAPI) refresherService() *datasets.Service {
	d.refreshOnce.Do(func() {
		d.refresher = datasets.New(d.deps.DB, d.deps.Config, d.deps.Log, d.deps.Cache)
	})
	return d.refresher
}

// executor returns the lazily constructed query executor. The dataset sample
// endpoint shares the chart-data executor so authorization, row-level security
// and row limits behave exactly like a chart query.
func (d *datasetAPI) executor() *queries.Executor {
	d.once.Do(func() {
		d.exec = queries.NewFromConfig(d.deps.DB, d.deps.Log, d.deps.Cache, d.deps.Mgr, d.deps.Config)
	})
	return d.exec
}

// samplesLimit bounds the sample row count requested by the UI.
const samplesLimit = 100
const samplesLimitMax = 1000

// samples returns the first rows of a dataset's underlying table (or virtual
// SQL) so the dataset screen can show real data next to the metadata.
func (d *datasetAPI) samples(w http.ResponseWriter, r *http.Request) {
	dataset, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}

	limit := samplesLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, convErr := strconv.Atoi(raw); convErr == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > samplesLimitMax {
		limit = samplesLimitMax
	}

	statement, err := d.sampleStatement(dataset)
	if err != nil {
		server.WriteError(w, err)
		return
	}

	result, err := d.executor().ExecuteReadOnlySQL(r.Context(), userIDOf(r), statement, queries.ReadOnlyOptions{
		DatasetID: dataset.ID,
		RowLimit:  limit,
		// Preview honours column masking for everybody but the compliance
		// officer: the sample grid must not show a value the chart cannot.
		ExemptMasking: maskExempt(r),
	})
	if err != nil {
		server.WriteError(w, err)
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"sql":         result.SQL,
		"columns":     result.ColNames,
		"coltypes":    result.ColTypes,
		"data":        result.Data,
		"rowcount":    result.RowCount,
		"truncated":   result.Truncated,
		"duration_ms": result.DurationMS,
		"limit":       limit,
		"table_name":  dataset.TableNameStr,
		"schema":      dataset.Schema,
	}})
}

// sampleStatement renders the SELECT used by the sample endpoint. Virtual
// datasets are read through their stored SQL, physical ones through the quoted
// schema-qualified table name of their engine.
func (d *datasetAPI) sampleStatement(dataset *models.SqlaTable) (string, error) {
	if sql := strings.TrimSpace(dataset.SQL); sql != "" {
		return strings.TrimRight(sql, "; \t\n"), nil
	}
	if dataset.TableNameStr == "" {
		return "", apperrors.BadRequest("The dataset has neither a table name nor virtual SQL")
	}

	var database models.Database
	if err := d.deps.DB.First(&database, dataset.DatabaseID).Error; err != nil {
		return "", apperrors.NotFound("The database of this dataset no longer exists")
	}
	// Resolve the dialect from the URI: the quoting rules live on the engine
	// spec, and getting them wrong produces a syntax error on any engine whose
	// identifier quote is not `"`.
	spec := connectors.GetOrDefault(database.SQLAlchemyURI)
	table := dataset.TableNameStr
	if dataset.Schema != "" {
		table = dataset.Schema + "." + table
	}
	return spec.SelectStar(table), nil
}

func (d *datasetAPI) datasetJSON(r *http.Request, t *models.SqlaTable) map[string]any {
	var cols []models.TableColumn
	d.deps.DB.WithContext(r.Context()).Where("table_id = ?", t.ID).Order("column_name").Find(&cols)
	columns := make([]map[string]any, 0, len(cols))
	for _, c := range cols {
		columns = append(columns, columnJSON(&c))
	}
	var metrics []models.SqlMetric
	d.deps.DB.WithContext(r.Context()).Where("table_id = ?", t.ID).Order("metric_name").Find(&metrics)
	metricList := make([]map[string]any, 0, len(metrics))
	for _, m := range metrics {
		metricList = append(metricList, metricJSON(&m))
	}
	var dbName string
	var database models.Database
	if err := d.deps.DB.WithContext(r.Context()).First(&database, t.DatabaseID).Error; err == nil {
		dbName = database.DatabaseName
	}
	var owners []struct {
		ID       int
		Username string
	}
	_ = d.deps.DB.WithContext(r.Context()).Raw(`
		SELECT u.id, u.username FROM ab_user u
		JOIN sqlatable_user tu ON tu.user_id = u.id WHERE tu.table_id = ?`, t.ID).Scan(&owners).Error
	ownerList := make([]map[string]any, 0, len(owners))
	for _, o := range owners {
		ownerList = append(ownerList, map[string]any{"id": o.ID, "username": o.Username})
	}
	kind := "physical"
	if t.SQL != "" || t.IsSQLLabView {
		kind = "virtual"
	}
	var source any
	if src, ok := d.extraSource(t); ok {
		kind = "external"
		source = sourceJSON(src)
	}
	return map[string]any{
		"id": t.ID, "uuid": t.UUID, "table_name": t.TableNameStr,
		"main_dttm_col": t.MainDttmCol, "currency_code_column": t.CurrencyCodeColumn,
		"database": map[string]any{"id": t.DatabaseID, "database_name": dbName},
		"schema":   t.Schema, "catalog": t.Catalog, "sql": t.SQL,
		"is_sqllab_view": t.IsSQLLabView, "template_params": t.TemplateParams,
		"extra": t.Extra, "normalize_columns": t.NormalizeColumns,
		"always_filter_main_dttm": t.AlwaysFilterMainDttm,
		"folders":                 jsonField(t.Folders), "description": t.Description,
		"default_endpoint": t.DefaultEndpoint, "is_featured": t.IsFeatured,
		"filter_select_enabled": t.FilterSelectEnabled, "offset": t.Offset,
		"cache_timeout": t.CacheTimeout, "params": t.Params,
		"perm": t.Perm, "schema_perm": t.SchemaPerm,
		"columns": columns, "metrics": metricList, "owners": ownerList,
		"created_on": t.CreatedOn, "changed_on": t.ChangedOn,
		"kind": kind, "source": source, "type": "table", "granularity_sqla": t.MainDttmCol,
		"datasource_type": "table", "datasource_name": t.TableNameStr,
		"name": t.TableNameStr, "verbose_name": t.TableNameStr,

		// Data refresh state (see internal/datasets). The UI needs all of it to
		// show "auto every N minutes / last run failed at …" without a second
		// request per row.
		"refresh_interval_sec":     t.RefreshIntervalSec,
		"last_refreshed_at":        t.LastRefreshedAt,
		"last_refresh_status":      t.LastRefreshStatus,
		"last_refresh_error":       t.LastRefreshError,
		"last_refresh_duration_ms": t.LastRefreshDurationMS,
		"last_refresh_trigger":     t.LastRefreshTrigger,
	}
}

func columnJSON(c *models.TableColumn) map[string]any {
	return map[string]any{
		"id": c.ID, "uuid": c.UUID, "column_name": c.ColumnName,
		"verbose_name": nullIfEmpty(c.VerboseName), "is_active": c.IsActive,
		"type": c.Type, "advanced_data_type": c.AdvancedDataType,
		"groupby": c.Groupby, "filterable": c.Filterable,
		"description": c.Description, "table_id": c.TableID,
		"is_dttm": c.IsDttm, "expression": c.Expression,
		"python_date_format": c.PythonDateFormat, "datetime_format": c.DatetimeFormat,
		"extra": c.Extra, "created_on": c.CreatedOn, "changed_on": c.ChangedOn,
		"mask_type": c.MaskType, "mask_rule": c.MaskRule,
		// Agent exposure (W-01): hidden columns are invisible to the Agent and
		// rejected by its SQL guard; pii_level is the coarse class the editor shows.
		"agent_visible": c.AgentVisible,
		"pii_level":     columnPIILevel(c),
		"masked":        queries.EffectiveMaskRule(*c).Type != "",
	}
}

func metricJSON(m *models.SqlMetric) map[string]any {
	return map[string]any{
		"id": m.ID, "uuid": m.UUID, "metric_name": m.MetricName,
		"verbose_name": nullIfEmpty(m.VerboseName), "metric_type": m.MetricType,
		"description": m.Description, "d3format": m.D3Format,
		"currency": nullIfEmpty(m.Currency), "warning_text": m.WarningText,
		"table_id": m.TableID, "expression": m.Expression, "extra": m.Extra,
		"created_on": m.CreatedOn, "changed_on": m.ChangedOn,
	}
}

type datasetPayload struct {
	TableName            string `json:"table_name"`
	DatabaseID           int    `json:"database_id"`
	Schema               string `json:"schema"`
	Catalog              string `json:"catalog"`
	SQL                  string `json:"sql"`
	MainDttmCol          string `json:"main_dttm_col"`
	Description          string `json:"description"`
	DefaultEndpoint      string `json:"default_endpoint"`
	CacheTimeout         *int   `json:"cache_timeout"`
	Offset               int    `json:"offset"`
	FilterSelectEnabled  *bool  `json:"filter_select_enabled"`
	NormalizeColumns     *bool  `json:"normalize_columns"`
	AlwaysFilterMainDttm *bool  `json:"always_filter_main_dttm"`
	IsFeatured           *bool  `json:"is_featured"`
	TemplateParams       any    `json:"template_params"`
	Extra                any    `json:"extra"`
	Owners               []int  `json:"owners"`
	Columns              []struct {
		ColumnName       string `json:"column_name"`
		VerboseName      string `json:"verbose_name"`
		Type             string `json:"type"`
		IsDttm           bool   `json:"is_dttm"`
		Groupby          *bool  `json:"groupby"`
		Filterable       *bool  `json:"filterable"`
		Expression       string `json:"expression"`
		Description      string `json:"description"`
		DatetimeFormat   string `json:"datetime_format"`
		PythonDateFormat string `json:"python_date_format"`
		IsActive         *bool  `json:"is_active"`
	} `json:"columns"`
}

func (d *datasetAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := d.deps.DB.WithContext(r.Context()).Model(&models.SqlaTable{}).Where("deleted_at IS NULL")
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("table_name ILIKE ? OR description ILIKE ?", like, like)
	}
	if dbID := server.QueryInt(r, "database_id", 0); dbID > 0 {
		q = q.Where("database_id = ?", dbID)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "table_name asc", func(t *models.SqlaTable) any { return d.datasetJSON(r, t) })
}

func (d *datasetAPI) info(w http.ResponseWriter, r *http.Request) {
	var rows []models.SqlaTable
	q := d.deps.DB.WithContext(r.Context()).Where("deleted_at IS NULL")
	if ids := idsParam(r); len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	if err := q.Order("table_name").Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		out = append(out, map[string]any{
			"id": t.ID, "text": t.TableNameStr, "table_name": t.TableNameStr,
			"schema": t.Schema, "database_id": t.DatabaseID,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"count": len(out), "result": out})
}

func (d *datasetAPI) find(r *http.Request) (*models.SqlaTable, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var t models.SqlaTable
	if err := d.deps.DB.WithContext(r.Context()).First(&t, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("%s", localize(r, "error.dataset.not_found", "Dataset not found"))
		}
		return nil, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error"))
	}
	return &t, nil
}

func (d *datasetAPI) get(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": d.datasetJSON(r, t)})
}

func (d *datasetAPI) create(w http.ResponseWriter, r *http.Request) {
	var p datasetPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.TableName == "" || p.DatabaseID == 0 {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.table_and_db_required", "table_name and database_id are required")))
		return
	}
	var existing int64
	d.deps.DB.WithContext(r.Context()).Model(&models.SqlaTable{}).
		Where("database_id = ? AND schema = ? AND table_name = ? AND deleted_at IS NULL",
			p.DatabaseID, p.Schema, p.TableName).Count(&existing)
	if existing > 0 {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.dataset.table_exists", "Table [%s] is already defined", p.TableName)))
		return
	}
	t := models.SqlaTable{
		UUID:                 uuid.NewString(),
		TableNameStr:         p.TableName,
		DatabaseID:           p.DatabaseID,
		Schema:               p.Schema,
		Catalog:              p.Catalog,
		SQL:                  p.SQL,
		MainDttmCol:          p.MainDttmCol,
		Description:          p.Description,
		DefaultEndpoint:      p.DefaultEndpoint,
		CacheTimeout:         p.CacheTimeout,
		Offset:               p.Offset,
		FilterSelectEnabled:  boolOr(p.FilterSelectEnabled, true),
		NormalizeColumns:     boolOr(p.NormalizeColumns, false),
		AlwaysFilterMainDttm: boolOr(p.AlwaysFilterMainDttm, false),
		IsFeatured:           boolOr(p.IsFeatured, false),
		TemplateParams:       jsonString(p.TemplateParams),
		Extra:                jsonString(p.Extra),
		IsSQLLabView:         p.SQL != "",
	}
	t.SchemaPerm = fmt.Sprintf("[%s].[%s](id:%d)", d.dbName(r, p.DatabaseID), p.Schema, 0)
	t.Perm = fmt.Sprintf("[%s].[%s](id:%d)", d.dbName(r, p.DatabaseID), p.TableName, 0)

	err := d.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
		for _, c := range p.Columns {
			col := models.TableColumn{
				UUID: uuid.NewString(), ColumnName: c.ColumnName,
				VerboseName: c.VerboseName, Type: c.Type, IsDttm: c.IsDttm,
				Groupby: boolOr(c.Groupby, true), Filterable: boolOr(c.Filterable, true),
				Expression: c.Expression, Description: c.Description,
				DatetimeFormat: c.DatetimeFormat, PythonDateFormat: c.PythonDateFormat,
				IsActive: boolOr(c.IsActive, true), TableID: t.ID,
			}
			if err := tx.Create(&col).Error; err != nil {
				return err
			}
		}
		if len(p.Owners) == 0 {
			if uid := userIDOf(r); uid > 0 {
				p.Owners = []int{uid}
			}
		}
		for _, uid := range p.Owners {
			if err := tx.Create(&models.SqlatableUser{UserID: uid, TableID: t.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.dataset.create_failed", "Could not create dataset: %s", err.Error())))
		return
	}
	_ = d.deps.DB.WithContext(r.Context()).First(&t, t.ID).Error
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": d.datasetJSON(r, &t)})
}

func (d *datasetAPI) update(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p datasetPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.TableName != "" {
		updates["table_name"] = p.TableName
	}
	if p.Schema != "" {
		updates["schema"] = p.Schema
	}
	if p.SQL != "" {
		updates["sql"] = p.SQL
	}
	if p.Description != "" {
		updates["description"] = p.Description
	}
	if p.MainDttmCol != "" {
		updates["main_dttm_col"] = p.MainDttmCol
	}
	if p.CacheTimeout != nil {
		updates["cache_timeout"] = *p.CacheTimeout
	}
	if p.Offset != 0 {
		updates["offset"] = p.Offset
	}
	if p.FilterSelectEnabled != nil {
		updates["filter_select_enabled"] = *p.FilterSelectEnabled
	}
	if p.NormalizeColumns != nil {
		updates["normalize_columns"] = *p.NormalizeColumns
	}
	if p.AlwaysFilterMainDttm != nil {
		updates["always_filter_main_dttm"] = *p.AlwaysFilterMainDttm
	}
	if p.IsFeatured != nil {
		updates["is_featured"] = *p.IsFeatured
	}
	if p.Extra != nil {
		updates["extra"] = jsonString(p.Extra)
	}
	if p.TemplateParams != nil {
		updates["template_params"] = jsonString(p.TemplateParams)
	}
	if err := d.deps.DB.WithContext(r.Context()).Model(t).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.dataset.update_failed", "Could not update dataset: %s", err.Error())))
		return
	}
	if p.Owners != nil {
		db := d.deps.DB.WithContext(r.Context())
		db.Where("table_id = ?", t.ID).Delete(&models.SqlatableUser{})
		for _, uid := range p.Owners {
			db.Create(&models.SqlatableUser{UserID: uid, TableID: t.ID})
		}
	}
	_ = d.deps.DB.WithContext(r.Context()).First(t, t.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": d.datasetJSON(r, t)})
}

func (d *datasetAPI) remove(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := d.deps.DB.WithContext(r.Context())
	// Soft delete mirrors Superset: datasets go to the recycle bin.
	now := nowPtr()
	if err := db.Model(t).Updates(map[string]any{"deleted_at": now}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

func (d *datasetAPI) relatedObjects(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var charts []models.Slice
	d.deps.DB.WithContext(r.Context()).Raw(`
		SELECT s.* FROM slices s WHERE s.datasource_id = ? AND s.datasource_type = 'table'`, t.ID).Scan(&charts)
	chartList := make([]map[string]any, 0, len(charts))
	for _, c := range charts {
		chartList = append(chartList, map[string]any{
			"id": c.ID, "slice_name": c.SliceName, "viz_type": c.VizType,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"charts": chartList, "dashboards": []any{}})
}

// refresh re-syncs a dataset on demand: external sources are re-pulled, while
// physical and virtual datasets have their columns re-read from the source.
// The outcome (including a failure) is stored on the dataset so the UI can show
// the real state of the last run.
func (d *datasetAPI) refresh(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	outcome, err := d.refresherService().Refresh(r.Context(), t.ID, datasets.TriggerManual)
	if err != nil {
		if errors.Is(err, datasets.ErrNotFound) {
			server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.dataset.not_found", "Dataset not found")))
			return
		}
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.sync_failed", "Could not refresh the dataset: %s", err.Error())))
		return
	}
	_ = d.deps.DB.WithContext(r.Context()).First(t, t.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result":  d.datasetJSON(r, t),
		"refresh": outcome,
	})
}

// refreshConfigRequest is the auto-refresh body. A nil interval is rejected so a
// malformed request cannot silently disable automatic refresh.
type refreshConfigRequest struct {
	IntervalSec *int `json:"interval_sec"`
}

// updateRefreshConfig stores how often a dataset should refresh itself.
func (d *datasetAPI) updateRefreshConfig(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p refreshConfigRequest
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.IntervalSec == nil {
		server.WriteError(w, apperrors.BadRequest("interval_sec is required"))
		return
	}
	interval := *p.IntervalSec
	if interval != 0 && (interval < datasets.MinIntervalSec || interval > datasets.MaxIntervalSec) {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.refresh_interval_range",
			"The refresh interval must be between 60 seconds and 30 days")))
		return
	}
	if err := d.deps.DB.WithContext(r.Context()).Model(&models.SqlaTable{}).
		Where("id = ?", t.ID).Update("refresh_interval_sec", interval).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	_ = d.deps.DB.WithContext(r.Context()).First(t, t.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": d.datasetJSON(r, t)})
}

func (d *datasetAPI) duplicate(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var body struct {
		TableName string `json:"table_name"`
	}
	_ = security.DecodeBody(r, &body)
	name := body.TableName
	if name == "" {
		name = t.TableNameStr + "_copy"
	}
	clone := *t
	clone.ID = 0
	clone.UUID = uuid.NewString()
	clone.TableNameStr = name
	clone.CreatedOn = time.Time{}
	clone.ChangedOn = time.Time{}
	if err := d.deps.DB.WithContext(r.Context()).Create(&clone).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.chart.duplicate_failed", "Could not duplicate: %s", err.Error())))
		return
	}
	var cols []models.TableColumn
	d.deps.DB.WithContext(r.Context()).Where("table_id = ?", t.ID).Find(&cols)
	for _, c := range cols {
		c.ID = 0
		c.UUID = uuid.NewString()
		c.TableID = clone.ID
		d.deps.DB.WithContext(r.Context()).Create(&c)
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": d.datasetJSON(r, &clone)})
}

func (d *datasetAPI) export(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=dataset_%s_export.json", t.TableNameStr))
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"__version": "1.0.0", "type": "dataset", "dataset": d.datasetJSON(r, t),
	})
}

// Columns ----------------------------------------------------------------

func (d *datasetAPI) listColumns(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var cols []models.TableColumn
	if err := d.deps.DB.WithContext(r.Context()).Where("table_id = ?", t.ID).Order("column_name").Find(&cols).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(cols))
	for i := range cols {
		out = append(out, columnJSON(&cols[i]))
	}
	server.WritePaginated(w, int64(len(out)), out, nil, nil)
}

type columnPayload struct {
	ColumnName       string `json:"column_name"`
	VerboseName      string `json:"verbose_name"`
	Type             string `json:"type"`
	IsDttm           *bool  `json:"is_dttm"`
	Groupby          *bool  `json:"groupby"`
	Filterable       *bool  `json:"filterable"`
	Expression       string `json:"expression"`
	Description      string `json:"description"`
	DatetimeFormat   string `json:"datetime_format"`
	PythonDateFormat string `json:"python_date_format"`
	IsActive         *bool  `json:"is_active"`
	// Column-level masking: "" (off), phone, idcard, email, name, full, custom.
	MaskType string `json:"mask_type"`
	MaskRule string `json:"mask_rule"`
	// Agent exposure: nil keeps the current value on update (default visible).
	AgentVisible *bool  `json:"agent_visible"`
	PIILevel     string `json:"pii_level"`
}

func (d *datasetAPI) createColumn(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p columnPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	c := models.TableColumn{
		UUID: uuid.NewString(), ColumnName: p.ColumnName, VerboseName: p.VerboseName,
		Type: p.Type, IsDttm: boolOr(p.IsDttm, false), Groupby: boolOr(p.Groupby, true),
		Filterable: boolOr(p.Filterable, true), Expression: p.Expression,
		Description: p.Description, DatetimeFormat: p.DatetimeFormat,
		PythonDateFormat: p.PythonDateFormat, IsActive: boolOr(p.IsActive, true),
		MaskType: p.MaskType, MaskRule: p.MaskRule,
		// New columns are visible to the Agent unless the caller says otherwise:
		// an invisible-by-default column would silently disappear from every
		// answer after a schema sync.
		AgentVisible: boolOr(p.AgentVisible, true),
		PIILevel:     normalizePIILevel(p.PIILevel),
		TableID:      t.ID,
	}
	if err := d.deps.DB.WithContext(r.Context()).Create(&c).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.dataset.create_column_failed", "Could not create column: %s", err.Error())))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": columnJSON(&c)})
}

func (d *datasetAPI) getColumn(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	cid, err := server.URLParamInt(r, "column_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var c models.TableColumn
	if err := d.deps.DB.WithContext(r.Context()).Where("table_id = ? AND id = ?", t.ID, cid).First(&c).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.dataset.column_not_found", "Column not found")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": columnJSON(&c)})
}

func (d *datasetAPI) updateColumn(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	cid, err := server.URLParamInt(r, "column_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var c models.TableColumn
	db := d.deps.DB.WithContext(r.Context())
	if err := db.Where("table_id = ? AND id = ?", t.ID, cid).First(&c).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.dataset.column_not_found", "Column not found")))
		return
	}
	var p columnPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{}
	if p.ColumnName != "" {
		updates["column_name"] = p.ColumnName
	}
	if p.Type != "" {
		updates["type"] = p.Type
	}
	if p.IsDttm != nil {
		updates["is_dttm"] = *p.IsDttm
	}
	if p.Groupby != nil {
		updates["groupby"] = *p.Groupby
	}
	if p.Filterable != nil {
		updates["filterable"] = *p.Filterable
	}
	if p.IsActive != nil {
		updates["is_active"] = *p.IsActive
	}
	if p.Expression != "" {
		updates["expression"] = p.Expression
	}
	if p.Description != "" {
		updates["description"] = p.Description
	}
	if p.VerboseName != "" {
		updates["verbose_name"] = p.VerboseName
	}
	// Mask fields are settable to "" (clearing the rule) so they are always
	// written, unlike the other string fields above.
	updates["mask_type"] = p.MaskType
	updates["mask_rule"] = p.MaskRule
	updates["pii_level"] = normalizePIILevel(p.PIILevel)
	if p.AgentVisible != nil {
		updates["agent_visible"] = *p.AgentVisible
	}
	if err := db.Model(&c).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	// Reload so the response carries the persisted values, not the stale row.
	_ = db.First(&c, c.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": columnJSON(&c)})
}

func (d *datasetAPI) deleteColumn(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	cid, err := server.URLParamInt(r, "column_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := d.deps.DB.WithContext(r.Context()).
		Where("table_id = ? AND id = ?", t.ID, cid).Delete(&models.TableColumn{}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Metrics ----------------------------------------------------------------

func (d *datasetAPI) listMetrics(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var metrics []models.SqlMetric
	if err := d.deps.DB.WithContext(r.Context()).Where("table_id = ?", t.ID).Order("metric_name").Find(&metrics).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(metrics))
	for i := range metrics {
		out = append(out, metricJSON(&metrics[i]))
	}
	server.WritePaginated(w, int64(len(out)), out, nil, nil)
}

type metricPayload struct {
	MetricName  string `json:"metric_name"`
	VerboseName string `json:"verbose_name"`
	Expression  string `json:"expression"`
	MetricType  string `json:"metric_type"`
	Description string `json:"description"`
	D3Format    string `json:"d3format"`
	WarningText string `json:"warning_text"`
	Currency    string `json:"currency"`
}

func (d *datasetAPI) createMetric(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p metricPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.MetricName == "" || p.Expression == "" {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.metric_fields_required", "metric_name and expression are required")))
		return
	}
	m := models.SqlMetric{
		UUID: uuid.NewString(), MetricName: p.MetricName, VerboseName: p.VerboseName,
		Expression: p.Expression, MetricType: p.MetricType, Description: p.Description,
		D3Format: p.D3Format, WarningText: p.WarningText, Currency: p.Currency, TableID: t.ID,
	}
	if err := d.deps.DB.WithContext(r.Context()).Create(&m).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.dataset.create_metric_failed", "Could not create metric: %s", err.Error())))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": metricJSON(&m)})
}

func (d *datasetAPI) getMetric(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	mid, err := server.URLParamInt(r, "metric_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var m models.SqlMetric
	if err := d.deps.DB.WithContext(r.Context()).Where("table_id = ? AND id = ?", t.ID, mid).First(&m).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.dataset.metric_not_found", "Metric not found")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": metricJSON(&m)})
}

func (d *datasetAPI) updateMetric(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	mid, err := server.URLParamInt(r, "metric_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var m models.SqlMetric
	db := d.deps.DB.WithContext(r.Context())
	if err := db.Where("table_id = ? AND id = ?", t.ID, mid).First(&m).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.dataset.metric_not_found", "Metric not found")))
		return
	}
	var p metricPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{}
	if p.MetricName != "" {
		updates["metric_name"] = p.MetricName
	}
	if p.Expression != "" {
		updates["expression"] = p.Expression
	}
	if p.Description != "" {
		updates["description"] = p.Description
	}
	if p.D3Format != "" {
		updates["d3format"] = p.D3Format
	}
	if p.VerboseName != "" {
		updates["verbose_name"] = p.VerboseName
	}
	if err := db.Model(&m).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": metricJSON(&m)})
}

func (d *datasetAPI) deleteMetric(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	mid, err := server.URLParamInt(r, "metric_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := d.deps.DB.WithContext(r.Context()).
		Where("table_id = ? AND id = ?", t.ID, mid).Delete(&models.SqlMetric{}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Validate / get_or_create / import ---------------------------------------

func (d *datasetAPI) validateSQL(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SQL            string `json:"sql"`
		DatabaseID     int    `json:"database_id"`
		Schema         string `json:"schema"`
		TemplateParams any    `json:"template_params"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	var database models.Database
	if err := d.deps.DB.WithContext(r.Context()).First(&database, body.DatabaseID).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.database.not_found", "Database not found")))
		return
	}
	h, err := openPool(database.SQLAlchemyURI)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	defer h.Close()
	probe := "SELECT * FROM (" + body.SQL + ") AS dataset_sql " + h.spec.LimitClause(1)
	rows, err := h.Query(r.Context(), probe)
	if err != nil {
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"message": "SQL is invalid", "result": []any{},
			"errors": []map[string]any{{"message": err.Error()}},
		})
		return
	}
	columns := make([]map[string]any, 0)
	if len(rows) > 0 {
		for name, v := range rows[0] {
			columns = append(columns, map[string]any{
				"column_name": name, "type": inferType(v), "is_dttm": false,
			})
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "SQL is valid", "result": columns, "errors": []any{}})
}

func inferType(v any) string {
	switch v.(type) {
	case float64, float32, int, int64, int32:
		return "FLOAT"
	case bool:
		return "BOOLEAN"
	case nil:
		return "STRING"
	default:
		return "STRING"
	}
}

func (d *datasetAPI) getOrCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TableName  string `json:"table_name"`
		DatabaseID int    `json:"database_id"`
		Schema     string `json:"schema"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	var t models.SqlaTable
	err := d.deps.DB.WithContext(r.Context()).
		Where("database_id = ? AND schema = ? AND table_name = ?", body.DatabaseID, body.Schema, body.TableName).
		First(&t).Error
	if err == nil {
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
			"table_id": t.ID, "extra": map[string]any{"database_id": t.DatabaseID, "schema": t.Schema},
		}})
		return
	}
	created := models.SqlaTable{
		UUID: uuid.NewString(), TableNameStr: body.TableName,
		DatabaseID: body.DatabaseID, Schema: body.Schema,
		FilterSelectEnabled: true,
	}
	if err := d.deps.DB.WithContext(r.Context()).Create(&created).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.dataset.create_failed", "Could not create dataset: %s", err.Error())))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": map[string]any{
		"table_id": created.ID, "extra": map[string]any{"database_id": created.DatabaseID, "schema": created.Schema},
	}})
}

func (d *datasetAPI) importDataset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Datasets []datasetPayload `json:"datasets"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	created := make([]int, 0, len(body.Datasets))
	for _, p := range body.Datasets {
		var existing models.Database
		if err := d.deps.DB.WithContext(r.Context()).First(&existing, p.DatabaseID).Error; err != nil {
			continue
		}
		t := models.SqlaTable{
			UUID: uuid.NewString(), TableNameStr: p.TableName, DatabaseID: p.DatabaseID,
			Schema: p.Schema, SQL: p.SQL, Description: p.Description,
			MainDttmCol: p.MainDttmCol, FilterSelectEnabled: true,
		}
		if err := d.deps.DB.WithContext(r.Context()).Create(&t).Error; err == nil {
			created = append(created, t.ID)
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK", "result": created})
}

func (d *datasetAPI) dbName(r *http.Request, id int) string {
	var db models.Database
	if err := d.deps.DB.WithContext(r.Context()).First(&db, id).Error; err != nil {
		return ""
	}
	return db.DatabaseName
}

var (
	_ = json.Marshal
	_ = connectors.Get
	_ = context.Background
)
