package queries

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/config"
	"superset/internal/connectors"
	"superset/internal/models"
	"superset/internal/pkg/cache"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
)

// QueryCachePrefix namespaces query result cache keys.
const QueryCachePrefix = "superset:chart_data:"

// ColumnMeta describes one result column.
type ColumnMeta struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryResultItem is the per query-object payload returned to the client.
type QueryResultItem struct {
	Data           []map[string]any `json:"data"`
	ColNames       []string         `json:"colnames"`
	ColTypes       []string         `json:"coltypes"`
	SQL            string           `json:"sql"`
	RowCount       int              `json:"rowcount"`
	CacheKey       string           `json:"cache_key"`
	Status         string           `json:"status"`
	Cached         bool             `json:"-"`
	AppliedFilters []map[string]any `json:"applied_filters"`
}

// QueryResult aggregates every query object of a query context.
type QueryResult struct {
	Columns    []ColumnMeta
	Data       []map[string]any
	RowCount   int
	SQL        string
	Cached     bool
	QueryID    int
	Status     string
	StartDttm  time.Time
	EndDttm    time.Time
	DurationMs int
	Results    []QueryResultItem
}

// Executor runs query contexts against the datasets' source databases.
type Executor struct {
	DB    *gorm.DB
	Log   *zap.Logger
	Cache *cache.Client
	Mgr   *security.Manager

	// DefaultRowLimit is applied when a query object does not set row_limit.
	DefaultRowLimit int
	// MaxRow caps every row_limit (superset config SQL_MAX_ROW).
	MaxRow int
	// CacheTTL is the fallback cache TTL when neither the request nor the
	// dataset defines one.
	CacheTTL time.Duration
	// QueryTimeout bounds a single statement. Zero falls back to
	// defaultQueryTimeout; callers wire it from the deployment config so slow
	// warehouses can be given more headroom than interactive chart queries.
	QueryTimeout time.Duration
	// ExplainMaxRows enables the EXPLAIN cost gate on the Agent's read-only
	// path: a plan whose sequential scan is estimated above this many rows is
	// rejected before it runs. Zero disables the gate.
	ExplainMaxRows int
	// Timezone is used for time grain expressions.
	Timezone *time.Location
}

// defaultQueryTimeout is the statement budget when nothing is configured
// (matches the historical hard-coded 120s, now overridable).
const defaultQueryTimeout = 120

// queryTimeout resolves the per-statement deadline.
func (e *Executor) queryTimeout() time.Duration {
	if e.QueryTimeout > 0 {
		return e.QueryTimeout
	}
	return defaultQueryTimeout * time.Second
}

// NewExecutor builds an executor with superset's default limits.
func NewExecutor(db *gorm.DB, log *zap.Logger, c *cache.Client, mgr *security.Manager) *Executor {
	return &Executor{
		DB:              db,
		Log:             log,
		Cache:           c,
		Mgr:             mgr,
		DefaultRowLimit: DefaultRowLimit,
		MaxRow:          100000,
		CacheTTL:        5 * time.Minute,
		Timezone:        time.UTC,
	}
}

// NewFromConfig builds an executor that honours the configured limits.
//
// Every caller used to re-derive these limits after NewExecutor, and only the
// chart-data handler actually did: SQL Lab, reports and the Agent each kept the
// hard-coded defaults, so SQLLAB_TIMEOUT silently did not apply where users are
// most likely to run a slow query. One constructor, one set of limits.
func NewFromConfig(db *gorm.DB, log *zap.Logger, c *cache.Client, mgr *security.Manager, cfg *config.Config) *Executor {
	e := NewExecutor(db, log, c, mgr)
	if cfg == nil {
		return e
	}
	if cfg.RowLimit > 0 {
		e.DefaultRowLimit = cfg.RowLimit
	}
	if cfg.SQLMaxRow > 0 {
		e.MaxRow = cfg.SQLMaxRow
	}
	if cfg.CacheDefaultTimeout > 0 {
		e.CacheTTL = time.Duration(cfg.CacheDefaultTimeout) * time.Second
	}
	if cfg.SqllabTimeout > 0 {
		e.QueryTimeout = time.Duration(cfg.SqllabTimeout) * time.Second
	}
	return e
}

