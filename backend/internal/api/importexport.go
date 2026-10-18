package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterImportExport mounts /api/v1/importexport/*
// (superset/importexport/api.py).
func RegisterImportExport(r chi.Router, deps *Deps) {
	a := &importExportAPI{deps: deps}

	r.Route("/importexport", func(ir chi.Router) {
		ir.Use(security.RequireUser)
		ir.With(security.RequirePermission(security.ActionImport, security.ResImportExport)).
			Post("/import/", a.importAssets)
		ir.With(security.RequirePermission(security.ActionExport, security.ResImportExport)).
			Get("/export/{type}/{id}/", a.export)
	})
}

type importExportAPI struct{ deps *Deps }

const importExportVersion = "1.0.0"

// Import ------------------------------------------------------------------

// importAssets accepts a multipart upload or a JSON {type, contents} body.
func (a *importExportAPI) importAssets(w http.ResponseWriter, r *http.Request) {
	assetType, raw, err := importExportRead(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var bundle map[string]any
	if err := json.Unmarshal(raw, &bundle); err != nil {
		server.WriteError(w, apperrors.BadRequest("Invalid import payload: %s", err.Error()))
		return
	}
	if assetType == "" {
		assetType = ieString(bundle, "type")
	}
	if assetType == "" {
		// Infer the asset type from the bundle's top-level keys.
		switch {
		case ieMap(bundle["dashboard"]) != nil || len(ieList(bundle["dashboards"])) > 0:
			assetType = "dashboard"
		case ieMap(bundle["chart"]) != nil || len(ieList(bundle["charts"])) > 0:
			assetType = "chart"
		case ieMap(bundle["dataset"]) != nil || len(ieList(bundle["datasets"])) > 0:
			assetType = "dataset"
		case ieMap(bundle["database"]) != nil || len(ieList(bundle["databases"])) > 0:
			assetType = "database"
		}
	}

	var result map[string]any
	switch strings.ToLower(assetType) {
	case "database":
		result, err = a.importDatabase(r, bundle)
	case "dataset":
		result, err = a.importDataset(r, bundle)
	case "chart":
		result, err = a.importChart(r, bundle)
	case "dashboard":
		result, err = a.importDashboard(r, bundle)
	default:
		server.WriteError(w, apperrors.BadRequest("Unsupported import type %q", assetType))
		return
	}
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK", "result": result})
}

// importExportRead extracts the asset type and JSON body from the request.
func importExportRead(r *http.Request) (string, []byte, error) {
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return "", nil, apperrors.BadRequest("Could not parse multipart body: %s", err.Error())
		}
		assetType := r.FormValue("type")
		for _, field := range []string{"file", "formData"} {
			f, _, err := r.FormFile(field)
			if err != nil {
				continue
			}
			defer f.Close()
			b, err := io.ReadAll(f)
			if err != nil {
				return "", nil, apperrors.BadRequest("Could not read upload: %s", err.Error())
			}
			return assetType, b, nil
		}
		return "", nil, apperrors.BadRequest("No file uploaded")
	}

	var body struct {
		Type     string          `json:"type"`
		Contents json.RawMessage `json:"contents"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		return "", nil, err
	}
	if len(body.Contents) == 0 {
		return "", nil, apperrors.BadRequest("contents is required")
	}
	var asString string
	if err := json.Unmarshal(body.Contents, &asString); err == nil {
		return body.Type, []byte(asString), nil
	}
	return body.Type, body.Contents, nil
}

func (a *importExportAPI) importDatabase(r *http.Request, bundle map[string]any) (map[string]any, error) {
	ref := ieMap(bundle["database"])
	if ref == nil {
		ref = ieFirst(bundle["databases"])
	}
	db, err := a.ieEnsureDatabase(r, ref)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"database": map[string]any{"id": db.ID, "database_name": db.DatabaseName},
	}, nil
}

func (a *importExportAPI) importDataset(r *http.Request, bundle map[string]any) (map[string]any, error) {
	ref := ieMap(bundle["dataset"])
	if ref == nil {
		ref = ieFirst(bundle["datasets"])
	}
	if ref == nil {
		return nil, apperrors.BadRequest("dataset is required")
	}
	var dbRef map[string]any
	if m := ieMap(bundle["database"]); m != nil {
		dbRef = m
	} else if m := ieFirst(bundle["databases"]); m != nil {
		dbRef = m
	} else if m := ieMap(ref["database"]); m != nil {
		dbRef = m
	}
	t, err := a.ieEnsureDataset(r, ref, dbRef, 0)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"dataset": map[string]any{"id": t.ID, "table_name": t.TableNameStr, "database_id": t.DatabaseID},
	}, nil
}

func (a *importExportAPI) importChart(r *http.Request, bundle map[string]any) (map[string]any, error) {
	ref := ieMap(bundle["chart"])
	if ref == nil {
		ref = ieFirst(bundle["charts"])
	}
	if ref == nil {
		return nil, apperrors.BadRequest("chart is required")
	}
	datasetRef := ieMap(bundle["dataset"])
	var dbRef map[string]any
	if m := ieMap(bundle["database"]); m != nil {
		dbRef = m
	} else if m := ieFirst(bundle["databases"]); m != nil {
		dbRef = m
	}
	// Charts refer to their dataset by the id from the exporting instance;
	// re-import bundled datasets and remap the datasource reference.
	datasets := ieList(bundle["datasets"])
	if len(datasets) > 0 {
		idMap := map[int]int{}
		for _, m := range datasets {
			t, err := a.ieEnsureDataset(r, m, dbRef, 0)
			if err != nil {
				return nil, err
			}
			idMap[ieInt(m, "id")] = t.ID
		}
		if oldID := ieInt(ref, "datasource_id"); oldID != 0 {
			if newID, ok := idMap[oldID]; ok {
				ref["datasource_id"] = newID
			}
		}
		datasetRef = nil
	}
	chart, err := a.ieEnsureChart(r, ref, datasetRef, dbRef)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"chart": map[string]any{"id": chart.ID, "slice_name": chart.SliceName},
	}, nil
}

func (a *importExportAPI) importDashboard(r *http.Request, bundle map[string]any) (map[string]any, error) {
	ref := ieMap(bundle["dashboard"])
	if ref == nil {
		ref = ieFirst(bundle["dashboards"])
	}
	if ref == nil {
		return nil, apperrors.BadRequest("dashboard is required")
	}

	// Databases first so datasets and charts can resolve their dependencies.
	for _, m := range ieList(bundle["databases"]) {
		if _, err := a.ieEnsureDatabase(r, m); err != nil {
			return nil, err
		}
	}
	var dbRef map[string]any
	if m := ieMap(bundle["database"]); m != nil {
		dbRef = m
	}

	datasetIDs := make([]int, 0)
	datasetIDMap := map[int]int{}
	for _, m := range ieList(bundle["datasets"]) {
		t, err := a.ieEnsureDataset(r, m, dbRef, 0)
		if err != nil {
			return nil, err
		}
		datasetIDs = append(datasetIDs, t.ID)
		datasetIDMap[ieInt(m, "id")] = t.ID
	}

	chartIDs := make([]int, 0)
	for _, m := range ieList(bundle["charts"]) {
		if oldID := ieInt(m, "datasource_id"); oldID != 0 {
			if newID, ok := datasetIDMap[oldID]; ok {
				m["datasource_id"] = newID
			}
		}
		c, err := a.ieEnsureChart(r, m, nil, dbRef)
		if err != nil {
			return nil, err
		}
		chartIDs = append(chartIDs, c.ID)
	}

	dash, err := a.ieEnsureDashboard(r, ref, chartIDs)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"dashboard": map[string]any{"id": dash.ID, "dashboard_title": dash.DashboardTitle},
		"charts":    chartIDs, "datasets": datasetIDs,
	}, nil
}

// Upsert helpers ----------------------------------------------------------

func (a *importExportAPI) ieEnsureDatabase(r *http.Request, ref map[string]any) (*models.Database, error) {
	if ref == nil {
		return nil, apperrors.BadRequest("database information is required")
	}
	db := a.deps.DB.WithContext(r.Context())
	uid := ieString(ref, "uuid")
	if uid != "" {
		var existing models.Database
		if err := db.Where("uuid = ?", uid).First(&existing).Error; err == nil {
			return &existing, nil
		}
	}
	name := ieString(ref, "database_name")
	if name == "" {
		return nil, apperrors.BadRequest("database_name is required")
	}
	var existing models.Database
	if err := db.Where("database_name = ?", name).First(&existing).Error; err == nil {
		return &existing, nil
	}
	uri := ieString(ref, "sqlalchemy_uri")
	if uri == "" {
		return nil, apperrors.BadRequest("sqlalchemy_uri is required to import database %q", name)
	}
	created := models.Database{
		UUID:                orDefault(uid, uuid.NewString()),
		DatabaseName:        name,
		VerboseName:         orDefault(ieString(ref, "verbose_name"), name),
		SQLAlchemyURI:       uri,
		ConfigurationMethod: orDefault(ieString(ref, "configuration_method"), "sqlalchemy_form"),
		ExposeInSQLlab:      ieBool(ref, "expose_in_sqllab", true),
		AllowRunAsync:       ieBool(ref, "allow_run_async", false),
		AllowFileUpload:     ieBool(ref, "allow_file_upload", false),
		AllowCTAS:           ieBool(ref, "allow_ctas", false),
		AllowCVAS:           ieBool(ref, "allow_cvas", false),
		AllowDML:            ieBool(ref, "allow_dml", false),
		ForceCTASSchema:     ieString(ref, "force_ctas_schema"),
		ImpersonateUser:     ieBool(ref, "impersonate_user", false),
		Extra:               jsonString(ref["extra"]),
	}
	if err := db.Create(&created).Error; err != nil {
		return nil, apperrors.Conflict("Could not import database: %s", err.Error())
	}
	return &created, nil
}

func (a *importExportAPI) ieEnsureDataset(r *http.Request, ref, dbRef map[string]any, fallbackDBID int) (*models.SqlaTable, error) {
	if ref == nil {
		return nil, apperrors.BadRequest("dataset information is required")
	}
	db := a.deps.DB.WithContext(r.Context())

	tableName := ieString(ref, "table_name")
	if tableName == "" {
		return nil, apperrors.BadRequest("table_name is required")
	}
	schema := ieString(ref, "schema")

	var existing models.SqlaTable
	if uid := ieString(ref, "uuid"); uid != "" {
		if err := db.Where("uuid = ?", uid).First(&existing).Error; err == nil {
			return &existing, nil
		}
	}
	if fallbackDBID != 0 {
		if err := db.Where("database_id = ? AND schema = ? AND table_name = ? AND deleted_at IS NULL",
			fallbackDBID, schema, tableName).First(&existing).Error; err == nil {
			return &existing, nil
		}
	}

	databaseID := fallbackDBID
	if dbRef != nil {
		database, err := a.ieEnsureDatabase(r, dbRef)
		if err != nil {
			return nil, err
		}
		databaseID = database.ID
	} else if v := ieInt(ref, "database_id"); v > 0 {
		databaseID = v
	}
	if databaseID == 0 {
		return nil, apperrors.BadRequest("database is required to import dataset %q", tableName)
	}

	created := models.SqlaTable{
		UUID:                orDefault(ieString(ref, "uuid"), uuid.NewString()),
		TableNameStr:        tableName,
		DatabaseID:          databaseID,
		Schema:              schema,
		Catalog:             ieString(ref, "catalog"),
		SQL:                 ieString(ref, "sql"),
		MainDttmCol:         ieString(ref, "main_dttm_col"),
		Description:         ieString(ref, "description"),
		DefaultEndpoint:     ieString(ref, "default_endpoint"),
		IsFeatured:          ieBool(ref, "is_featured", false),
		FilterSelectEnabled: ieBool(ref, "filter_select_enabled", true),
		CacheTimeout:        ieIntPtr(ref, "cache_timeout"),
		TemplateParams:      jsonString(ref["template_params"]),
		Extra:               jsonString(ref["extra"]),
		IsSQLLabView:        ieString(ref, "sql") != "",
	}
	if err := db.Create(&created).Error; err != nil {
		return nil, apperrors.Conflict("Could not import dataset: %s", err.Error())
	}

	for _, cm := range ieList(ref["columns"]) {
		col := models.TableColumn{
			UUID:             orDefault(ieString(cm, "uuid"), uuid.NewString()),
			ColumnName:       ieString(cm, "column_name"),
			VerboseName:      ieString(cm, "verbose_name"),
			Type:             ieString(cm, "type"),
			IsDttm:           ieBool(cm, "is_dttm", false),
			Groupby:          ieBool(cm, "groupby", true),
			Filterable:       ieBool(cm, "filterable", true),
			IsActive:         ieBool(cm, "is_active", true),
			Expression:       ieString(cm, "expression"),
			Description:      ieString(cm, "description"),
			PythonDateFormat: ieString(cm, "python_date_format"),
			DatetimeFormat:   ieString(cm, "datetime_format"),
			TableID:          created.ID,
		}
		if col.ColumnName == "" {
			continue
		}
		if err := db.Create(&col).Error; err != nil {
			return nil, apperrors.Conflict("Could not import dataset column: %s", err.Error())
		}
	}
	for _, mm := range ieList(ref["metrics"]) {
		metric := models.SqlMetric{
			UUID:        orDefault(ieString(mm, "uuid"), uuid.NewString()),
			MetricName:  ieString(mm, "metric_name"),
			VerboseName: ieString(mm, "verbose_name"),
			MetricType:  ieString(mm, "metric_type"),
			Description: ieString(mm, "description"),
			D3Format:    ieString(mm, "d3format"),
			Currency:    ieString(mm, "currency"),
			WarningText: ieString(mm, "warning_text"),
			Expression:  ieString(mm, "expression"),
			TableID:     created.ID,
		}
		if metric.MetricName == "" || metric.Expression == "" {
			continue
		}
		if err := db.Create(&metric).Error; err != nil {
			return nil, apperrors.Conflict("Could not import dataset metric: %s", err.Error())
		}
	}
	return &created, nil
}

func (a *importExportAPI) ieEnsureChart(r *http.Request, ref, datasetRef, dbRef map[string]any) (*models.Slice, error) {
	if ref == nil {
		return nil, apperrors.BadRequest("chart information is required")
	}
	db := a.deps.DB.WithContext(r.Context())
	name := ieString(ref, "slice_name")
	if name == "" {
		return nil, apperrors.BadRequest("slice_name is required")
	}
	var existing models.Slice
	if uid := ieString(ref, "uuid"); uid != "" {
		if err := db.Where("uuid = ?", uid).First(&existing).Error; err == nil {
			return &existing, nil
		}
	}

	datasourceType := orDefault(ieString(ref, "datasource_type"), "table")
	datasourceID := ieInt(ref, "datasource_id")
	if datasetRef != nil {
		dataset, err := a.ieEnsureDataset(r, datasetRef, dbRef, 0)
		if err != nil {
			return nil, err
		}
		datasourceType = "table"
		datasourceID = dataset.ID
	}
	if datasourceID == 0 {
		return nil, apperrors.BadRequest("dataset is required to import chart %q", name)
	}
	var datasourceName string
	if datasourceType == "table" {
		var dataset models.SqlaTable
		if err := db.First(&dataset, datasourceID).Error; err == nil {
			datasourceName = dataset.TableNameStr
		}
	}

	created := models.Slice{
		UUID:           orDefault(ieString(ref, "uuid"), uuid.NewString()),
		SliceName:      name,
		DatasourceID:   datasourceID,
		DatasourceType: datasourceType,
		DatasourceName: orDefault(ieString(ref, "datasource_name"), datasourceName),
		VizType:        ieString(ref, "viz_type"),
		Params:         datatypes.JSON(ieJSONBytes(ref["params"])),
		QueryContext:   datatypes.JSON(ieJSONBytes(ref["query_context"])),
		Description:    ieString(ref, "description"),
		CacheTimeout:   ieIntPtr(ref, "cache_timeout"),
	}
	if err := db.Create(&created).Error; err != nil {
		return nil, apperrors.Conflict("Could not import chart: %s", err.Error())
	}
	return &created, nil
}

func (a *importExportAPI) ieEnsureDashboard(r *http.Request, ref map[string]any, chartIDs []int) (*models.Dashboard, error) {
	if ref == nil {
		return nil, apperrors.BadRequest("dashboard information is required")
	}
	db := a.deps.DB.WithContext(r.Context())
	title := ieString(ref, "dashboard_title")
	if title == "" {
		return nil, apperrors.BadRequest("dashboard_title is required")
	}
	var existing models.Dashboard
	if uid := ieString(ref, "uuid"); uid != "" {
		if err := db.Where("uuid = ?", uid).First(&existing).Error; err == nil {
			a.ieLinkDashboardCharts(r, existing.ID, chartIDs)
			return &existing, nil
		}
	}
	created := models.Dashboard{
		UUID:           orDefault(ieString(ref, "uuid"), uuid.NewString()),
		DashboardTitle: title,
		Slug:           ieString(ref, "slug"),
		Description:    ieString(ref, "description"),
		CSS:            ieString(ref, "css"),
		PositionJSON:   datatypes.JSON(ieJSONBytes(ref["position_json"])),
		JSONMetadata:   datatypes.JSON(ieJSONBytes(ref["json_metadata"])),
		Published:      ieBool(ref, "published", false),
	}
	if err := db.Create(&created).Error; err != nil {
		return nil, apperrors.Conflict("Could not import dashboard: %s", err.Error())
	}
	a.ieLinkDashboardCharts(r, created.ID, chartIDs)
	return &created, nil
}

func (a *importExportAPI) ieLinkDashboardCharts(r *http.Request, dashboardID int, chartIDs []int) {
	db := a.deps.DB.WithContext(r.Context())
	for _, chartID := range chartIDs {
		var count int64
		db.Model(&models.DashboardSlices{}).
			Where("dashboard_id = ? AND slice_id = ?", dashboardID, chartID).Count(&count)
		if count == 0 {
			db.Create(&models.DashboardSlices{DashboardID: dashboardID, SliceID: chartID})
		}
	}
}

// Export ------------------------------------------------------------------

func (a *importExportAPI) export(w http.ResponseWriter, r *http.Request) {
	assetType := strings.ToLower(server.URLParam(r, "type"))
	id, err := server.URLParamInt(r, "id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())

	var payload map[string]any
	switch assetType {
	case "database":
		var database models.Database
		if err := db.First(&database, id).Error; err != nil {
			server.WriteError(w, apperrors.NotFound("Database not found"))
			return
		}
		datasets := make([]map[string]any, 0)
		var rows []models.SqlaTable
		db.Where("database_id = ?", database.ID).Find(&rows)
		for i := range rows {
			datasets = append(datasets, ieDatasetJSON(db, &rows[i]))
		}
		payload = map[string]any{
			"__version": importExportVersion, "type": "database",
			"database": ieDatabaseJSON(&database), "datasets": datasets,
		}
	case "dataset":
		var dataset models.SqlaTable
		if err := db.First(&dataset, id).Error; err != nil {
			server.WriteError(w, apperrors.NotFound("Dataset not found"))
			return
		}
		var database models.Database
		db.First(&database, dataset.DatabaseID)
		var charts []models.Slice
		db.Where("datasource_type = ? AND datasource_id = ?", "table", dataset.ID).Find(&charts)
		chartList := make([]map[string]any, 0, len(charts))
		for i := range charts {
			chartList = append(chartList, ieChartJSON(&charts[i]))
		}
		payload = map[string]any{
			"__version": importExportVersion, "type": "dataset",
			"dataset": ieDatasetJSON(db, &dataset), "database": ieDatabaseJSON(&database),
			"charts": chartList,
		}
	case "chart":
		var chart models.Slice
		if err := db.First(&chart, id).Error; err != nil {
			server.WriteError(w, apperrors.NotFound("Chart not found"))
			return
		}
		payload = map[string]any{
			"__version": importExportVersion, "type": "chart",
			"chart": ieChartJSON(&chart),
		}
		if chart.DatasourceType == "table" {
			var dataset models.SqlaTable
			if err := db.First(&dataset, chart.DatasourceID).Error; err == nil {
				payload["dataset"] = ieDatasetJSON(db, &dataset)
				var database models.Database
				if err := db.First(&database, dataset.DatabaseID).Error; err == nil {
					payload["database"] = ieDatabaseJSON(&database)
				}
			}
		}
		var dashboards []models.Dashboard
		db.Raw(`SELECT d.* FROM dashboards d JOIN dashboard_slices ds ON ds.dashboard_id = d.id
			WHERE ds.slice_id = ? GROUP BY d.id ORDER BY d.dashboard_title`, chart.ID).Scan(&dashboards)
		dashList := make([]map[string]any, 0, len(dashboards))
		for i := range dashboards {
			dashList = append(dashList, ieDashboardJSON(&dashboards[i]))
		}
		payload["dashboards"] = dashList
	case "dashboard":
		var dashboard models.Dashboard
		if err := db.First(&dashboard, id).Error; err != nil {
			server.WriteError(w, apperrors.NotFound("Dashboard not found"))
			return
		}
		var charts []models.Slice
		db.Raw(`SELECT s.* FROM slices s JOIN dashboard_slices ds ON ds.slice_id = s.id
			WHERE ds.dashboard_id = ? ORDER BY s.slice_name`, dashboard.ID).Scan(&charts)
		chartList := make([]map[string]any, 0, len(charts))
		datasetIDs := make([]int, 0, len(charts))
		for i := range charts {
			chartList = append(chartList, ieChartJSON(&charts[i]))
			if charts[i].DatasourceType == "table" && charts[i].DatasourceID > 0 {
				datasetIDs = append(datasetIDs, charts[i].DatasourceID)
			}
		}
		datasets := make([]map[string]any, 0)
		databaseIDs := make([]int, 0)
		if len(datasetIDs) > 0 {
			var rows []models.SqlaTable
			db.Where("id IN ?", datasetIDs).Find(&rows)
			for i := range rows {
				datasets = append(datasets, ieDatasetJSON(db, &rows[i]))
				databaseIDs = append(databaseIDs, rows[i].DatabaseID)
			}
		}
		databases := make([]map[string]any, 0)
		if len(databaseIDs) > 0 {
			var rows []models.Database
			db.Where("id IN ?", databaseIDs).Find(&rows)
			for i := range rows {
				databases = append(databases, ieDatabaseJSON(&rows[i]))
			}
		}
		payload = map[string]any{
			"__version": importExportVersion, "type": "dashboard",
			"dashboard": ieDashboardJSON(&dashboard), "charts": chartList,
			"datasets": datasets, "databases": databases,
		}
	default:
		server.WriteError(w, apperrors.BadRequest("Unsupported export type %q", assetType))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("%s_%d_export.json", assetType, id)))
	server.WriteJSON(w, http.StatusOK, payload)
}

func ieDatabaseJSON(db *models.Database) map[string]any {
	return map[string]any{
		"id": db.ID, "uuid": db.UUID, "database_name": db.DatabaseName,
		"verbose_name": db.VerboseName, "sqlalchemy_uri": db.SQLAlchemyURI,
		"configuration_method": db.ConfigurationMethod,
		"expose_in_sqllab":     db.ExposeInSQLlab, "allow_run_async": db.AllowRunAsync,
		"allow_file_upload": db.AllowFileUpload, "allow_ctas": db.AllowCTAS,
		"allow_cvas": db.AllowCVAS, "allow_dml": db.AllowDML,
		"force_ctas_schema": db.ForceCTASSchema, "impersonate_user": db.ImpersonateUser,
		"extra": db.Extra, "cache_timeout": db.CacheTimeout,
	}
}

func ieDatasetJSON(db *gorm.DB, t *models.SqlaTable) map[string]any {
	var columns []models.TableColumn
	db.Where("table_id = ?", t.ID).Order("column_name").Find(&columns)
	columnList := make([]map[string]any, 0, len(columns))
	for i := range columns {
		c := &columns[i]
		columnList = append(columnList, map[string]any{
			"uuid": c.UUID, "column_name": c.ColumnName, "verbose_name": c.VerboseName,
			"type": c.Type, "is_dttm": c.IsDttm, "groupby": c.Groupby,
			"filterable": c.Filterable, "is_active": c.IsActive,
			"expression": c.Expression, "description": c.Description,
			"python_date_format": c.PythonDateFormat, "datetime_format": c.DatetimeFormat,
		})
	}
	var metrics []models.SqlMetric
	db.Where("table_id = ?", t.ID).Order("metric_name").Find(&metrics)
	metricList := make([]map[string]any, 0, len(metrics))
	for i := range metrics {
		m := &metrics[i]
		metricList = append(metricList, map[string]any{
			"uuid": m.UUID, "metric_name": m.MetricName, "verbose_name": m.VerboseName,
			"metric_type": m.MetricType, "expression": m.Expression,
			"description": m.Description, "d3format": m.D3Format,
			"currency": m.Currency, "warning_text": m.WarningText,
		})
	}
	return map[string]any{
		"id": t.ID, "uuid": t.UUID, "table_name": t.TableNameStr,
		"database_id": t.DatabaseID, "schema": t.Schema, "catalog": t.Catalog,
		"sql": t.SQL, "main_dttm_col": t.MainDttmCol, "description": t.Description,
		"default_endpoint": t.DefaultEndpoint, "is_featured": t.IsFeatured,
		"filter_select_enabled": t.FilterSelectEnabled, "cache_timeout": t.CacheTimeout,
		"template_params": jsonField([]byte(t.TemplateParams)), "extra": jsonField([]byte(t.Extra)),
		"columns": columnList, "metrics": metricList,
	}
}

func ieChartJSON(s *models.Slice) map[string]any {
	return map[string]any{
		"id": s.ID, "uuid": s.UUID, "slice_name": s.SliceName,
		"viz_type": s.VizType, "datasource_id": s.DatasourceID,
		"datasource_type": s.DatasourceType, "datasource_name": s.DatasourceName,
		"params": jsonField(s.Params), "query_context": jsonField(s.QueryContext),
		"description": s.Description, "cache_timeout": s.CacheTimeout,
	}
}

func ieDashboardJSON(d *models.Dashboard) map[string]any {
	return map[string]any{
		"id": d.ID, "uuid": d.UUID, "dashboard_title": d.DashboardTitle,
		"slug": d.Slug, "description": d.Description, "css": d.CSS,
		"position_json": jsonField(d.PositionJSON),
		"json_metadata": jsonField(d.JSONMetadata), "published": d.Published,
	}
}

// Value helpers -----------------------------------------------------------

// ieJSONBytes serialises a JSON column, defaulting to an empty object.
func ieJSONBytes(v any) []byte {
	s := jsonString(v)
	if s == "" {
		s = "{}"
	}
	return []byte(s)
}

func ieString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}

func ieInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case float32:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case string:
		var n int
		if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
			return n
		}
	}
	return 0
}

func ieIntPtr(m map[string]any, key string) *int {
	if m == nil {
		return nil
	}
	if _, ok := m[key]; !ok || m[key] == nil {
		return nil
	}
	v := ieInt(m, key)
	return &v
}

func ieBool(m map[string]any, key string, def bool) bool {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

func ieMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func ieList(v any) []map[string]any {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func ieFirst(v any) map[string]any {
	list := ieList(v)
	if len(list) == 0 {
		return nil
	}
	return list[0]
}
