package api

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/queries"
	"superset/internal/security"
	"superset/internal/server"
	"superset/internal/workers"
)

// RegisterSqlLab mounts /api/v1/sqllab/* mirroring superset/sqllab/api.py.
func RegisterSqlLab(r chi.Router, deps *Deps) {
	s := &sqlLabAPI{deps: deps}

	r.Route("/sqllab", func(sr chi.Router) {
		sr.Use(security.RequireUser)

		sr.With(security.RequirePermission(security.ActionWrite, security.ResQuery)).Post("/", s.execute)
		sr.With(security.RequirePermission(security.ActionRead, security.ResQuery)).Get("/views/", s.views)
		sr.With(security.RequirePermission(security.ActionRead, security.ResQuery)).Get("/history/", s.history)
		sr.Post("/estimate/", s.estimate)
		sr.Post("/format/", s.format)
		sr.With(security.RequirePermission(security.ActionSqllabExport, security.ResSqllab)).Post("/export/", s.export)

		sr.Route("/saved_query", func(qr chi.Router) {
			qr.With(security.RequirePermission(security.ActionRead, security.ResSavedQuery)).Get("/", s.listSavedQueries)
			qr.With(security.RequirePermission(security.ActionWrite, security.ResSavedQuery)).Post("/", s.createSavedQuery)
			qr.With(security.RequirePermission(security.ActionRead, security.ResSavedQuery)).Get("/{pk}", s.getSavedQuery)
			qr.With(security.RequirePermission(security.ActionWrite, security.ResSavedQuery)).Put("/{pk}", s.updateSavedQuery)
			qr.With(security.RequirePermission(security.ActionWrite, security.ResSavedQuery)).Delete("/{pk}", s.deleteSavedQuery)
		})

		sr.Route("/tab_state", func(tr chi.Router) {
			tr.With(security.RequirePermission(security.ActionRead, security.ResTabState)).Get("/", s.listTabStates)
			tr.With(security.RequirePermission(security.ActionWrite, security.ResTabState)).Post("/", s.createTabState)
			tr.With(security.RequirePermission(security.ActionWrite, security.ResTabState)).Put("/{pk}", s.updateTabState)
			tr.With(security.RequirePermission(security.ActionWrite, security.ResTabState)).Delete("/{pk}", s.deleteTabState)
		})

		// Both spellings are accepted: the SPA and Superset's own docs use a
		// trailing slash, the chi pattern above does not.
		sr.With(security.RequirePermission(security.ActionRead, security.ResQuery)).Get("/{pk}/results", s.results)
		sr.With(security.RequirePermission(security.ActionRead, security.ResQuery)).Get("/{pk}/results/", s.results)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResQuery)).Post("/{pk}/cancel", s.cancel)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResQuery)).Post("/{pk}/cancel/", s.cancel)
		sr.With(security.RequirePermission(security.ActionRead, security.ResQuery)).Get("/{pk}", s.get)
	})
}

type sqlLabAPI struct{ deps *Deps }

// sqlLabQueryTimeout is the fallback bound for synchronous statement execution,
// used when SQLLAB_TIMEOUT is unset. SQL Lab is exactly where a user runs
// arbitrary (and accidentally expensive) SQL, so the bound has to be
// configurable rather than a constant.
const sqlLabQueryTimeout = 90 * time.Second

// queryTimeout resolves the configured statement bound.
func (s *sqlLabAPI) queryTimeout() time.Duration {
	if s.deps.Config != nil && s.deps.Config.SqllabTimeout > 0 {
		return time.Duration(s.deps.Config.SqllabTimeout) * time.Second
	}
	return sqlLabQueryTimeout
}

type sqlLabPayload struct {
	DatabaseID   int    `json:"database_id"`
	Schema       string `json:"schema"`
	Catalog      string `json:"catalog"`
	SQL          string `json:"sql"`
	QueryID      int    `json:"query_id"`
	JSON         any    `json:"json"`
	RunAsync     bool   `json:"run_async"`
	RunAsyncAlt  bool   `json:"runAsync"`
	SelectAsCTA  bool   `json:"select_as_cta"`
	SelectAsCTAA bool   `json:"select_as_ctas"`
	CTASMethod   string `json:"ctas_method"`
	Template     any    `json:"template_params"`
	TemplateAlt  any    `json:"templateParams"`
	QueryLimit   int    `json:"query_limit"`
	QueryLimitA  int    `json:"queryLimit"`
}

type sqlLabEstimatePayload struct {
	DatabaseID int    `json:"database_id"`
	Schema     string `json:"schema"`
	SQL        string `json:"sql"`
}

type sqlLabFormatPayload struct {
	SQL string `json:"sql"`
}

type sqlLabExportPayload struct {
	DatabaseID  int    `json:"database_id"`
	Schema      string `json:"schema"`
	SQL         string `json:"sql"`
	QueryID     int    `json:"query_id"`
	QueryLimit  int    `json:"query_limit"`
	QueryLimitA int    `json:"queryLimit"`
}