// ExecuteQueryContext validates the request, builds and runs every SQL query
// and returns the superset chart-data payload.
func (e *Executor) ExecuteQueryContext(ctx context.Context, userID int, qc *QueryContext) (*QueryResult, error) {
	if qc == nil {
		return nil, apperrors.BadRequest("Query context is required")
	}
	if len(qc.Queries) == 0 {
		return nil, apperrors.BadRequest("Query context has no queries")
	}
	if !strings.EqualFold(qc.ResultFormat, "json") {
		return nil, apperrors.BadRequest("Only json result_format is supported")
	}

	ds, database, err := e.loadDataset(ctx, qc)
	if err != nil {
		return nil, err
	}
	if err := e.authorize(ctx, userID, ds, database); err != nil {
		return nil, err
	}

	spec, err := engineSpec(database.SQLAlchemyURI)
	if err != nil {
		return nil, err
	}

	result := &QueryResult{Status: "success", StartDttm: time.Now().UTC()}
	var pool *sql.DB
	defer func() {
		if pool != nil {
			_ = pool.Close()
		}
	}()

	rls := e.rlsProvider(ctx, userID)
	tz := "UTC"
	if e.Timezone != nil {
		tz = e.Timezone.String()
	}

	for i := range qc.Queries {
		q := &qc.Queries[i]
		built, err := BuildSQL(ds, q, spec, BuildOptions{
			Timezone:        tz,
			DefaultRowLimit: e.rowLimit(),
			MaxRow:          e.maxRow(),
			RLSProvider:     rls,
		})
		if err != nil {
			return nil, err
		}

		item := QueryResultItem{
			SQL:            built.SQL,
			Status:         "success",
			AppliedFilters: built.AppliedFilters,
		}
		if item.AppliedFilters == nil {
			item.AppliedFilters = []map[string]any{}
		}

		start := time.Now()
		ttl := e.cacheTTL(qc, ds)
		key := CacheKey(qc.FormData, built.SQL)

		if ttl > 0 && e.Cache != nil && !qc.Force {
			var cached cachedQueryResult
			if hit, cerr := e.Cache.Get(ctx, key, &cached); cerr == nil && hit {
				item.Data = cached.Data
				item.ColNames = cached.ColNames
				item.ColTypes = cached.ColTypes
				item.RowCount = cached.RowCount
				item.CacheKey = key
				item.Cached = true
				result.Cached = true
			} else if cerr != nil && e.Log != nil {
				e.Log.Warn("query cache read failed", zap.Error(cerr))
			}
		}
		if !item.Cached {
			if pool == nil {
				pool, err = openPool(database.SQLAlchemyURI)
				if err != nil {
					e.logQueryError(database.ID, "connect", err)
					return nil, apperrors.ServiceUnavailable("The data source is unavailable")
				}
			}
			raw, err := executeSQL(ctx, pool, built.SQL, e.queryTimeout())
			if err != nil {
				e.recordQuery(ctx, userID, database.ID, built.SQL, 0, start, time.Now(), "failed", err.Error())
				e.logQueryError(database.ID, "execute", err)
				return nil, classifyQueryError(err, e.queryTimeout())
			}
			item.Data = raw.Data
			item.ColNames = raw.ColNames
			item.ColTypes = raw.ColTypes
			item.RowCount = raw.RowCount
			item.CacheKey = key

			if ttl > 0 && e.Cache != nil {
				if cerr := e.Cache.Set(ctx, key, cachedQueryResult{
					Data: item.Data, ColNames: item.ColNames,
					ColTypes: item.ColTypes, RowCount: item.RowCount,
				}, ttl); cerr != nil && e.Log != nil {
					e.Log.Warn("query cache write failed", zap.Error(cerr))
				}
			}
		}

		if len(q.PostProcessing) > 0 {
			processed, perr := ApplyPostProcessing(item.Data, q.PostProcessing)
			if perr != nil {
				return nil, apperrors.Newf(apperrors.InvalidPostProcessingError, 400, "%s", perr.Error())
			}
			item.Data = processed
			item.RowCount = len(processed)
			if names := orderedColumns(item.ColNames, processed); len(names) > 0 {
				item.ColNames = names
				item.ColTypes = alignTypes(names, built.Projections, processed)
			}
		}
		if item.RowCount == 0 {
			item.RowCount = len(item.Data)
		}

		end := time.Now()
		id, rerr := e.recordQuery(ctx, userID, database.ID, built.SQL, item.RowCount, start, end, "success", "")
		if rerr != nil && e.Log != nil {
			e.Log.Warn("query audit write failed", zap.Error(rerr))
		}
		if result.QueryID == 0 {
			result.QueryID = id
		}
		result.Results = append(result.Results, item)
		result.SQL = joinSQL(result.SQL, built.SQL)
	}

	if len(result.Results) > 0 {
		first := result.Results[0]
		result.Data = first.Data
		result.RowCount = first.RowCount
		for i, name := range first.ColNames {
			typ := "STRING"
			if i < len(first.ColTypes) {
				typ = first.ColTypes[i]
			}
			result.Columns = append(result.Columns, ColumnMeta{Name: name, Type: typ})
		}
	}
	result.EndDttm = time.Now().UTC()
	result.DurationMs = int(result.EndDttm.Sub(result.StartDttm).Milliseconds())
	return result, nil
}

