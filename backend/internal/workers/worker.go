package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/config"
	"superset/internal/connectors"
	"superset/internal/models"
)

// Worker tuning. These mirror the Python task limits (SQLLAB_ASYNC_TIME_LIMIT_SEC
// and the celery concurrency setting).
const (
	workerConcurrency = 8
	workerTaskTimeout = 300 * time.Second
	workerMaxRetry    = 1
	cancelPollEvery   = 250 * time.Millisecond
)

// queryPayload is the JSON body of a sql_lab.execute_query task.
type queryPayload struct {
	QueryID    int    `json:"query_id"`
	UserID     int    `json:"user_id"`
	DatabaseID int    `json:"database_id"`
	Schema     string `json:"schema"`
	SQL        string `json:"sql"`
	ReadOnly   bool   `json:"read_only"`
}

// runner owns the worker-side dependencies.
type runner struct {
	log  *zap.Logger
	db   *gorm.DB
	rdb  *redis.Client
	bus  *Bus
	conn asynq.RedisClientOpt
}

// Run consumes sql_lab.execute_query until ctx is cancelled. It returns an
// error only for unrecoverable setup problems.
func Run(ctx context.Context, cfg *config.Config, log *zap.Logger, db *gorm.DB) error {
	return run(ctx, cfg, log, db, false)
}

// RunWorkerOnce is the test-friendly variant of Run: it processes a single
// query and then shuts the asynq server down. It exists so that the pipeline
// can be verified without wiring the worker into cmd/superset.
func RunWorkerOnce(ctx context.Context, cfg *config.Config, log *zap.Logger, db *gorm.DB) error {
	return run(ctx, cfg, log, db, true)
}

func run(ctx context.Context, cfg *config.Config, log *zap.Logger, db *gorm.DB, once bool) error {
	if db == nil {
		return errors.New("workers: metadata database is required")
	}
	r, err := newRunner(cfg, log, db)
	if err != nil {
		return err
	}
	defer r.close()

	done := make(chan struct{})
	var stop sync.Once
	var after func()
	if once {
		after = func() { stop.Do(func() { close(done) }) }
	}

	srv := r.newServer()
	startErr := make(chan error, 1)
	go func() { startErr <- srv.Start(r.handler(after)) }()
	if err := <-startErr; err != nil {
		return fmt.Errorf("worker: start: %w", err)
	}

	log.Info("worker: consuming sql_lab.execute_query",
		zap.Int("concurrency", workerConcurrency),
		zap.Duration("timeout", workerTaskTimeout))

	select {
	case <-ctx.Done():
	case <-done:
	}

	// Shutdown blocks until active tasks finish (or ShutdownTimeout expires).
	srv.Shutdown()
	return nil
}

func newRunner(cfg *config.Config, log *zap.Logger, db *gorm.DB) (*runner, error) {
	if cfg == nil {
		return nil, errors.New("workers: config is required")
	}
	if strings.TrimSpace(cfg.RedisURL) == "" {
		return nil, errors.New("workers: REDIS_URL is required to run the async worker")
	}
	opt, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("workers: parse redis url: %w", err)
	}
	rdb := redis.NewClient(opt)
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("workers: redis unavailable at %s: %w", opt.Addr, err)
	}
	bus, err := NewBus(cfg.RedisURL)
	if err != nil {
		_ = rdb.Close()
		return nil, err
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &runner{
		log: log,
		db:  db,
		rdb: rdb,
		bus: bus,
		conn: asynq.RedisClientOpt{
			Addr:      opt.Addr,
			Username:  opt.Username,
			Password:  opt.Password,
			DB:        opt.DB,
			TLSConfig: opt.TLSConfig,
		},
	}, nil
}

func (r *runner) close() {
	if r.bus != nil {
		_ = r.bus.Close()
	}
	if r.rdb != nil {
		_ = r.rdb.Close()
	}
}

func (r *runner) newServer() *asynq.Server {
	return asynq.NewServer(r.conn, asynq.Config{
		Concurrency: workerConcurrency,
		Queues:      map[string]int{"default": 1},
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
			r.log.Error("worker: task failed",
				zap.String("type", t.Type()), zap.Error(err))
		}),
	})
}

func (r *runner) handler(after func()) *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskSQLQuery, func(ctx context.Context, t *asynq.Task) error {
		if after != nil {
			defer after()
		}
		return r.handleSQLQuery(ctx, t)
	})
	return mux
}