// execute runs a statement synchronously and persists the query row. Async
// execution is accepted for compatibility but runs inline for now.
func (s *sqlLabAPI) execute(w http.ResponseWriter, r *http.Request) {
	var p sqlLabPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	stmt := strings.TrimSpace(p.SQL)
	if p.DatabaseID == 0 || stmt == "" {
		server.WriteError(w, apperrors.BadRequest("database_id and sql are required"))
		return
	}
	var database models.Database
	if err := s.deps.DB.WithContext(r.Context()).First(&database, p.DatabaseID).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Database not found"))
		return
	}

	limit := p.QueryLimit
	if limit == 0 {
		limit = p.QueryLimitA
	}
	if limit <= 0 {
		limit = s.deps.Config.DefaultSqllabLimit
	}
	if limit <= 0 {
		limit = 1000
	}
	ctas := p.SelectAsCTA || p.SelectAsCTAA
	ctasMethod := p.CTASMethod
	if ctasMethod == "" {
		ctasMethod = "TABLE"
	}
	// CTAS needs the temp-table bookkeeping of the synchronous path, so it is
	// always executed inline even when run_async is requested.
	runAsync := (p.RunAsync || p.RunAsyncAlt) && !ctas
	status := "running"
	if runAsync {
		status = "pending"
	}

	q := &models.Query{}
	db := s.deps.DB.WithContext(r.Context())
	if p.QueryID > 0 {
		if err := db.First(q, p.QueryID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				server.WriteError(w, apperrors.NotFound("Query not found"))
				return
			}
			server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
			return
		}
	}
	if q.ClientID == "" {
		q.ClientID = uuid.NewString()
	}
	q.DatabaseID = database.ID
	q.Schema = p.Schema
	q.Catalog = p.Catalog
	q.SQL = stmt
	q.UserID = intPtrNil(userIDOf(r))
	q.Status = status
	q.SelectAsCTA = &ctas
	q.CTASMethod = ctasMethod
	q.Limit = &limit
	q.Progress = 0
	if extra := sqlLabExtraMap(p.JSON); extra != nil {
		if tab, ok := extra["tab"].(string); ok {
			q.TabName = tab
		}
		if v, ok := extra["sql_editor_id"].(string); ok {
			q.SQLEditorID = v
		}
		q.ExtraJSON = datatypes.JSON([]byte(mustJSON(extra)))
	}
	q.StartTime = sqlLabEpochPtr(time.Now())
	q.EndTime = nil
	q.ErrorMessage = ""
	q.Rows = nil
	if err := db.Save(q).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Could not persist query: %s", err.Error()))
		return
	}

	if runAsync {
		s.executeAsync(w, r, db, q, database, p, limit)
		return
	}

	h, err := openPool(database.SQLAlchemyURI)
	if err != nil {
		sqlLabFailWith(w, db, q, err, s.queryTimeout())
		return
	}
	defer h.Close()

	readOnly := h.spec.IsReadOnlyQuery(stmt)
	if !readOnly && !database.AllowDML {
		db.Model(q).Updates(map[string]any{
			"status": "failed", "error_message": "This database does not allow DML statements",
			"end_time": sqlLabEpochPtr(time.Now()),
		})
		q.Status = "failed"
		q.ErrorMessage = "This database does not allow DML statements"
		q.EndTime = sqlLabEpochPtr(time.Now())
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"id": q.ID, "status": "failed", "data": []any{}, "columns": []any{},
			"query":  sqlLabQueryJSON(db, q),
			"errors": []map[string]any{{"message": "This database does not allow DML statements"}},
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.queryTimeout())
	defer cancel()

	execSQL := stmt
	if ctas && readOnly {
		tmpName := sqlLabTmpTableName()
		create := h.spec.CTASPrefix(p.Schema, tmpName) + stmt
		if _, err := h.Exec(ctx, create); err != nil {
			sqlLabFailWith(w, db, q, err, s.queryTimeout())
			return
		}
		q.TmpTableName = tmpName
		q.TmpSchemaName = p.Schema
		q.SelectAsCTAUsed = true
		execSQL = h.spec.SelectStar(qualified(h.spec, p.Schema, tmpName))
	} else if readOnly && limit > 0 {
		execSQL = sqlLabApplyLimit(h, stmt, limit)
	}
	q.ExecutedSQL = execSQL
	q.SelectSQL = stmt

	var (
		data    []map[string]any
		columns []map[string]any
		age     int
	)
	if readOnly {
		data, columns, age, err = sqlLabRun(ctx, h, execSQL)
	} else {
		var res interface{ RowsAffected() (int64, error) }
		res, err = h.Exec(ctx, execSQL)
		if err == nil {
			if n, aerr := res.RowsAffected(); aerr == nil {
				age = int(n)
			}
		}
	}
	if err != nil {
		if ctas && q.TmpTableName != "" {
			_, _ = h.Exec(ctx, "DROP TABLE IF EXISTS "+qualified(h.spec, q.TmpSchemaName, q.TmpTableName))
		}
		sqlLabFailWith(w, db, q, err, s.queryTimeout())
		return
	}

	rows := age
	q.Rows = &rows
	q.Status = "success"
	q.ErrorMessage = ""
	q.EndTime = sqlLabEpochPtr(time.Now())
	q.ResultsKey = "sqllab:" + q.ClientID + ":" + uuid.NewString()
	if err := db.Save(q).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Could not persist query: %s", err.Error()))
		return
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"id": q.ID, "status": q.Status, "data": data, "columns": columns,
		"query": sqlLabQueryJSON(db, q), "errors": []any{},
	})
}

// sqlLabAsyncTimeout bounds the in-process fallback. Queued work is bounded by
// the asynq task timeout (300s) instead.
const sqlLabAsyncTimeout = 300 * time.Second

// sqlLabCancelPoll bounds how often an inline execution checks the cancel flag.
const sqlLabCancelPoll = 250 * time.Millisecond

