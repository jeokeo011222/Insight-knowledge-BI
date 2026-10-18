// Package workers implements the asynchronous SQL Lab pipeline: an asynq
// backed task queue, a result store (Redis with a filesystem fallback), an
// event bus and a WebSocket fan-out hub.
//
// The package is deliberately independent from internal/api so that the
// queue producer (HTTP handlers) and the consumer (worker process) can be
// wired independently.
package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// Task type names. TaskThumbnail is consumed by the thumbnails package; it is
// declared here so that producers share a single source of truth.
const (
	TaskSQLQuery  = "sql_lab.execute_query"
	TaskThumbnail = "cache.thumbnail"
)

const (
	// ResultKeyPrefix prefixes the Redis key holding a serialised Result.
	ResultKeyPrefix = "sqllab:results:"
	// CancelKeyPrefix prefixes the Redis key used as a cooperative cancel flag.
	CancelKeyPrefix = "sqllab:cancel:"
	// EventChannel is the Redis pub/sub channel used by Bus.
	EventChannel = "superset:events"

	resultTTL      = time.Hour
	cancelFlagTTL  = 10 * time.Minute
	queueTaskLimit = 300 * time.Second
	queueMaxRetry  = 1
)

// ErrResultNotFound is returned when neither Redis nor the filesystem holds a
// result for the requested query.
var ErrResultNotFound = errors.New("workers: query result not found")

// localCancels keeps cancel flags for the in-process fallback path (used when
// Redis or the asynq queue is unavailable). It is shared by the API process
// and any worker running inside it.
var localCancels sync.Map // map[int]time.Time

// Client is the producer side of the queue. It owns the Redis connection used
// for cancel flags, result storage and event publication.
type Client struct {
	rdb   *redis.Client
	queue *asynq.Client
	bus   *Bus
}

// NewClient connects to Redis and returns a queue client. A clear error is
// returned when Redis is unreachable so callers can degrade gracefully
// instead of panicking.
func NewClient(redisURL string) (*Client, error) {
	redisURL = strings.TrimSpace(redisURL)
	if redisURL == "" {
		return nil, errors.New("workers: REDIS_URL is empty, async queue disabled")
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("workers: parse redis url: %w", err)
	}
	rdb := redis.NewClient(opt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("workers: redis unavailable at %s: %w", opt.Addr, err)
	}
	bus, err := NewBus(redisURL)
	if err != nil {
		_ = rdb.Close()
		return nil, err
	}
	return &Client{
		rdb: rdb,
		queue: asynq.NewClient(asynq.RedisClientOpt{
			Addr:      opt.Addr,
			Username:  opt.Username,
			Password:  opt.Password,
			DB:        opt.DB,
			TLSConfig: opt.TLSConfig,
		}),
		bus: bus,
	}, nil
}

// Bus exposes the event bus attached to the client.
func (c *Client) Bus() *Bus {
	if c == nil {
		return nil
	}
	return c.bus
}

// Redis exposes the underlying Redis client (nil when the client is nil).
func (c *Client) Redis() *redis.Client {
	if c == nil {
		return nil
	}
	return c.rdb
}

// EnqueueSQLQuery schedules TaskSQLQuery for the given query id. The payload
// is merged with query_id so the consumer can always recover the record.
func (c *Client) EnqueueSQLQuery(ctx context.Context, queryID int, payload map[string]any) error {
	if c == nil || c.queue == nil {
		return errors.New("workers: queue client is not configured")
	}
	if queryID <= 0 {
		return errors.New("workers: query id is required")
	}
	body := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		body[k] = v
	}
	body["query_id"] = queryID
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("workers: marshal payload: %w", err)
	}
	task := asynq.NewTask(
		TaskSQLQuery,
		raw,
		asynq.Queue("default"),
		asynq.MaxRetry(queueMaxRetry),
		asynq.Timeout(queueTaskLimit),
	)
	if _, err := c.queue.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("workers: enqueue query %d: %w", queryID, err)
	}
	return nil
}