func (r *runner) handleSQLQuery(ctx context.Context, t *asynq.Task) error {
	var p queryPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		r.log.Error("worker: invalid payload", zap.Error(err))
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	if p.QueryID <= 0 {
		return fmt.Errorf("%w: missing query_id", asynq.SkipRetry)
	}
	return r.executeQuery(ctx, p)
}

// executeQuery loads the Query row, runs the pre-computed statement, stores
// the result and updates the record. Business failures (bad SQL, missing
// database) are persisted and do not trigger an asynq retry.
func (r *runner) executeQuery(ctx context.Context, p queryPayload) error {
	started := time.Now()

	var q models.Query
	if err := r.db.WithContext(ctx).First(&q, p.QueryID).Error; err != nil {
		return fmt.Errorf("worker: load query %d: %w", p.QueryID, err)
	}
	userID := p.UserID
	if q.UserID != nil {
		userID = *q.UserID
	}

	// A cancel flag may have been written while the task sat in the queue.
	if q.Status == "cancelled" || IsCancelled(ctx, r.rdb, p.QueryID) {
		r.finishCancelled(ctx, &q, userID)
		return nil
	}

	var database models.Database
	if err := r.db.WithContext(ctx).First(&database, q.DatabaseID).Error; err != nil {
		r.finishFailed(ctx, &q, userID, fmt.Errorf("database %d not found: %w", q.DatabaseID, err))
		return nil
	}

	execSQL := strings.TrimSpace(q.ExecutedSQL)
	if execSQL == "" {
		execSQL = strings.TrimSpace(q.SQL)
	}
	if execSQL == "" {
		r.finishFailed(ctx, &q, userID, errors.New("query has no SQL to execute"))
		return nil
	}

	dbh, spec, err := connectors.Open(database.SQLAlchemyURI)
	if err != nil {
		r.finishFailed(ctx, &q, userID, err)
		return nil
	}
	defer dbh.Close()

	readOnly := p.ReadOnly || spec.IsReadOnlyQuery(execSQL)
	if !readOnly && !database.AllowDML {
		r.finishFailed(ctx, &q, userID, errors.New("this database does not allow DML statements"))
		return nil
	}

	now := epochNow()
	r.db.Model(&q).Updates(map[string]any{
		"status": "running", "start_time": now, "error_message": "",
	})
	q.Status = "running"
	q.StartTime = &now
	r.publishUpdate(userID, q.ID, "running", 0, 0)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	stopWatch := make(chan struct{})
	go r.watchCancel(runCtx, cancelRun, p.QueryID, stopWatch)
	defer close(stopWatch)

	var (
		data    []map[string]any
		columns []map[string]any
		age     int
	)
	if readOnly {
		data, columns, age, err = readRows(runCtx, dbh, execSQL)
	} else {
		var res sql.Result
		res, err = dbh.ExecContext(runCtx, execSQL)
		if err == nil {
			if n, aerr := res.RowsAffected(); aerr == nil {
				age = int(n)
			}
		}
	}
	if err != nil {
		if IsCancelled(context.Background(), r.rdb, p.QueryID) || errors.Is(runCtx.Err(), context.Canceled) {
			r.finishCancelled(ctx, &q, userID)
			return nil
		}
		r.finishFailed(ctx, &q, userID, err)
		return nil
	}

	duration := time.Since(started)
	result := &Result{
		QueryID:    q.ID,
		Status:     "success",
		Data:       data,
		Columns:    columns,
		RowCount:   age,
		Errors:     []map[string]any{},
		DurationMS: duration.Milliseconds(),
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	r.persistResult(ctx, result)

	writeCtx, cancelWrite := persistContext(ctx)
	rows := age
	err = r.db.WithContext(writeCtx).Model(&q).Updates(map[string]any{
		"status":        "success",
		"rows":          rows,
		"end_time":      epochNow(),
		"error_message": "",
		"results_key":   ResultKeyPrefix + fmt.Sprint(q.ID),
	}).Error
	cancelWrite()
	if err != nil {
		r.log.Error("worker: persist success", zap.Int("query_id", q.ID), zap.Error(err))
	}
	ClearCancel(context.Background(), r.rdb, q.ID)

	r.publishUpdate(userID, q.ID, "success", age, duration.Milliseconds())
	r.publishEvent(userID, Event{
		"type":         "query_result",
		"query_id":     q.ID,
		"status":       "success",
		"rowcount":     age,
		"columns":      columns,
		"results_path": fmt.Sprintf("/api/v1/sqllab/%d/results", q.ID),
	})
	r.log.Info("worker: query succeeded",
		zap.Int("query_id", q.ID), zap.Int("rows", age),
		zap.Duration("duration", duration))
	return nil
}

// watchCancel polls the cancel flag while the statement runs and aborts the
// execution context as soon as it is set.
func (r *runner) watchCancel(ctx context.Context, cancel context.CancelFunc, queryID int, stop <-chan struct{}) {
	ticker := time.NewTicker(cancelPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if IsCancelled(context.Background(), r.rdb, queryID) {
				cancel()
				return
			}
		}
	}
}