// executeAsync validates the statement, persists the final SQL and hands the
// query to the asynq queue. When the queue is unavailable (or the enqueue
// fails) the query runs in a goroutine instead and the response is marked
// async_fallback=true.
func (s *sqlLabAPI) executeAsync(w http.ResponseWriter, r *http.Request, db *gorm.DB, q *models.Query, database models.Database, p sqlLabPayload, limit int) {
	stmt := strings.TrimSpace(p.SQL)
	userID := userIDOf(r)

	h, err := openPool(database.SQLAlchemyURI)
	if err != nil {
		sqlLabFailWith(w, db, q, err, s.queryTimeout())
		return
	}
	defer h.Close()

	readOnly := h.spec.IsReadOnlyQuery(stmt)
	if !readOnly && !database.AllowDML {
		sqlLabFail(w, db, q, errors.New("This database does not allow DML statements"))
		return
	}

	// Dialect-specific LIMIT handling happens here so the worker only has to
	// execute the stored statement.
	execSQL := stmt
	if readOnly && limit > 0 {
		execSQL = sqlLabApplyLimit(h, stmt, limit)
	}
	q.SelectSQL = stmt
	q.ExecutedSQL = execSQL
	q.Status = "pending"
	if err := db.Save(q).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Could not persist query: %s", err.Error()))
		return
	}

	payload := map[string]any{
		"query_id":    q.ID,
		"user_id":     userID,
		"database_id": database.ID,
		"schema":      p.Schema,
		"sql":         stmt,
		"read_only":   readOnly,
	}

	fallback := false
	if s.deps.Queue != nil {
		if err := s.deps.Queue.EnqueueSQLQuery(r.Context(), q.ID, payload); err != nil {
			s.deps.Log.Warn("sqllab: enqueue failed, executing inline",
				zap.Int("query_id", q.ID), zap.Error(err))
			fallback = true
		}
	} else {
		fallback = true
	}
	if fallback {
		go s.runQueryInline(q.ID, database.SQLAlchemyURI, execSQL, readOnly, userID)
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"id":             q.ID,
		"status":         q.Status,
		"async":          true,
		"async_fallback": fallback,
		"query":          sqlLabQueryJSON(db, q),
		"errors":         []any{},
	})
}

// runQueryInline is the degraded async path: it mirrors the synchronous
// execution but writes its result to the result store and reports progress on
// the event bus.
func (s *sqlLabAPI) runQueryInline(queryID int, uri, stmt string, readOnly bool, userID int) {
	ctx, cancel := context.WithTimeout(context.Background(), sqlLabAsyncTimeout)
	defer cancel()

	db := s.deps.DB.WithContext(ctx)
	var q models.Query
	if err := db.First(&q, queryID).Error; err != nil {
		s.deps.Log.Warn("sqllab: inline query not found", zap.Int("query_id", queryID), zap.Error(err))
		return
	}
	if workers.IsCancelled(ctx, nil, queryID) {
		s.finishInlineCancelled(ctx, &q, userID)
		return
	}

	started := time.Now()
	db.Model(&q).Updates(map[string]any{"status": "running", "error_message": ""})
	s.publishAsyncEvent(userID, map[string]any{
		"type": "query_update", "query_id": queryID, "status": "running",
		"row_count": 0, "duration_ms": 0,
	})

	go func() {
		ticker := time.NewTicker(sqlLabCancelPoll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if workers.IsCancelled(context.Background(), nil, queryID) {
					cancel()
					return
				}
			}
		}
	}()

	h, err := openPool(uri)
	if err != nil {
		s.finishInlineFailed(ctx, &q, userID, err)
		return
	}
	defer h.Close()

	var (
		data    []map[string]any
		columns []map[string]any
		age     int
	)
	if readOnly {
		data, columns, age, err = sqlLabRun(ctx, h, stmt)
	} else {
		var res interface{ RowsAffected() (int64, error) }
		res, err = h.Exec(ctx, stmt)
		if err == nil {
			if n, aerr := res.RowsAffected(); aerr == nil {
				age = int(n)
			}
		}
	}
	if err != nil {
		if workers.IsCancelled(context.Background(), nil, queryID) || ctx.Err() != nil {
			s.finishInlineCancelled(ctx, &q, userID)
			return
		}
		s.finishInlineFailed(ctx, &q, userID, err)
		return
	}

	duration := time.Since(started)
	rows := age
	now := sqlLabEpochPtr(time.Now())
	db.Model(&q).Updates(map[string]any{
		"status": "success", "rows": rows, "end_time": now,
		"error_message": "", "results_key": workers.ResultKeyPrefix + strconv.Itoa(q.ID),
	})
	s.storeInlineResult(ctx, &workers.Result{
		QueryID: q.ID, Status: "success", Data: data, Columns: columns,
		RowCount: age, Errors: []map[string]any{},
		DurationMS: duration.Milliseconds(), FinishedAt: time.Now().UTC().Format(time.RFC3339),
	})
	workers.ClearCancel(context.Background(), nil, queryID)

	s.publishAsyncEvent(userID, map[string]any{
		"type": "query_update", "query_id": q.ID, "status": "success",
		"row_count": age, "duration_ms": duration.Milliseconds(),
	})
	s.publishAsyncEvent(userID, map[string]any{
		"type": "query_result", "query_id": q.ID, "status": "success",
		"rowcount": age, "columns": columns,
		"results_path": fmt.Sprintf("/api/v1/sqllab/%d/results", q.ID),
	})
}

func (s *sqlLabAPI) finishInlineFailed(ctx context.Context, q *models.Query, userID int, cause error) {
	msg := cause.Error()
	now := sqlLabEpochPtr(time.Now())
	s.deps.DB.Model(q).Updates(map[string]any{
		"status": "failed", "error_message": msg, "end_time": now,
	})
	s.storeInlineResult(ctx, &workers.Result{
		QueryID: q.ID, Status: "failed",
		Errors:     []map[string]any{{"message": msg}},
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	})
	workers.ClearCancel(context.Background(), nil, q.ID)
	s.publishAsyncEvent(userID, map[string]any{
		"type": "query_update", "query_id": q.ID, "status": "failed",
		"row_count": 0, "duration_ms": 0,
	})
	s.publishAsyncEvent(userID, map[string]any{
		"type": "error", "query_id": q.ID, "status": "failed", "message": msg,
	})
}