// Response renders the chart-data wire format:
//
//	{"result":[{...}],"query_context":{...},"errors":[]}
func (r *QueryResult) Response(qc *QueryContext) map[string]any {
	results := make([]map[string]any, 0, len(r.Results))
	for _, item := range r.Results {
		results = append(results, map[string]any{
			"data":             item.Data,
			"colnames":         item.ColNames,
			"coltypes":         item.ColTypes,
			"sql":              item.SQL,
			"query":            item.SQL,
			"rowcount":         item.RowCount,
			"cache_key":        item.CacheKey,
			"status":           item.Status,
			"applied_filters":  item.AppliedFilters,
			"rejected_filters": []any{},
			"result_format":    "json",
		})
	}
	out := map[string]any{
		"result": results,
		"errors": []any{},
	}
	if qc != nil {
		out["query_context"] = qc.Echo()
	}
	return out
}

// CacheKey derives the deterministic cache key from form_data + SQL.
func CacheKey(formData map[string]any, sqlText string) string {
	if formData == nil {
		formData = map[string]any{}
	}
	raw, err := json.Marshal(formData)
	if err != nil {
		raw = []byte("{}")
	}
	h := sha256.New()
	_, _ = h.Write(raw)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(sqlText))
	return QueryCachePrefix + hex.EncodeToString(h.Sum(nil))
}

// cachedQueryResult is the JSON stored in the cache.
type cachedQueryResult struct {
	Data     []map[string]any `json:"data"`
	ColNames []string         `json:"colnames"`
	ColTypes []string         `json:"coltypes"`
	RowCount int              `json:"rowcount"`
}

// loadDataset resolves the datasource referenced by the query context.
func (e *Executor) loadDataset(ctx context.Context, qc *QueryContext) (*DatasetInfo, *models.Database, error) {
	id, typ := qc.DatasourceID()
	if id == 0 {
		return nil, nil, apperrors.BadRequest("Datasource is required")
	}
	if typ != "" && !strings.EqualFold(typ, "table") {
		return nil, nil, apperrors.BadRequest("Unsupported datasource type %q", typ)
	}

	var table models.SqlaTable
	q := e.DB.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", id)
	if err := q.First(&table).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, apperrors.NotFound("Dataset not found")
		}
		return nil, nil, apperrors.Internal("Could not load dataset: %s", err.Error())
	}

	var columns []models.TableColumn
	if err := e.DB.WithContext(ctx).Where("table_id = ?", table.ID).
		Order("column_name").Find(&columns).Error; err != nil {
		return nil, nil, apperrors.Internal("Could not load columns: %s", err.Error())
	}
	var metrics []models.SqlMetric
	if err := e.DB.WithContext(ctx).Where("table_id = ?", table.ID).
		Order("metric_name").Find(&metrics).Error; err != nil {
		return nil, nil, apperrors.Internal("Could not load metrics: %s", err.Error())
	}

	var database models.Database
	if err := e.DB.WithContext(ctx).First(&database, table.DatabaseID).Error; err != nil {
		return nil, nil, apperrors.NotFound("Database not found")
	}
	return &DatasetInfo{Table: &table, Columns: columns, Metrics: metrics}, &database, nil
}