func (r *runner) finishCancelled(ctx context.Context, q *models.Query, userID int) {
	now := epochNow()
	writeCtx, cancel := persistContext(ctx)
	defer cancel()
	err := r.db.WithContext(writeCtx).Model(q).Updates(map[string]any{
		"status": "cancelled", "end_time": now, "error_message": "",
	}).Error
	if err != nil {
		r.log.Error("worker: persist cancelled", zap.Int("query_id", q.ID), zap.Error(err))
	}
	q.Status = "cancelled"
	q.EndTime = &now
	ClearCancel(context.Background(), r.rdb, q.ID)

	r.persistResult(ctx, &Result{
		QueryID:    q.ID,
		Status:     "cancelled",
		Data:       []map[string]any{},
		Columns:    []map[string]any{},
		Errors:     []map[string]any{},
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	})
	r.publishUpdate(userID, q.ID, "cancelled", 0, 0)
}

func (r *runner) finishFailed(ctx context.Context, q *models.Query, userID int, cause error) {
	msg := cause.Error()
	now := epochNow()
	writeCtx, cancel := persistContext(ctx)
	defer cancel()
	err := r.db.WithContext(writeCtx).Model(q).Updates(map[string]any{
		"status": "failed", "error_message": msg, "end_time": now,
	}).Error
	if err != nil {
		r.log.Error("worker: persist failure", zap.Int("query_id", q.ID), zap.Error(err))
	}
	q.Status = "failed"
	q.ErrorMessage = msg
	q.EndTime = &now
	ClearCancel(context.Background(), r.rdb, q.ID)

	r.persistResult(ctx, &Result{
		QueryID:    q.ID,
		Status:     "failed",
		Data:       []map[string]any{},
		Columns:    []map[string]any{},
		Errors:     []map[string]any{{"message": msg}},
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	})
	r.publishUpdate(userID, q.ID, "failed", 0, 0)
	r.publishEvent(userID, Event{
		"type":     "error",
		"query_id": q.ID,
		"status":   "failed",
		"message":  msg,
	})
	r.log.Warn("worker: query failed", zap.Int("query_id", q.ID), zap.String("error", msg))
}

// persistResult stores a result document even if the task context was already
// cancelled (for example due to an asynq timeout).
func (r *runner) persistResult(ctx context.Context, res *Result) {
	writeCtx, cancel := persistContext(ctx)
	defer cancel()
	if err := SaveResult(writeCtx, r.rdb, res); err != nil {
		r.log.Error("worker: persist result", zap.Int("query_id", res.QueryID), zap.Error(err))
	}
}

func (r *runner) publishUpdate(userID, queryID int, status string, rows int, durationMS int64) {
	r.publishEvent(userID, Event{
		"type":        "query_update",
		"query_id":    queryID,
		"status":      status,
		"row_count":   rows,
		"duration_ms": durationMS,
	})
}

func (r *runner) publishEvent(userID int, event Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.bus.Publish(ctx, userID, event); err != nil {
		r.log.Warn("worker: publish event", zap.Error(err))
	}
}

// persistContext detaches database writes from the task deadline so that the
// final status update still lands after a timeout.
func persistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
}

func epochNow() float64 {
	return float64(time.Now().UnixNano()) / float64(time.Second)
}

// readRows executes a read statement and returns rows plus column metadata.
func readRows(ctx context.Context, db *sql.DB, stmt string) ([]map[string]any, []map[string]any, int, error) {
	rows, err := db.QueryContext(ctx, stmt)
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

// normalizeValue converts driver values into JSON-friendly types.
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

// isTemporalType mirrors the SQL Lab column annotation for date/time types.
func isTemporalType(name string) bool {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "date"), strings.Contains(n, "time"),
		strings.Contains(n, "timestamp"), strings.Contains(n, "interval"):
		return true
	}
	return false
}