func (s *sqlLabAPI) finishInlineCancelled(ctx context.Context, q *models.Query, userID int) {
	now := sqlLabEpochPtr(time.Now())
	s.deps.DB.Model(q).Updates(map[string]any{"status": "cancelled", "end_time": now})
	s.storeInlineResult(ctx, &workers.Result{
		QueryID: q.ID, Status: "cancelled",
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	})
	workers.ClearCancel(context.Background(), nil, q.ID)
	s.publishAsyncEvent(userID, map[string]any{
		"type": "query_update", "query_id": q.ID, "status": "cancelled",
		"row_count": 0, "duration_ms": 0,
	})
}

func (s *sqlLabAPI) storeInlineResult(ctx context.Context, res *workers.Result) {
	var err error
	if s.deps.Queue != nil {
		err = s.deps.Queue.SaveResult(ctx, res)
	} else {
		err = workers.SaveResult(ctx, nil, res)
	}
	if err != nil {
		s.deps.Log.Warn("sqllab: persist inline result", zap.Int("query_id", res.QueryID), zap.Error(err))
	}
}

// publishAsyncEvent sends a query event through Redis when available, or
// directly to the local WebSocket hub otherwise.
func (s *sqlLabAPI) publishAsyncEvent(userID int, event map[string]any) {
	if s.deps.Queue != nil && s.deps.Queue.Bus() != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.deps.Queue.Bus().Publish(ctx, userID, event); err != nil {
			s.deps.Log.Warn("sqllab: publish event", zap.Error(err))
		}
		return
	}
	publishLocalEvent(userID, event)
}

// cancel marks a pending/running query as cancelled. The worker observes the
// flag and aborts its execution context.
func (s *sqlLabAPI) cancel(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	q, err := s.loadQueryForCaller(r, id)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	switch q.Status {
	case "success", "failed", "cancelled":
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"result": map[string]any{"id": q.ID, "status": q.Status},
		})
		return
	}

	if s.deps.Queue != nil {
		if err := s.deps.Queue.CancelQuery(r.Context(), id); err != nil {
			s.deps.Log.Warn("sqllab: cancel query", zap.Int("query_id", id), zap.Error(err))
		}
	} else {
		workers.CancelLocal(id)
	}

	now := sqlLabEpochPtr(time.Now())
	s.deps.DB.WithContext(r.Context()).Model(q).
		Where("status IN ?", []string{"pending", "running"}).
		Updates(map[string]any{"status": "cancelled", "end_time": now})

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": map[string]any{"id": id, "status": "cancelled"},
	})
}

// results returns the stored result document for a query. Results are readable
// once the worker (or the inline fallback) has finished writing them.
func (s *sqlLabAPI) results(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	q, err := s.loadQueryForCaller(r, id)
	if err != nil {
		server.WriteError(w, err)
		return
	}

	var (
		res *workers.Result
	)
	if s.deps.Queue != nil {
		res, err = s.deps.Queue.LoadResult(r.Context(), id)
	} else {
		res, err = workers.LoadResult(r.Context(), nil, id)
	}
	if errors.Is(err, workers.ErrResultNotFound) {
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"id": id, "status": q.Status,
			"data": []any{}, "columns": []any{}, "rowcount": 0,
			"errors": []map[string]any{{"message": "Result not available yet"}},
		})
		return
	}
	if err != nil {
		server.WriteError(w, apperrors.Internal("Could not load result: %s", err.Error()))
		return
	}
	data := res.Data
	if data == nil {
		data = []map[string]any{}
	}
	columns := res.Columns
	if columns == nil {
		columns = []map[string]any{}
	}
	errs := res.Errors
	if errs == nil {
		errs = []map[string]any{}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"id": res.QueryID, "status": res.Status, "data": data,
		"columns": columns, "rowcount": res.RowCount, "errors": errs,
	})
}

// loadQueryForCaller loads a query and enforces ownership (admins bypass it).
func (s *sqlLabAPI) loadQueryForCaller(r *http.Request, id int) (*models.Query, error) {
	var q models.Query
	if err := s.deps.DB.WithContext(r.Context()).First(&q, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("Query not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	if q.UserID != nil && *q.UserID != userIDOf(r) {
		if perms, ok := security.PermissionsFrom(r.Context()); !ok || !perms.Admin() {
			return nil, apperrors.Forbidden("You don't have access to this query")
		}
	}
	return &q, nil
}

// sqlLabFail marks a query as failed and writes the error envelope.
func sqlLabFail(w http.ResponseWriter, db *gorm.DB, q *models.Query, cause error) {
	sqlLabFailWith(w, db, q, cause, sqlLabQueryTimeout)
}

// sqlLabFailWith records a failed statement using the statement-error policy:
// connectivity and timeout failures get a classified, driver-free sentence;
// rejected statements keep the driver's text because that is the fix. The raw
// error still goes to the log for operators.
func sqlLabFailWith(w http.ResponseWriter, db *gorm.DB, q *models.Query, cause error, timeout time.Duration) {
	status, msg := queries.StatementErrorMessage(cause, timeout)
	if msg == "" {
		msg = cause.Error()
	}
	if status == 424 || status == 504 || status == 500 {
		zap.L().Warn("sql lab statement failed",
			zap.Int("query_id", q.ID), zap.Int("status", status), zap.Error(cause))
	}
	q.Status = "failed"
	q.ErrorMessage = msg
	q.EndTime = sqlLabEpochPtr(time.Now())
	_ = db.Save(q).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"id": q.ID, "status": "failed", "data": []any{}, "columns": []any{},
		"query":  sqlLabQueryJSON(db, q),
		"errors": []map[string]any{{"message": msg, "status_code": status}},
	})
}

