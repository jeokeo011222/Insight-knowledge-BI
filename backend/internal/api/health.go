package api

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"superset/internal/connectors"
	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// Data-source health probes (数据源健康检查).
//
// A probe opens a connection through the connector layer and runs a trivial
// statement. Results are persisted on the db row so the engine list can paint
// a status dot without touching every database on each page load.
//
// Scheduling: rather than adding a cron entry, the list endpoint refreshes
// stale rows in the background (see maybeProbeStale). That keeps the feature
// dependency-free and still gives every engine a fresh status within one page
// visit, at a bounded cost.

// probeStaleAfter is how old a probe may be before it is refreshed.
const probeStaleAfter = 6 * time.Hour

// probeTimeout bounds a single probe.
const probeTimeout = 12 * time.Second

// probeConcurrency caps the background refresh fan-out.
const probeConcurrency = 3

// probeLocks serialises probe runs per database id.
var probeLocks sync.Map

// probeResult is the outcome of one probe.
type probeResult struct {
	OK        bool
	LatencyMs int
	Error     string
}

// probeDatabase pings one engine and records the outcome.
func (d *databaseAPI) probeDatabase(ctx context.Context, db *models.Database) probeResult {
	lock, _ := probeLocks.LoadOrStore(db.ID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	start := time.Now()
	err := connectors.Ping(ctx, db.SQLAlchemyURI)
	latency := int(time.Since(start).Milliseconds())
	result := probeResult{OK: err == nil, LatencyMs: latency}
	if err != nil {
		result.Error = err.Error()
		// Keep the message short enough for a table cell / tooltip.
		if len(result.Error) > 500 {
			result.Error = result.Error[:500]
		}
	}

	now := time.Now()
	updates := map[string]any{
		"last_probe_at":    now,
		"last_probe_ok":    result.OK,
		"last_probe_ms":    latency,
		"last_probe_error": result.Error,
	}
	// The probe result is metadata: a write failure must not surface as a 500
	// on the caller's page load.
	_ = d.deps.DB.WithContext(context.WithoutCancel(ctx)).
		Model(&models.Database{}).Where("id = ?", db.ID).Updates(updates).Error
	return result
}

// maybeProbeStale refreshes a few stale engines in the background.
func (d *databaseAPI) maybeProbeStale(ctx context.Context, list []models.Database) {
	stale := make([]models.Database, 0, probeConcurrency)
	cutoff := time.Now().Add(-probeStaleAfter)
	for index := range list {
		item := list[index]
		if item.LastProbeAt != nil && item.LastProbeAt.After(cutoff) {
			continue
		}
		stale = append(stale, item)
		if len(stale) >= probeConcurrency {
			break
		}
	}
	if len(stale) == 0 {
		return
	}
	// Detach from the request: the page should not wait for the probes.
	base := context.WithoutCancel(ctx)
	for index := range stale {
		target := stale[index]
		go func() {
			probeCtx, cancel := context.WithTimeout(base, probeTimeout+2*time.Second)
			defer cancel()
			d.probeDatabase(probeCtx, &target)
		}()
	}
}

// RegisterHealth mounts the health endpoints.
func RegisterHealth(r chi.Router, deps *Deps) {
	d := &databaseAPI{deps: deps}
	r.Group(func(gr chi.Router) {
		gr.Use(security.RequireUser)
		gr.With(security.RequirePermission(security.ActionRead, security.ResDatabase)).
			Get("/database/health", d.health)
		gr.With(security.RequirePermission(security.ActionTestConn, security.ResDatabase)).
			Post("/database/{pk}/probe", d.probe)
		gr.With(security.RequirePermission(security.ActionTestConn, security.ResDatabase)).
			Post("/database/probe_all", d.probeAll)
	})
}

// health returns the stored probe status of every engine.
func (d *databaseAPI) health(w http.ResponseWriter, r *http.Request) {
	var rows []models.Database
	if err := d.deps.DB.WithContext(r.Context()).
		Order("database_name").
		Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	d.maybeProbeStale(r.Context(), rows)

	result := make([]map[string]any, 0, len(rows))
	down := 0
	for _, row := range rows {
		if row.LastProbeOK != nil && !*row.LastProbeOK {
			down++
		}
		result = append(result, map[string]any{
			"id":               row.ID,
			"database_name":    row.DatabaseName,
			"backend":          "sqlalchemy",
			"last_probe_at":    row.LastProbeAt,
			"last_probe_ok":    row.LastProbeOK,
			"last_probe_ms":    row.LastProbeMs,
			"last_probe_error": nullIfEmpty(row.LastProbeError),
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result":  result,
		"total":   len(result),
		"failing": down,
	})
}

// probe forces a fresh probe of one engine.
func (d *databaseAPI) probe(w http.ResponseWriter, r *http.Request) {
	db, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	result := d.probeDatabase(r.Context(), db)
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result": map[string]any{
			"id":               db.ID,
			"ok":               result.OK,
			"latency_ms":       result.LatencyMs,
			"error":            nullIfEmpty(result.Error),
			"last_probe_at":    time.Now(),
			"last_probe_ok":    result.OK,
			"last_probe_ms":    result.LatencyMs,
			"last_probe_error": nullIfEmpty(result.Error),
		},
	})
}

// probeAll answers POST /database/probe_all: a bounded sweep triggered from the
// engine list ("全部检测").
func (d *databaseAPI) probeAll(w http.ResponseWriter, r *http.Request) {
	var rows []models.Database
	if err := d.deps.DB.WithContext(r.Context()).
		Order("database_name").Find(&rows).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	probed := 0
	failing := 0
	for index := range rows {
		result := d.probeDatabase(r.Context(), &rows[index])
		probed++
		if !result.OK {
			failing++
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"probed": probed, "failing": failing,
		"message": "Probed " + strconv.Itoa(probed) + " engines",
	})
}
