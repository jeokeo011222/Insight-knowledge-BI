package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/connectors"
	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterDatabases mounts /api/v1/database/*.
func RegisterDatabases(r chi.Router, deps *Deps) {
	d := &databaseAPI{deps: deps, pools: newPoolRegistry()}

	r.Route("/database", func(dr chi.Router) {
		dr.Use(security.RequireUser)

		dr.With(security.RequirePermission(security.ActionRead, security.ResDatabase)).Get("/", d.list)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDatabase)).Post("/", d.create)
		dr.With(security.RequirePermission(security.ActionRead, security.ResDatabase)).Get("/_info", d.info)

		dr.Get("/available", d.available)
		dr.Get("/{pk}", d.get)
		dr.With(security.RequirePermission(security.ActionWrite, security.ResDatabase)).Put("/{pk}", d.update)
		dr.With(security.RequirePermission(security.ActionDelete, security.ResDatabase)).Delete("/{pk}", d.remove)
		dr.With(security.RequirePermission(security.ActionExport, security.ResDatabase)).Get("/{pk}/export/", d.export)

		dr.Post("/test_connection", d.testConnection)
		dr.Get("/{pk}/connection", d.connectionInfo)
		dr.Get("/{pk}/schemas", d.schemas)
		dr.Get("/{pk}/tables", d.tables)
		dr.Get("/{pk}/table_metadata", d.tableMetadata)
		dr.Get("/{pk}/catalog", d.catalog)
		dr.Get("/{pk}/function_names", d.functionNames)
		dr.Get("/{pk}/validate_parameters", d.validateParameters)
		dr.Post("/{pk}/select_star", d.selectStar)
		dr.Post("/{pk}/refresh", d.refresh)
	})
}

type databaseAPI struct {
	deps  *Deps
	pools *poolRegistry
}

// poolRegistry caches *sql.DB handles per database id.
type poolRegistry struct {
	mu    sync.RWMutex
	pools map[int]*cachedPool
}

type cachedPool struct {
	db      *connectorsPool
	updated time.Time
	uri     string
}

// connectorsPool is a thin alias kept for readability.
type connectorsPool = poolHandle

func newPoolRegistry() *poolRegistry {
	return &poolRegistry{pools: map[int]*cachedPool{}}
}

func (p *poolRegistry) get(id int, uri string) (*poolHandle, error) {
	p.mu.RLock()
	if c, ok := p.pools[id]; ok && c.uri == uri {
		p.mu.RUnlock()
		return c.db, nil
	}
	p.mu.RUnlock()
	h, err := openPool(uri)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if old, ok := p.pools[id]; ok {
		old.db.Close()
	}
	p.pools[id] = &cachedPool{db: h, updated: time.Now(), uri: uri}
	p.mu.Unlock()
	return h, nil
}

func (p *poolRegistry) invalidate(id int) {
	p.mu.Lock()
	if c, ok := p.pools[id]; ok {
		c.db.Close()
		delete(p.pools, id)
	}
	p.mu.Unlock()
}

// databaseJSON renders the GET/POST response body.
func (d *databaseAPI) databaseJSON(r *http.Request, db *models.Database) map[string]any {
	return map[string]any{
		"id":                                db.ID,
		"database_name":                     db.DatabaseName,
		"verbose_name":                      nullIfEmpty(db.VerboseName),
		"uuid":                              db.UUID,
		"sqlalchemy_uri":                    maskPassword(db.SQLAlchemyURI),
		"backend":                           engineOf(db.SQLAlchemyURI),
		"configuration_method":              db.ConfigurationMethod,
		"cache_timeout":                     db.CacheTimeout,
		"expose_in_sqllab":                  db.ExposeInSQLlab,
		"allow_run_async":                   db.AllowRunAsync,
		"allow_file_upload":                 db.AllowFileUpload,
		"allow_ctas":                        db.AllowCTAS,
		"allow_cvas":                        db.AllowCVAS,
		"allow_dml":                         db.AllowDML,
		"allow_multi_schema_metadata_fetch": db.AllowMultiSchemaMetadataFetch,
		"explore_database":                  db.ExploreDatabase,
		"force_ctas_schema":                 db.ForceCTASSchema,
		"impersonate_user":                  db.ImpersonateUser,
		"extra":                             db.Extra,
		"encrypted_extra":                   maskSecrets(db.EncryptedExtra),
		"encrypted_extra_configured":        strings.TrimSpace(db.EncryptedExtra) != "",
		"server_cert":                       nullIfEmpty(db.ServerCert),
		"is_managed_externally":             db.IsManagedExternally,
		"external_url":                      nullIfEmpty(db.ExternalURL),
		"last_probe_at":                     db.LastProbeAt,
		"last_probe_ok":                     db.LastProbeOK,
		"last_probe_ms":                     db.LastProbeMs,
		"last_probe_error":                  nullIfEmpty(db.LastProbeError),
		"created_on":                        db.CreatedOn,
		"changed_on":                        db.ChangedOn,
		"created_by":                        nil,
		"changed_by":                        nil,
	}
}

type databasePayload struct {
	DatabaseName        string `json:"database_name"`
	VerboseName         string `json:"verbose_name"`
	SQLAlchemyURI       string `json:"sqlalchemy_uri"`
	ConfigurationMethod string `json:"configuration_method"`
	CacheTimeout        *int   `json:"cache_timeout"`
	ExposeInSQLlab      *bool  `json:"expose_in_sqllab"`
	AllowRunAsync       *bool  `json:"allow_run_async"`
	AllowFileUpload     *bool  `json:"allow_file_upload"`
	AllowCTAS           *bool  `json:"allow_ctas"`
	AllowCVAS           *bool  `json:"allow_cvas"`
	AllowDML            *bool  `json:"allow_dml"`
	AllowMultiSchema    *bool  `json:"allow_multi_schema_metadata_fetch"`
	ExploreDatabase     *bool  `json:"explore_database"`
	IsManagedExternally *bool  `json:"is_managed_externally"`
	ExternalURL         string `json:"external_url"`
	ForceCTASSchema     string `json:"force_ctas_schema"`
	ImpersonateUser     *bool  `json:"impersonate_user"`
	Extra               any    `json:"extra"`
	ServerCert          string `json:"server_cert"`
	// Connection form fields (Superset's dynamic form).
	// DatabaseID identifies an already stored connection; the probe uses it to
	// reuse the saved password when the browser only ever had a masked URI.
	DatabaseID     *int   `json:"database_id"`
	Engine         string `json:"engine"`
	Host           string `json:"host"`
	Port           *int   `json:"port"`
	Database       string `json:"database"`
	Schema         string `json:"schema"`
	Path           string `json:"path"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Query          string `json:"query"`
	EncryptedExtra any    `json:"encrypted_extra"`
}

func (d *databaseAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := d.deps.DB.WithContext(r.Context()).Model(&models.Database{})
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("database_name ILIKE ? OR verbose_name ILIKE ?", like, like)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "database_name asc", func(db *models.Database) any { return d.databaseJSON(r, db) })
}

func (d *databaseAPI) info(w http.ResponseWriter, r *http.Request) {
	var dbs []models.Database
	if err := d.deps.DB.WithContext(r.Context()).Order("database_name").Find(&dbs).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(dbs))
	for _, db := range dbs {
		out = append(out, map[string]any{
			"id": db.ID, "text": displayName(db.DatabaseName, db.VerboseName),
			"database_name": db.DatabaseName, "name": db.DatabaseName,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"count": len(out), "result": out})
}

type engineInfo struct {
	Engine             string         `json:"engine"`
	EngineName         string         `json:"engine_name"`
	Driver             string         `json:"driver"`
	DefaultDriver      string         `json:"default_driver"`
	DefaultPort        int            `json:"default_port"`
	SupportsFileUpload bool           `json:"supports_file_upload"`
	SupportsJoins      bool           `json:"supports_joins"`
	SupportsSubqueries bool           `json:"supports_subqueries"`
	Parameters         []any          `json:"parameters"`
	EngineInformation  map[string]any `json:"engine_information"`
	// Category groups the engine in the "supported databases" picker.
	Category string `json:"category"`
	// Connection describes the form fields this engine needs. The frontend
	// renders them directly, so adding an engine never requires a UI change.
	Connection connectors.ConnectionForm `json:"connection"`
}

func (d *databaseAPI) available(w http.ResponseWriter, r *http.Request) {
	specs := connectors.Specs()
	out := make([]engineInfo, 0, len(specs))
	for _, s := range specs {
		form := connectors.Form(s.Engine())
		out = append(out, engineInfo{
			Engine: s.Engine(), EngineName: s.EngineName(), Driver: s.Driver(),
			DefaultDriver: s.Driver(), DefaultPort: s.DefaultPort(),
			SupportsFileUpload: s.SupportsFileUpload(), SupportsJoins: s.SupportsJoins(),
			SupportsSubqueries: s.SupportsSubqueries(),
			Parameters:         engineParameters(s),
			EngineInformation:  connectors.Information(s),
			Category:           connectors.Category(s.Engine()),
			Connection:         form,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out})
}

// engineParameters describes the connection form fields for an engine. The
// shape mirrors Superset's connection form parameters: every entry is a small
// JSON-schema-like object. It is derived from the same source the UI uses so
// the two cannot drift.
func engineParameters(spec connectors.EngineSpec) []any {
	form := connectors.Form(spec.Engine())
	out := make([]any, 0, len(form.Fields))
	for _, f := range form.Fields {
		entry := map[string]any{
			"name":     f.Name,
			"label":    f.Label,
			"type":     f.Type,
			"required": f.Required,
			"group":    f.Group,
		}
		if f.Default != nil {
			entry["default"] = f.Default
		}
		if f.Placeholder != "" {
			entry["placeholder"] = f.Placeholder
		}
		if f.Help != "" {
			entry["description"] = f.Help
		}
		if f.Type == connectors.TypePassword {
			entry["encrypted"] = true
		}
		out = append(out, entry)
	}
	return out
}

func (d *databaseAPI) get(w http.ResponseWriter, r *http.Request) {
	db, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": d.databaseJSON(r, db)})
}

func (d *databaseAPI) create(w http.ResponseWriter, r *http.Request) {
	var p databasePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(p.DatabaseName) == "" {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.database.name_required", "Database name is required")))
		return
	}
	uri, err := d.resolveURI(&p)
	if err != nil {
		server.WriteError(w, uriError(r, err))
		return
	}
	if d.deps.Config.PreventUnsafeDBConnections {
		if err := validateURI(r, uri); err != nil {
			server.WriteError(w, err)
			return
		}
	}
	db := models.Database{
		UUID:                          uuid.NewString(),
		DatabaseName:                  p.DatabaseName,
		VerboseName:                   orDefault(p.VerboseName, p.DatabaseName),
		SQLAlchemyURI:                 uri,
		ConfigurationMethod:           orDefault(p.ConfigurationMethod, "sqlalchemy_form"),
		CacheTimeout:                  p.CacheTimeout,
		ExposeInSQLlab:                boolOr(p.ExposeInSQLlab, true),
		AllowRunAsync:                 boolOr(p.AllowRunAsync, false),
		AllowFileUpload:               boolOr(p.AllowFileUpload, false),
		AllowCTAS:                     boolOr(p.AllowCTAS, false),
		AllowCVAS:                     boolOr(p.AllowCVAS, false),
		AllowDML:                      boolOr(p.AllowDML, false),
		AllowMultiSchemaMetadataFetch: boolOr(p.AllowMultiSchema, false),
		ExploreDatabase:               boolOr(p.ExploreDatabase, true),
		IsManagedExternally:           boolOr(p.IsManagedExternally, false),
		ExternalURL:                   p.ExternalURL,
		ForceCTASSchema:               p.ForceCTASSchema,
		ImpersonateUser:               boolOr(p.ImpersonateUser, false),
		Extra:                         jsonString(p.Extra),
		EncryptedExtra:                jsonString(p.EncryptedExtra),
		ServerCert:                    p.ServerCert,
	}
	if err := d.deps.DB.WithContext(r.Context()).Create(&db).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.database.create_failed", "Could not create database: %s", err.Error())))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": d.databaseJSON(r, &db)})
}

func (d *databaseAPI) update(w http.ResponseWriter, r *http.Request) {
	db, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p databasePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{}
	if p.DatabaseName != "" {
		updates["database_name"] = p.DatabaseName
	}
	if p.VerboseName != "" {
		updates["verbose_name"] = p.VerboseName
	}
	// Editing through the structured form means the browser never receives the
	// stored password (it only ever sees a masked URI). Keep the existing
	// secret unless the user typed a new one, otherwise saving unrelated
	// settings would silently drop the credential and break the connection.
	if p.SQLAlchemyURI == "" && p.Password == "" && p.Engine != "" {
		if parsed, perr := connectors.ParseSQLAlchemyURI(db.SQLAlchemyURI); perr == nil {
			p.Password = parsed.Password
		}
	}
	// Only rebuild the URI when the request actually carried connection
	// fields; a request that just flips a switch must not clear the stored one.
	if p.Engine != "" || p.SQLAlchemyURI != "" || p.Host != "" || p.Path != "" {
		uri, uriErr := d.resolveURI(&p)
		if uriErr != nil {
			server.WriteError(w, uriError(r, uriErr))
			return
		}
		updates["sqlalchemy_uri"] = uri
	}
	if p.CacheTimeout != nil {
		updates["cache_timeout"] = *p.CacheTimeout
	}
	for key, ptr := range map[string]*bool{
		"expose_in_sqllab": p.ExposeInSQLlab, "allow_run_async": p.AllowRunAsync,
		"allow_file_upload": p.AllowFileUpload, "allow_ctas": p.AllowCTAS,
		"allow_cvas": p.AllowCVAS, "allow_dml": p.AllowDML,
		"impersonate_user":                  p.ImpersonateUser,
		"allow_multi_schema_metadata_fetch": p.AllowMultiSchema,
		"explore_database":                  p.ExploreDatabase,
		"is_managed_externally":             p.IsManagedExternally,
	} {
		if ptr != nil {
			updates[key] = *ptr
		}
	}
	if p.ForceCTASSchema != "" {
		updates["force_ctas_schema"] = p.ForceCTASSchema
	}
	if p.ExternalURL != "" {
		updates["external_url"] = p.ExternalURL
	}
	if p.ServerCert != "" {
		updates["server_cert"] = p.ServerCert
	}
	if p.Extra != nil {
		updates["extra"] = jsonString(p.Extra)
	}
	if p.EncryptedExtra != nil {
		updates["encrypted_extra"] = jsonString(p.EncryptedExtra)
	}
	updates["changed_by_fk"] = intPtrNil(userIDOf(r))
	if err := d.deps.DB.WithContext(r.Context()).Model(db).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.database.update_failed", "Could not update database: %s", err.Error())))
		return
	}
	d.pools.invalidate(db.ID)
	_ = d.deps.DB.WithContext(r.Context()).First(db, db.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": d.databaseJSON(r, db)})
}

func (d *databaseAPI) remove(w http.ResponseWriter, r *http.Request) {
	db, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	// Soft-deleted datasets must not block removing the connection: the list
	// screen already hides them, so the user cannot get rid of the database.
	var datasetCount int64
	d.deps.DB.WithContext(r.Context()).Model(&models.SqlaTable{}).
		Where("database_id = ? AND deleted_at IS NULL", db.ID).Count(&datasetCount)
	if datasetCount > 0 {
		server.WriteError(w, apperrors.BadRequest("%s",
			localize(r, "error.database.in_use", "There are %d datasets associated with this database", datasetCount)))
		return
	}
	if err := d.deps.DB.WithContext(r.Context()).Delete(db).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	d.pools.invalidate(db.ID)
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

func (d *databaseAPI) export(w http.ResponseWriter, r *http.Request) {
	db, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var datasets []models.SqlaTable
	d.deps.DB.WithContext(r.Context()).Where("database_id = ?", db.ID).Find(&datasets)
	payload := map[string]any{
		"__version": "1.0.0",
		"type":      "database",
		"database":  d.databaseJSON(r, db),
		"datasets":  datasets,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_export.json", db.DatabaseName))
	server.WriteJSON(w, http.StatusOK, payload)
}

func (d *databaseAPI) testConnection(w http.ResponseWriter, r *http.Request) {
	var p databasePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	// Probing an existing connection must not require retyping the password:
	// the browser only receives a masked URI, so the stored secret is restored
	// here before the URI is assembled.
	if p.DatabaseID != nil && p.Password == "" && p.SQLAlchemyURI == "" {
		var stored models.Database
		if err := d.deps.DB.WithContext(r.Context()).First(&stored, *p.DatabaseID).Error; err == nil {
			if parsed, perr := connectors.ParseSQLAlchemyURI(stored.SQLAlchemyURI); perr == nil {
				p.Password = parsed.Password
			}
		}
	}
	uri, uriErr := d.resolveURI(&p)
	if uriErr != nil {
		server.WriteError(w, uriError(r, uriErr))
		return
	}
	start := time.Now()
	if err := validateURI(r, uri); err != nil {
		server.WriteError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := connectors.Ping(ctx, uri); err != nil {
		if errors.Is(err, connectors.ErrDBFileNotFound) {
			server.WriteJSON(w, http.StatusOK, map[string]any{
				"message": localize(r, "error.database.file_not_found",
					"The database file does not exist: %s", firstNonEmpty(p.Path, p.Database)),
				"result": nil,
				"errors": []map[string]any{{"message": err.Error()}},
			})
			return
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"message": "Connection failed: " + err.Error(),
			"result":  nil,
			"errors":  []map[string]any{{"message": err.Error()}},
		})
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"message": "Seems OK!",
		"result":  map[string]any{"latency_ms": time.Since(start).Milliseconds()},
	})
}

func (d *databaseAPI) connectionInfo(w http.ResponseWriter, r *http.Request) {
	db, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	parsed, perr := connectors.ParseSQLAlchemyURI(db.SQLAlchemyURI)
	if perr != nil {
		server.WriteError(w, apperrors.BadRequest("%s", perr.Error()))
		return
	}
	out := map[string]any{
		"engine": parsed.Engine, "driver": parsed.Driver,
		"host": parsed.Host, "port": parsed.Port,
		"database": parsed.Database, "username": parsed.Username,
		"query": parsed.Query.Encode(),
		"kind":  string(connectors.Kind(parsed.Engine)),
	}
	// Catalogs live in the path as "catalog/schema"; file engines in the path
	// alone. Split them out so the structured form repopulates field by field
	// instead of showing the raw path in the "database" input.
	switch connectors.Kind(parsed.Engine) {
	case connectors.KindCatalog:
		parts := strings.SplitN(parsed.Database, "/", 2)
		out["database"] = parts[0]
		if len(parts) > 1 {
			out["schema"] = parts[1]
		}
	case connectors.KindFile:
		// A path that still carries a scheme is an older connection saved with
		// the prefix pasted in; show the form the bare path it expects.
		path := parsed.Database
		if strings.Contains(path, "://") {
			path = connectors.ParseFileURI(db.SQLAlchemyURI)
		}
		out["path"] = path
		out["database"] = ""
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out})
}

func (d *databaseAPI) pool(r *http.Request, id int) (*poolHandle, error) {
	var db models.Database
	if err := d.deps.DB.WithContext(r.Context()).First(&db, id).Error; err != nil {
		return nil, apperrors.NotFound("%s", localize(r, "error.database.not_found", "Database not found"))
	}
	return d.pools.get(db.ID, db.SQLAlchemyURI)
}

func (d *databaseAPI) schemas(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	h, err := d.pool(r, id)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	rows, err := h.Query(r.Context(), h.spec.SchemasQuery())
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.database.load_schemas_failed", "Could not load schemas: %s", err.Error())))
		return
	}
	out := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		name := rowText(row, "schema_name", "schema")
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	// The connection's own database is the natural default (MySQL exposes
	// databases as schemas; PostgreSQL keeps "public"). Returning it lets the
	// dataset wizard preselect the right one instead of the first alphabetically
	// — which on a MySQL server is usually somebody else's schema.
	defaultSchema := ""
	if parsed, perr := connectors.ParseSQLAlchemyURI(h.uri); perr == nil {
		defaultSchema = h.spec.DefaultSchema(parsed.Database)
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": out, "count": len(out), "default_schema": defaultSchema,
	})
}

func (d *databaseAPI) tables(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	h, err := d.pool(r, id)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	schema := server.QueryStr(r, "schema_name")
	if schema == "" {
		dbName := ""
		if parsed, perr := connectors.ParseSQLAlchemyURI(h.uri); perr == nil {
			dbName = parsed.Database
		}
		schema = h.spec.DefaultSchema(dbName)
	}
	rows, err := h.Query(r.Context(), fmt.Sprintf(h.spec.TablesQuery(), escapeSQL(schema)))
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.database.load_tables_failed", "Could not load tables: %s", err.Error())))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		name := rowText(row, "table_name", "tablename", "name")
		if name == "" {
			continue
		}
		tableType := rowText(row, "table_type", "type")
		out = append(out, map[string]any{
			"value": name, "label": name, "type": tableType,
			"extra": map[string]any{"schema": schema},
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out, "count": len(out)})
}

func (d *databaseAPI) tableMetadata(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	h, err := d.pool(r, id)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	schema := server.QueryStr(r, "schema_name")
	table := server.QueryStr(r, "table_name")
	if table == "" {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.table_name_required", "table_name is required")))
		return
	}
	q := fmt.Sprintf(h.spec.ColumnsQuery(), escapeSQL(schema), escapeSQL(table))
	rows, err := h.Query(r.Context(), q)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.database.load_columns_failed", "Could not load columns: %s", err.Error())))
		return
	}
	cols := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		name := rowText(row, "column_name", "column", "name")
		if name == "" {
			continue
		}
		dtype := strings.ToLower(rowText(row, "data_type", "type", "column_type"))
		cols = append(cols, map[string]any{
			"name": name, "type": dtype, "is_dttm": isTemporalType(dtype),
			"is_date":  strings.HasPrefix(dtype, "date"),
			"nullable": strings.EqualFold(rowText(row, "is_nullable"), "yes"),
			"default":  rowValue(row, "column_default", "default"),
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"name": table, "schema": schema, "columns": cols, "indexes": []any{},
		"select_star": h.spec.SelectStar(qualified(spec(h), schema, table)),
	})
}

func spec(h *poolHandle) connectors.EngineSpec { return h.spec }

func (d *databaseAPI) catalog(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	h, err := d.pool(r, id)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	schemaRows, err := h.Query(r.Context(), h.spec.SchemasQuery())
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	var tree []map[string]any
	for _, sr := range schemaRows {
		schema := rowText(sr, "schema_name", "schema")
		if schema == "" {
			continue
		}
		tableRows, tableErr := h.Query(r.Context(), fmt.Sprintf(h.spec.TablesQuery(), escapeSQL(schema)))
		tables := make([]map[string]any, 0, len(tableRows))
		for _, tr := range tableRows {
			name := rowText(tr, "table_name", "tablename", "name")
			if name == "" {
				continue
			}
			tables = append(tables, map[string]any{
				"name": name, "type": "table",
				"extra": map[string]any{"schema": schema},
			})
		}
		node := map[string]any{
			"name": schema, "type": "schema", "children": tables,
		}
		if tableErr != nil {
			// One unreadable schema must not fail the whole tree, but silently
			// returning an empty list makes a broken query look like "0 tables".
			// The UI reads this field and says so instead.
			node["tables_error"] = tableErr.Error()
		}
		tree = append(tree, node)
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": tree})
}

func (d *databaseAPI) functionNames(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": []string{}})
}

func (d *databaseAPI) validateParameters(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": []any{}})
}

func (d *databaseAPI) selectStar(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var body struct {
		Schema string `json:"schema"`
		Table  string `json:"table"`
	}
	_ = security.DecodeBody(r, &body)
	h, err := d.pool(r, id)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	sqlText := h.spec.SelectStar(qualified(h.spec, body.Schema, body.Table)) + " " + h.spec.LimitClause(100)
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"query": sqlText}})
}

func (d *databaseAPI) refresh(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	d.pools.invalidate(id)
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Helpers -----------------------------------------------------------------

func (d *databaseAPI) find(r *http.Request) (*models.Database, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var db models.Database
	if err := d.deps.DB.WithContext(r.Context()).First(&db, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("%s", localize(r, "error.database.not_found", "Database not found"))
		}
		return nil, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error()))
	}
	return &db, nil
}

// resolveURI builds a SQLAlchemy URI from the either/or payload.
// resolveURI prefers an explicit sqlalchemy_uri and otherwise assembles one from
// the structured connection form. Assembly lives in the connectors package so
// the escaping rules are unit-tested and shared with the engine catalogue.
func (d *databaseAPI) resolveURI(p *databasePayload) (string, error) {
	if uri := strings.TrimSpace(p.SQLAlchemyURI); uri != "" {
		// File engines have one input ("database file"), so a pasted
		// "duckdb:///D:/data/stock.duckdb" arrives as a whole URI and used to be
		// stored with the scheme doubled: duckdb:///duckdb:///D:/data/...
		return connectors.CleanFileURI(uri), nil
	}
	if strings.TrimSpace(p.Engine) == "" {
		return "", connectors.ErrEngineRequired
	}
	return connectors.BuildURI(connectors.BuildOptions{
		Engine:   p.Engine,
		Host:     p.Host,
		Port:     derefInt(p.Port, 0),
		Database: p.Database,
		Schema:   p.Schema,
		Username: p.Username,
		Password: p.Password,
		Query:    p.Query,
		Path:     firstNonEmpty(p.Path, p.Database),
	})
}

// uriError turns an assembly failure into a message that names the field the
// user still has to fill in. A file-backed engine missing its path used to
// report "SQLAlchemy URI is required", which pointed at a field the form does
// not even show.
func uriError(r *http.Request, err error) error {
	switch {
	case errors.Is(err, connectors.ErrEngineRequired):
		return apperrors.BadRequest("%s", localize(r, "error.database.engine_required", "Choose a database engine first"))
	case errors.Is(err, connectors.ErrPathRequired):
		return apperrors.BadRequest("%s", localize(r, "error.database.file_path_required",
			"Database file path is required, for example /data/app.duckdb"))
	case errors.Is(err, connectors.ErrHostRequired):
		return apperrors.BadRequest("%s", localize(r, "error.database.host_required", "Host is required"))
	case errors.Is(err, connectors.ErrURIRequired):
		return apperrors.BadRequest("%s", localize(r, "error.database.uri_required", "SQLAlchemy URI is required"))
	default:
		return apperrors.BadRequest("%s", err.Error())
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func validateURI(r *http.Request, uri string) error {
	parsed, err := connectors.ParseSQLAlchemyURI(uri)
	if err != nil {
		return apperrors.BadRequest("%s", err.Error())
	}
	if connectors.Get(parsed.Engine) == nil {
		return apperrors.BadRequest("%s", localize(r, "error.database.unknown_engine", "Unknown database engine %q", parsed.Engine))
	}
	return nil
}

func maskPassword(uri string) string {
	at := strings.LastIndex(uri, "@")
	if at < 0 {
		return uri
	}
	schemeEnd := strings.Index(uri, "://")
	if schemeEnd < 0 || schemeEnd > at {
		return uri
	}
	creds := uri[schemeEnd+3 : at]
	colon := strings.Index(creds, ":")
	if colon < 0 {
		return uri
	}
	return uri[:schemeEnd+3] + creds[:colon] + ":XXXXXXXXXX" + uri[at:]
}

func engineOf(uri string) string {
	parsed, err := connectors.ParseSQLAlchemyURI(uri)
	if err != nil {
		return ""
	}
	return parsed.Engine
}

func displayName(name, verbose string) string {
	if verbose != "" && verbose != name {
		return verbose + " (" + name + ")"
	}
	return name
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

func jsonString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func escapeSQL(s string) string { return strings.ReplaceAll(s, "'", "''") }

// maskSecrets returns the key structure of an encrypted extra payload with
// every value replaced. The UI only needs to know *which* secrets are already
// stored; shipping the ciphertext (or the plaintext) back to the browser would
// turn a read permission into a credential leak.
func maskSecrets(raw string) any {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return map[string]any{}
	}
	masked := make(map[string]any, len(parsed))
	for key := range parsed {
		masked[key] = "********"
	}
	return masked
}

func qualified(spec connectors.EngineSpec, schema, table string) string {
	if schema == "" {
		return table
	}
	return schema + "." + table
}

func isTemporalType(dtype string) bool {
	dtype = strings.ToLower(dtype)
	for _, t := range []string{"timestamp", "datetime", "date", "time"} {
		if strings.Contains(dtype, t) {
			return true
		}
	}
	return false
}

var _ = zap.String