// sqlLabRun executes a read statement preserving column order and types.
func sqlLabRun(ctx context.Context, h *poolHandle, stmt string) ([]map[string]any, []map[string]any, int, error) {
	rows, err := h.DB().QueryContext(ctx, stmt)
	if err != nil {
		return nil, nil, 0, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, 0, err
	}
	colTypes, _ := rows.ColumnTypes()
	columns := make([]map[string]any, 0, len(cols))
	for i, c := range cols {
		typeName := ""
		if i < len(colTypes) {
			typeName = colTypes[i].DatabaseTypeName()
		}
		columns = append(columns, map[string]any{
			"column_name": c, "name": c, "type": typeName,
			"is_dttm": isTemporalType(typeName),
		})
	}

	out := make([]map[string]any, 0, 64)
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, 0, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = normalizeValue(values[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, 0, err
	}
	return out, columns, len(out), nil
}

// sqlLabApplyLimit appends a LIMIT clause when the statement has no top-level
// LIMIT (a LIMIT inside a subquery does not cap the outer result set).
func sqlLabApplyLimit(h *poolHandle, stmt string, limit int) string {
	if sqlLabHasTopLevelLimit(stmt) {
		return stmt
	}
	trimmed := strings.TrimRight(strings.TrimSpace(stmt), ";")
	return trimmed + " " + h.spec.LimitClause(limit)
}

// sqlLabHasTopLevelLimit scans past string literals and nested parentheses.
func sqlLabHasTopLevelLimit(stmt string) bool {
	depth := 0
	for i := 0; i < len(stmt); {
		c := stmt[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			quote := c
			i++
			for i < len(stmt) {
				if stmt[i] == quote {
					if i+1 < len(stmt) && stmt[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		case c == '(':
			depth++
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			i++
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_':
			j := i
			for j < len(stmt) {
				ch := stmt[j]
				if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
					(ch >= '0' && ch <= '9') || ch == '_' {
					j++
					continue
				}
				break
			}
			if depth == 0 && strings.EqualFold(stmt[i:j], "LIMIT") {
				return true
			}
			i = j
		default:
			i++
		}
	}
	return false
}

func sqlLabTmpTableName() string {
	return "sqllab_tmp_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
}

func sqlLabEpochPtr(t time.Time) *float64 {
	v := float64(t.UnixNano()) / float64(time.Second)
	return &v
}

// sqlLabJSONBytes serialises a JSON column, defaulting to an empty object so
// that the value is always valid for a json/jsonb column.
func sqlLabJSONBytes(v any) []byte {
	s := jsonString(v)
	if s == "" {
		s = "{}"
	}
	return []byte(s)
}

func sqlLabExtraMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		return jsonMap([]byte(s))
	}
	return nil
}

// sqlLabQueryJSON renders the persisted query object.
func sqlLabQueryJSON(db *gorm.DB, q *models.Query) map[string]any {
	var dbName string
	var database models.Database
	if err := db.Select("id", "database_name").First(&database, q.DatabaseID).Error; err == nil {
		dbName = database.DatabaseName
	}
	return map[string]any{
		"id": q.ID, "client_id": q.ClientID, "database_id": q.DatabaseID,
		"database_name": dbName, "schema": q.Schema, "catalog": q.Catalog,
		"sql": q.SQL, "executed_sql": q.ExecutedSQL, "select_sql": q.SelectSQL,
		"status": q.Status, "start_time": q.StartTime, "end_time": q.EndTime,
		"rows": q.Rows, "error_message": nullIfEmpty(q.ErrorMessage),
		"results_key": q.ResultsKey, "progress": q.Progress, "limit": q.Limit,
		"select_as_cta": q.SelectAsCTA, "select_as_cta_used": q.SelectAsCTAUsed,
		"ctas_method": q.CTASMethod, "tmp_table_name": q.TmpTableName,
		"tmp_schema_name": q.TmpSchemaName, "user_id": q.UserID,
		"tab_name": q.TabName, "sql_editor_id": q.SQLEditorID,
		"extra_json": jsonField(q.ExtraJSON),
	}
}

func (s *sqlLabAPI) views(w http.ResponseWriter, r *http.Request) {
	var tabs []models.TabState
	if err := s.deps.DB.WithContext(r.Context()).
		Where("user_id = ?", userIDOf(r)).Order("id asc").Find(&tabs).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(tabs))
	for i := range tabs {
		out = append(out, sqlLabTabStateJSON(&tabs[i]))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out, "count": len(out)})
}