// CancelQuery writes the cooperative cancel flag and broadcasts a
// "cancelled" query_update event. The event is published with user_id 0
// (broadcast) because the caller identity is not part of the cancel flag.
func (c *Client) CancelQuery(ctx context.Context, queryID int) error {
	if queryID <= 0 {
		return errors.New("workers: query id is required")
	}
	markLocalCancel(queryID)
	if c == nil {
		return nil
	}
	var firstErr error
	if c.rdb != nil {
		if err := c.rdb.Set(ctx, cancelKey(queryID), "1", cancelFlagTTL).Err(); err != nil {
			firstErr = fmt.Errorf("workers: write cancel flag: %w", err)
		}
	}
	if c.bus != nil {
		event := map[string]any{
			"type":        "query_update",
			"query_id":    queryID,
			"status":      "cancelled",
			"row_count":   0,
			"duration_ms": 0,
		}
		if err := c.bus.Publish(ctx, 0, event); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close releases the queue, Redis connection and bus.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.queue != nil {
		_ = c.queue.Close()
	}
	if c.bus != nil {
		_ = c.bus.Close()
	}
	if c.rdb != nil {
		return c.rdb.Close()
	}
	return nil
}

// Result is the document persisted after a query finishes. It doubles as the
// payload returned by GET /api/v1/sqllab/{pk}/results.
type Result struct {
	QueryID    int              `json:"query_id"`
	Status     string           `json:"status"`
	Data       []map[string]any `json:"data"`
	Columns    []map[string]any `json:"columns"`
	RowCount   int              `json:"rowcount"`
	Errors     []map[string]any `json:"errors"`
	DurationMS int64            `json:"duration_ms"`
	FinishedAt string           `json:"finished_at"`
}

// SaveResult persists a result document in Redis (TTL 1h) and mirrors it to
// the filesystem when Redis is unavailable.
func (c *Client) SaveResult(ctx context.Context, res *Result) error {
	if res == nil {
		return errors.New("workers: result is nil")
	}
	var rdb *redis.Client
	if c != nil {
		rdb = c.rdb
	}
	return SaveResult(ctx, rdb, res)
}

// LoadResult reads a result document from Redis, falling back to disk.
func (c *Client) LoadResult(ctx context.Context, queryID int) (*Result, error) {
	var rdb *redis.Client
	if c != nil {
		rdb = c.rdb
	}
	return LoadResult(ctx, rdb, queryID)
}

// SaveResult writes res to Redis key sqllab:results:<id> with a one hour TTL.
// A nil client (or a failed write) falls back to
// os.TempDir()/superset-results/<id>.json.
func SaveResult(ctx context.Context, rdb *redis.Client, res *Result) error {
	raw, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("workers: marshal result: %w", err)
	}
	if rdb != nil {
		if err := rdb.Set(ctx, resultKey(res.QueryID), raw, resultTTL).Err(); err == nil {
			return nil
		}
	}
	return writeResultFile(res.QueryID, raw)
}

// LoadResult reads a result document from Redis first and the filesystem
// second. ErrResultNotFound is returned when both sources miss.
func LoadResult(ctx context.Context, rdb *redis.Client, queryID int) (*Result, error) {
	if rdb != nil {
		raw, err := rdb.Get(ctx, resultKey(queryID)).Bytes()
		switch {
		case err == nil:
			var res Result
			if err := json.Unmarshal(raw, &res); err != nil {
				return nil, fmt.Errorf("workers: decode result: %w", err)
			}
			return &res, nil
		case !errors.Is(err, redis.Nil):
			// Redis is degraded; the file copy may still be present.
			_ = err
		}
	}
	raw, err := os.ReadFile(resultFilePath(queryID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrResultNotFound
		}
		return nil, fmt.Errorf("workers: read result file: %w", err)
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("workers: decode result file: %w", err)
	}
	return &res, nil
}

// DeleteResult removes a result from both backends.
func DeleteResult(ctx context.Context, rdb *redis.Client, queryID int) error {
	if rdb != nil {
		_ = rdb.Del(ctx, resultKey(queryID)).Err()
	}
	if err := os.Remove(resultFilePath(queryID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeResultFile(queryID int, raw []byte) error {
	path := resultFilePath(queryID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("workers: create result dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("workers: write result file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("workers: commit result file: %w", err)
	}
	return nil
}

func resultDir() string {
	return filepath.Join(os.TempDir(), "superset-results")
}

func resultFilePath(queryID int) string {
	return filepath.Join(resultDir(), strconv.Itoa(queryID)+".json")
}

func resultKey(queryID int) string {
	return ResultKeyPrefix + strconv.Itoa(queryID)
}

func cancelKey(queryID int) string {
	return CancelKeyPrefix + strconv.Itoa(queryID)
}

// markLocalCancel records a cancel request for the in-process fallback.
func markLocalCancel(queryID int) {
	localCancels.Store(queryID, time.Now())
}

// CancelLocal records a cancel request in the current process. It is used when
// Redis is unavailable and run_async degraded to a goroutine.
func CancelLocal(queryID int) {
	markLocalCancel(queryID)
}

// ClearCancel removes the cancel flag from both backends.
func ClearCancel(ctx context.Context, rdb *redis.Client, queryID int) {
	localCancels.Delete(queryID)
	if rdb != nil {
		_ = rdb.Del(ctx, cancelKey(queryID)).Err()
	}
}

// IsCancelled reports whether a cancel flag exists in Redis or in the
// in-process registry.
func IsCancelled(ctx context.Context, rdb *redis.Client, queryID int) bool {
	if _, ok := localCancels.Load(queryID); ok {
		return true
	}
	if rdb == nil {
		return false
	}
	n, err := rdb.Exists(ctx, cancelKey(queryID)).Result()
	return err == nil && n > 0
}