// authorize enforces datasource level access for the caller.
func (e *Executor) authorize(ctx context.Context, userID int, ds *DatasetInfo, database *models.Database) error {
	if e.Mgr == nil {
		return nil
	}
	// Guest tokens (embedded dashboards and public share links) are already
	// scoped by the server that minted them, and a guest holds no database
	// roles to resolve datasource grants against. The permission check on the
	// endpoint (can_query_form_data on Chart) still applies.
	if u := security.UserFrom(ctx); u != nil && u.IsGuest {
		return nil
	}
	if userID <= 0 {
		return apperrors.Unauthorized("Unauthorized")
	}
	if !e.Mgr.CanAccessDatasource(ctx, userID, database.DatabaseName, ds.Table.Schema, ds.Table.TableNameStr, ds.Table.ID) {
		return apperrors.DatasourceSecurityAccess(
			fmt.Errorf("You don't have access to the datasource %s", ds.Table.TableNameStr))
	}
	return nil
}

// rlsProvider loads row level security clauses for the caller.
func (e *Executor) rlsProvider(ctx context.Context, userID int) func(int) []string {
	if e.DB == nil || userID <= 0 {
		return nil
	}
	return func(datasetID int) []string {
		var rows []struct {
			Clause string
		}
		err := e.DB.WithContext(ctx).Raw(`
			SELECT DISTINCT rls.clause AS clause
			FROM row_level_security_filters rls
			JOIN rls_filter_tables rt ON rt.rls_filter_id = rls.id
			WHERE rt.table_id = ?
			  AND (
			    EXISTS (
			      SELECT 1 FROM rls_filter_roles rr
			      JOIN ab_user_role ur ON ur.role_id = rr.role_id
			      WHERE rr.rls_filter_id = rls.id AND ur.user_id = ?
			    )
			    OR EXISTS (
			      SELECT 1 FROM rls_filter_subjects rs
			      JOIN ab_user_group ug ON ug.group_id = rs.subject_id
			      WHERE rs.rls_filter_id = rls.id AND ug.user_id = ?
			    )
			  )`, datasetID, userID, userID).Scan(&rows).Error
		if err != nil {
			if e.Log != nil {
				e.Log.Warn("rls lookup failed", zap.Error(err), zap.Int("dataset_id", datasetID))
			}
			return nil
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			if clause := strings.TrimSpace(r.Clause); clause != "" {
				out = append(out, clause)
			}
		}
		return out
	}
}

// recordQuery writes the audit row into the query table.
func (e *Executor) recordQuery(ctx context.Context, userID, databaseID int, sqlText string, rows int, start, end time.Time, status, errMsg string) (int, error) {
	if e.DB == nil {
		return 0, nil
	}
	startF := float64(start.UnixNano()) / 1e9
	endF := float64(end.UnixNano()) / 1e9
	rec := models.Query{
		ClientID:     uuid.NewString(),
		DatabaseID:   databaseID,
		Status:       status,
		SQL:          sqlText,
		ExecutedSQL:  sqlText,
		Rows:         &rows,
		ErrorMessage: errMsg,
		StartTime:    &startF,
		EndTime:      &endF,
	}
	if userID > 0 {
		uid := userID
		rec.UserID = &uid
	}
	if err := e.DB.WithContext(ctx).Create(&rec).Error; err != nil {
		return 0, err
	}
	return rec.ID, nil
}

func (e *Executor) rowLimit() int {
	if e.DefaultRowLimit > 0 {
		return e.DefaultRowLimit
	}
	return DefaultRowLimit
}