func (s *sqlLabAPI) history(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := s.deps.DB.WithContext(r.Context()).Model(&models.Query{})
	if perms, ok := security.PermissionsFrom(r.Context()); !ok || !perms.Admin() {
		q = q.Where("user_id = ?", userIDOf(r))
	}
	if p.Search != "" {
		q = q.Where("sql ILIKE ?", "%"+p.Search+"%")
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	names := map[int]string{}
	shaper := func(row *models.Query) any {
		name, ok := names[row.DatabaseID]
		if !ok {
			var database models.Database
			if err := s.deps.DB.WithContext(r.Context()).
				Select("id", "database_name").First(&database, row.DatabaseID).Error; err == nil {
				name = database.DatabaseName
			}
			names[row.DatabaseID] = name
		}
		return map[string]any{
			"id": row.ID, "sql": row.SQL, "status": row.Status,
			"start_time": row.StartTime, "end_time": row.EndTime, "rows": row.Rows,
			"database_name": name, "schema": row.Schema, "tab_name": row.TabName,
			"error_message": nullIfEmpty(row.ErrorMessage),
		}
	}
	listResultW(w, r, q, p, "id desc", shaper)
}

func (s *sqlLabAPI) get(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var q models.Query
	if err := s.deps.DB.WithContext(r.Context()).First(&q, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Query not found"))
		return
	}
	if q.UserID != nil && *q.UserID != userIDOf(r) {
		if perms, ok := security.PermissionsFrom(r.Context()); !ok || !perms.Admin() {
			server.WriteError(w, apperrors.Forbidden("You don't have access to this query"))
			return
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": sqlLabQueryJSON(s.deps.DB, &q)})
}

func (s *sqlLabAPI) estimate(w http.ResponseWriter, r *http.Request) {
	var p sqlLabEstimatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.DatabaseID == 0 || strings.TrimSpace(p.SQL) == "" {
		server.WriteError(w, apperrors.BadRequest("database_id and sql are required"))
		return
	}
	var database models.Database
	if err := s.deps.DB.WithContext(r.Context()).First(&database, p.DatabaseID).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Database not found"))
		return
	}
	h, err := openPool(database.SQLAlchemyURI)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	defer h.Close()
	probe := "SELECT COUNT(*) AS rowcount FROM (" +
		strings.TrimRight(strings.TrimSpace(p.SQL), ";") + ") AS sqllab_estimate"
	rows, err := h.Query(r.Context(), probe)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("Could not estimate query: %s", err.Error()))
		return
	}
	var rowcount any
	if len(rows) > 0 {
		rowcount = rows[0]["rowcount"]
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{"rowcount": rowcount}})
}

func (s *sqlLabAPI) format(w http.ResponseWriter, r *http.Request) {
	var p sqlLabFormatPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": sqlLabFormatSQL(p.SQL)})
}

func (s *sqlLabAPI) export(w http.ResponseWriter, r *http.Request) {
	var p sqlLabExportPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	stmt := strings.TrimSpace(p.SQL)
	databaseID := p.DatabaseID
	if p.QueryID > 0 {
		var q models.Query
		if err := s.deps.DB.WithContext(r.Context()).First(&q, p.QueryID).Error; err != nil {
			server.WriteError(w, apperrors.NotFound("Query not found"))
			return
		}
		if stmt == "" {
			stmt = q.SQL
		}
		if databaseID == 0 {
			databaseID = q.DatabaseID
		}
	}
	if databaseID == 0 || stmt == "" {
		server.WriteError(w, apperrors.BadRequest("database_id and sql are required"))
		return
	}
	limit := p.QueryLimit
	if limit == 0 {
		limit = p.QueryLimitA
	}
	if limit <= 0 {
		limit = s.deps.Config.SQLMaxRow
	}
	if limit <= 0 {
		limit = 10000
	}

	var database models.Database
	if err := s.deps.DB.WithContext(r.Context()).First(&database, databaseID).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Database not found"))
		return
	}
	h, err := openPool(database.SQLAlchemyURI)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	defer h.Close()
	if !h.spec.IsReadOnlyQuery(stmt) {
		server.WriteError(w, apperrors.BadRequest("Only read-only statements can be exported"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.queryTimeout())
	defer cancel()
	rows, columns, _, err := sqlLabRun(ctx, h, sqlLabApplyLimit(h, stmt, limit))
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("Could not export query: %s", err.Error()))
		return
	}

	filename := fmt.Sprintf("sqllab_export_%s.csv", time.Now().UTC().Format("20060102T150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	cw := csv.NewWriter(w)
	header := make([]string, 0, len(columns))
	for _, c := range columns {
		header = append(header, fmt.Sprint(c["column_name"]))
	}
	_ = cw.Write(header)
	for _, row := range rows {
		record := make([]string, 0, len(header))
		for _, name := range header {
			record = append(record, sqlLabCSVValue(row[name]))
		}
		if err := cw.Write(record); err != nil {
			return
		}
	}
	cw.Flush()
}

func sqlLabCSVValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32)
	default:
		return fmt.Sprint(t)
	}
}

// Saved queries -----------------------------------------------------------

type sqlLabSavedQueryPayload struct {
	Label              string `json:"label"`
	DbID               int    `json:"db_id"`
	DatabaseID         int    `json:"database_id"`
	Schema             string `json:"schema"`
	Catalog            string `json:"catalog"`
	SQL                string `json:"sql"`
	Description        string `json:"description"`
	TemplateParameters any    `json:"template_parameters"`
	ExtraJSON          any    `json:"extra_json"`
	Rows               *int   `json:"rows"`
}

// findSavedQuery resolves a saved query by id.
func (s *sqlLabAPI) findSavedQuery(r *http.Request) (*models.SavedQuery, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var q models.SavedQuery
	if err := s.deps.DB.WithContext(r.Context()).First(&q, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("Saved query not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &q, nil
}

func (s *sqlLabAPI) listSavedQueries(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := s.deps.DB.WithContext(r.Context()).Model(&models.SavedQuery{})
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("label ILIKE ? OR sql ILIKE ?", like, like)
	}
	if dbID := server.QueryInt(r, "db_id", 0); dbID > 0 {
		q = q.Where("db_id = ?", dbID)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "id desc", func(x *models.SavedQuery) any { return sqlLabSavedQueryJSON(x) })
}

func sqlLabSavedQueryJSON(q *models.SavedQuery) map[string]any {
	return map[string]any{
		"id": q.ID, "uuid": q.UUID, "label": q.Label, "db_id": q.DbID,
		"schema": q.Schema, "catalog": q.Catalog, "sql": q.SQL,
		"description": q.Description, "template_parameters": q.TemplateParameters,
		"extra_json": jsonField(q.ExtraJSON), "user_id": q.UserID,
		"rows": q.Rows, "last_run": q.LastRun,
		"created_on": q.CreatedOn, "changed_on": q.ChangedOn,
	}
}

func (s *sqlLabAPI) createSavedQuery(w http.ResponseWriter, r *http.Request) {
	var p sqlLabSavedQueryPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.Label == "" || p.SQL == "" {
		server.WriteError(w, apperrors.BadRequest("label and sql are required"))
		return
	}
	dbID := p.DbID
	if dbID == 0 {
		dbID = p.DatabaseID
	}
	if dbID == 0 {
		server.WriteError(w, apperrors.BadRequest("db_id is required"))
		return
	}
	var database models.Database
	if err := s.deps.DB.WithContext(r.Context()).First(&database, dbID).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Database not found"))
		return
	}
	q := models.SavedQuery{
		UUID: uuid.NewString(), UserID: intPtrNil(userIDOf(r)), DbID: &dbID,
		Schema: p.Schema, Catalog: p.Catalog, Label: p.Label, SQL: p.SQL,
		Description: p.Description, TemplateParameters: jsonString(p.TemplateParameters),
		ExtraJSON: datatypes.JSON(sqlLabJSONBytes(p.ExtraJSON)), Rows: p.Rows,
	}
	if err := s.deps.DB.WithContext(r.Context()).Create(&q).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create saved query: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": sqlLabSavedQueryJSON(&q)})
}

func (s *sqlLabAPI) getSavedQuery(w http.ResponseWriter, r *http.Request) {
	q, err := s.findSavedQuery(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": sqlLabSavedQueryJSON(q)})
}

func (s *sqlLabAPI) updateSavedQuery(w http.ResponseWriter, r *http.Request) {
	q, err := s.findSavedQuery(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p sqlLabSavedQueryPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.Label != "" {
		updates["label"] = p.Label
	}
	if p.SQL != "" {
		updates["sql"] = p.SQL
	}
	if p.Description != "" {
		updates["description"] = p.Description
	}
	if p.Schema != "" {
		updates["schema"] = p.Schema
	}
	if p.Catalog != "" {
		updates["catalog"] = p.Catalog
	}
	if p.TemplateParameters != nil {
		updates["template_parameters"] = jsonString(p.TemplateParameters)
	}
	if p.ExtraJSON != nil {
		updates["extra_json"] = jsonString(p.ExtraJSON)
	}
	if p.DbID > 0 {
		updates["db_id"] = p.DbID
	} else if p.DatabaseID > 0 {
		updates["db_id"] = p.DatabaseID
	}
	if p.Rows != nil {
		updates["rows"] = *p.Rows
	}
	if err := s.deps.DB.WithContext(r.Context()).Model(q).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update saved query: %s", err.Error()))
		return
	}
	_ = s.deps.DB.WithContext(r.Context()).First(q, q.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": sqlLabSavedQueryJSON(q)})
}

func (s *sqlLabAPI) deleteSavedQuery(w http.ResponseWriter, r *http.Request) {
	q, err := s.findSavedQuery(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := s.deps.DB.WithContext(r.Context()).Delete(q).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Tab state ---------------------------------------------------------------

type sqlLabTabStatePayload struct {
	Label          string `json:"label"`
	Active         *bool  `json:"active"`
	DatabaseID     int    `json:"database_id"`
	Schema         string `json:"schema"`
	Catalog        string `json:"catalog"`
	SQL            string `json:"sql"`
	QueryLimit     *int   `json:"query_limit"`
	LatestQueryID  string `json:"latest_query_id"`
	Autorun        *bool  `json:"autorun"`
	TemplateParams any    `json:"template_params"`
	HideLeftBar    *bool  `json:"hide_left_bar"`
	SavedQueryID   *int   `json:"saved_query_id"`
	ExtraJSON      any    `json:"extra_json"`
}

func sqlLabTabStateJSON(t *models.TabState) map[string]any {
	return map[string]any{
		"id": t.ID, "user_id": t.UserID, "label": t.Label, "active": t.Active,
		"database_id": t.DatabaseID, "schema": t.Schema, "catalog": t.Catalog,
		"sql": t.SQL, "query_limit": t.QueryLimit, "latest_query_id": t.LatestQueryID,
		"autorun": t.Autorun, "template_params": t.TemplateParams,
		"hide_left_bar": t.HideLeftBar, "saved_query_id": t.SavedQueryID,
		"extra_json": jsonField(t.ExtraJSON),
	}
}

func (s *sqlLabAPI) listTabStates(w http.ResponseWriter, r *http.Request) {
	var tabs []models.TabState
	if err := s.deps.DB.WithContext(r.Context()).
		Where("user_id = ?", userIDOf(r)).Order("id asc").Find(&tabs).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	out := make([]map[string]any, 0, len(tabs))
	for i := range tabs {
		out = append(out, sqlLabTabStateJSON(&tabs[i]))
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": out, "count": len(out)})
}

func (s *sqlLabAPI) createTabState(w http.ResponseWriter, r *http.Request) {
	var p sqlLabTabStatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	t := models.TabState{
		UserID: userIDOf(r), Label: p.Label, Active: boolOr(p.Active, false),
		DatabaseID: p.DatabaseID, Schema: p.Schema, Catalog: p.Catalog, SQL: p.SQL,
		QueryLimit: p.QueryLimit, LatestQueryID: p.LatestQueryID,
		Autorun: boolOr(p.Autorun, false), TemplateParams: jsonString(p.TemplateParams),
		HideLeftBar: boolOr(p.HideLeftBar, false), SavedQueryID: p.SavedQueryID,
		ExtraJSON: datatypes.JSON(sqlLabJSONBytes(p.ExtraJSON)),
	}
	if err := s.deps.DB.WithContext(r.Context()).Create(&t).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create tab state: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": sqlLabTabStateJSON(&t)})
}

func (s *sqlLabAPI) findTabState(r *http.Request) (*models.TabState, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var t models.TabState
	if err := s.deps.DB.WithContext(r.Context()).
		Where("id = ? AND user_id = ?", id, userIDOf(r)).First(&t).Error; err != nil {
		return nil, apperrors.NotFound("Tab state not found")
	}
	return &t, nil
}

func (s *sqlLabAPI) updateTabState(w http.ResponseWriter, r *http.Request) {
	t, err := s.findTabState(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p sqlLabTabStatePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{}
	if p.Label != "" {
		updates["label"] = p.Label
	}
	if p.Active != nil {
		updates["active"] = *p.Active
	}
	if p.DatabaseID != 0 {
		updates["database_id"] = p.DatabaseID
	}
	if p.Schema != "" {
		updates["schema"] = p.Schema
	}
	if p.Catalog != "" {
		updates["catalog"] = p.Catalog
	}
	if p.SQL != "" {
		updates["sql"] = p.SQL
	}
	if p.QueryLimit != nil {
		updates["query_limit"] = *p.QueryLimit
	}
	if p.LatestQueryID != "" {
		updates["latest_query_id"] = p.LatestQueryID
	}
	if p.Autorun != nil {
		updates["autorun"] = *p.Autorun
	}
	if p.TemplateParams != nil {
		updates["template_params"] = jsonString(p.TemplateParams)
	}
	if p.HideLeftBar != nil {
		updates["hide_left_bar"] = *p.HideLeftBar
	}
	if p.SavedQueryID != nil {
		updates["saved_query_id"] = *p.SavedQueryID
	}
	if p.ExtraJSON != nil {
		updates["extra_json"] = jsonString(p.ExtraJSON)
	}
	if err := s.deps.DB.WithContext(r.Context()).Model(t).Updates(updates).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not update tab state: %s", err.Error()))
		return
	}
	_ = s.deps.DB.WithContext(r.Context()).First(t, t.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": sqlLabTabStateJSON(t)})
}

func (s *sqlLabAPI) deleteTabState(w http.ResponseWriter, r *http.Request) {
	t, err := s.findTabState(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := s.deps.DB.WithContext(r.Context()).Delete(t).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Formatter ---------------------------------------------------------------

var sqlLabClauseKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "GROUP": true, "ORDER": true,
	"HAVING": true, "LIMIT": true, "OFFSET": true, "UNION": true,
	"JOIN": true, "LEFT": true, "RIGHT": true, "INNER": true, "OUTER": true,
	"FULL": true, "CROSS": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"WITH": true, "VALUES": true, "SET": true, "RETURNING": true,
}

var sqlLabKeywords = map[string]bool{
	"AS": true, "ON": true, "AND": true, "OR": true, "NOT": true, "IN": true,
	"IS": true, "NULL": true, "LIKE": true, "ILIKE": true, "BETWEEN": true,
	"EXISTS": true, "CASE": true, "WHEN": true, "THEN": true, "ELSE": true,
	"END": true, "ASC": true, "DESC": true, "DISTINCT": true, "ALL": true,
	"COUNT": true, "SUM": true, "AVG": true, "MIN": true, "MAX": true,
	"CAST": true, "BY": true, "INTO": true, "CREATE": true, "TABLE": true,
	"VIEW": true, "DROP": true, "ALTER": true, "TRUNCATE": true, "EXPLAIN": true,
	"SHOW": true, "USING": true, "OVER": true, "PARTITION": true, "WINDOW": true,
}

// sqlLabFormatSQL upper-cases keywords and breaks lines before top-level
// clauses. String literals, quoted identifiers and comments are preserved.
func sqlLabFormatSQL(in string) string {
	var b strings.Builder
	depth := 0
	i, n := 0, len(in)
	for i < n {
		c := in[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			quote := c
			j := i + 1
			for j < n {
				if in[j] == quote {
					if j+1 < n && in[j+1] == quote {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			b.WriteString(in[i:j])
			i = j
			continue
		case c == '-' && i+1 < n && in[i+1] == '-':
			j := strings.IndexByte(in[i:], '\n')
			if j < 0 {
				b.WriteString(in[i:])
				i = n
			} else {
				b.WriteString(in[i : i+j])
				i += j
			}
			continue
		case c == '/' && i+1 < n && in[i+1] == '*':
			j := strings.Index(in[i+2:], "*/")
			if j < 0 {
				b.WriteString(in[i:])
				i = n
			} else {
				b.WriteString(in[i : i+2+j+2])
				i += 2 + j + 2
			}
			continue
		case c == '(':
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_':
			j := i
			for j < n {
				ch := in[j]
				if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
					(ch >= '0' && ch <= '9') || ch == '_' {
					j++
					continue
				}
				break
			}
			word := strings.ToUpper(in[i:j])
			switch {
			case sqlLabClauseKeywords[word] && depth == 0:
				sqlLabNewline(&b)
				b.WriteString(word)
			case sqlLabKeywords[word]:
				b.WriteString(word)
			default:
				b.WriteString(in[i:j])
			}
			i = j
			continue
		}
		b.WriteByte(c)
		i++
	}
	return strings.TrimSpace(b.String())
}

// sqlLabNewline appends a newline unless the buffer is empty or already
// positioned at the beginning of a line.
func sqlLabNewline(b *strings.Builder) {
	s := b.String()
	trimmed := strings.TrimRight(s, " \t")
	if trimmed == "" {
		return
	}
	if strings.HasSuffix(trimmed, "\n") {
		return
	}
	if len(trimmed) != len(s) {
		// Drop the trailing spaces before breaking the line.
		b.Reset()
		b.WriteString(trimmed)
	}
	b.WriteByte('\n')
}