func (e *Executor) maxRow() int {
	if e.MaxRow > 0 {
		return e.MaxRow
	}
	return 100000
}

// cacheTTL resolves the TTL: request override > dataset > executor default.
func (e *Executor) cacheTTL(qc *QueryContext, ds *DatasetInfo) time.Duration {
	if qc.CustomCacheTimeout != nil {
		if *qc.CustomCacheTimeout <= 0 {
			return 0
		}
		return time.Duration(*qc.CustomCacheTimeout) * time.Second
	}
	if ds != nil && ds.Table != nil && ds.Table.CacheTimeout != nil {
		if *ds.Table.CacheTimeout <= 0 {
			return 0
		}
		return time.Duration(*ds.Table.CacheTimeout) * time.Second
	}
	return e.CacheTTL
}

// engineSpec resolves the engine spec from a SQLAlchemy URI.
func engineSpec(uri string) (connectors.EngineSpec, error) {
	parsed, err := connectors.ParseSQLAlchemyURI(uri)
	if err != nil {
		return nil, apperrors.BadRequest("%s", err.Error())
	}
	spec := connectors.Get(parsed.Engine)
	if spec == nil {
		return nil, apperrors.BadRequest("Unsupported database engine %q", parsed.Engine)
	}
	return spec, nil
}

// logQueryError keeps the raw driver error in the operator log while the
// client only ever sees the classified message.
func (e *Executor) logQueryError(databaseID int, stage string, err error) {
	if e == nil || e.Log == nil || err == nil {
		return
	}
	e.Log.Error("query failed",
		zap.String("stage", stage), zap.Int("database_id", databaseID), zap.Error(err))
}

// openPool materialises a connection pool for the database. All SQL goes
// through the pool built from the dataset's SQLAlchemy URI.
func openPool(uri string) (*sql.DB, error) {
	db, _, err := connectors.Open(uri)
	if err != nil {
		return nil, err
	}
	return db, nil
}

// executeSQL runs a statement and returns ordered rows plus column metadata.
// The querier is either the pool or a read-only session pinned to one
// connection (see readonly_session.go).
func executeSQL(ctx context.Context, db sqlQuerier, statement string, timeout time.Duration) (*cachedQueryResult, error) {
	if timeout <= 0 {
		timeout = defaultQueryTimeout * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	data := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalizeValue(values[i])
		}
		data = append(data, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	types := make([]string, len(cols))
	for i, c := range cols {
		types[i] = inferColumnType(data, c)
	}
	return &cachedQueryResult{Data: data, ColNames: cols, ColTypes: types, RowCount: len(data)}, nil
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return t
	}
}

func inferColumnType(data []map[string]any, col string) string {
	for _, row := range data {
		switch v := row[col].(type) {
		case nil:
			continue
		case bool:
			return "BOOLEAN"
		case int, int32, int64, uint, uint32, uint64:
			return "INT"
		case float32, float64:
			return "FLOAT"
		case time.Time:
			return "DATETIME"
		case string:
			if _, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return "DATETIME"
			}
			return "STRING"
		}
	}
	return "STRING"
}

// alignTypes maps the result column names onto the projected types.
func alignTypes(names []string, projections []Projection, data []map[string]any) []string {
	out := make([]string, len(names))
	for i, name := range names {
		found := false
		for _, p := range projections {
			if strings.EqualFold(p.Label, name) {
				out[i] = p.Type
				found = true
				break
			}
		}
		if !found {
			out[i] = inferColumnType(data, name)
		}
	}
	return out
}

// orderedColumns keeps the original column order and appends new columns
// (introduced by post-processing) in sorted order.
func orderedColumns(previous []string, data []map[string]any) []string {
	present := map[string]bool{}
	for _, row := range data {
		for k := range row {
			present[k] = true
		}
	}
	out := make([]string, 0, len(present))
	seen := map[string]bool{}
	for _, name := range previous {
		if present[name] && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	extra := make([]string, 0)
	for name := range present {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sortStrings(extra)
	return append(out, extra...)
}

func joinSQL(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + ";\n" + next
}
