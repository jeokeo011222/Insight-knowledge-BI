// Package api wires all REST modules onto the router. Each module exposes a
// Register function; RegisterAll mounts them under /api/v1.
package api

import (
	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/config"
	"superset/internal/pkg/cache"
	"superset/internal/pkg/i18n"
	"superset/internal/security"
	"superset/internal/server"
	"superset/internal/thumbnails"
	"superset/internal/workers"

	"github.com/go-chi/chi/v5"
)

// Deps carries shared dependencies for all API modules.
type Deps struct {
	Config *config.Config
	Log    *zap.Logger
	DB     *gorm.DB
	Cache  *cache.Client
	I18n   *i18n.Translator
	Mgr    *security.Manager
	Auth   *security.Authenticator
	// Thumbs renders chart/dashboard thumbnails with a headless browser.
	Thumbs *thumbnails.Service
	// Queue is the async SQL Lab producer. It is nil when Redis is
	// unavailable, in which case run_async degrades to an in-process
	// goroutine (the response then carries async_fallback=true).
	Queue *workers.Client
}

// RegisterAll mounts every REST module. New modules are added here as they
// are implemented (see docs/api-registry.md).
func RegisterAll(srv *server.Server, cfg *config.Config, log *zap.Logger, tr *i18n.Translator, db *gorm.DB) error {
	// Make the translator available to message helpers everywhere in the API.
	i18n.SetDefault(tr)

	mgr := security.NewManager(db, log)
	auth := security.NewAuthenticator(db, mgr, log, cfg.SecretKey, cfg.GuestTokenJWTSecret, cfg.GuestTokenHeaderName)

	// Redis backs query-result caching, filter state and thumbnails. A
	// missing Redis must not prevent the server from starting, so failures
	// degrade to a nil cache.
	var cacheClient *cache.Client
	if cfg.RedisURL != "" {
		if c, err := cache.New(cfg.RedisURL); err != nil {
			log.Warn("redis unavailable, caching disabled", zap.Error(err))
		} else {
			cacheClient = c
		}
	}
	deps := &Deps{Config: cfg, Log: log, DB: db, Cache: cacheClient, I18n: tr, Mgr: mgr, Auth: auth}
	// Thumbnails are captured on demand and cached under the OS temp dir.
	deps.Thumbs = thumbnails.NewService(cfg, log)

	// The async SQL Lab queue piggybacks on the same Redis instance. Without
	// Redis, run_async falls back to in-process goroutines.
	if cfg.RedisURL != "" {
		if q, err := workers.NewClient(cfg.RedisURL); err != nil {
			log.Warn("async query queue unavailable, falling back to goroutines", zap.Error(err))
		} else {
			deps.Queue = q
		}
	}

	// Language resolution must run before any handler that can produce a
	// localized error message.
	srv.Router.Use(i18n.Middleware)

	// Authentication runs for every route so that handlers can inspect the
	// caller without re-parsing credentials.
	srv.Router.Use(auth.Authenticate)

	r := srv.Router
	r.Route("/api/v1", func(api chi.Router) {
		RegisterSecurity(api, deps, auth, mgr)
		RegisterDatabases(api, deps)
		RegisterDatasets(api, deps)
		RegisterSemantic(api, deps)
		RegisterCharts(api, deps)
		RegisterDashboards(api, deps)
		RegisterPublicShares(api, deps)
		RegisterHealth(api, deps)
		RegisterLineage(api, deps)
		RegisterScreens(api, deps)
		RegisterQueries(api, deps)
		RegisterSqlLab(api, deps)
		RegisterAnnotations(api, deps)
		RegisterTags(api, deps)
		RegisterRLS(api, deps)
		RegisterReports(api, deps)
		RegisterEmbedded(api, deps)
		RegisterImportExport(api, deps)
		RegisterThemes(api, deps)
		RegisterMisc(api, deps)
		RegisterWS(api, deps)
		RegisterAI(api, deps)
		RegisterSystem(api, deps)
	})

	// Authenticated user-facing HTML entrypoint (SPA shell).
	srv.RegisterSPA()

	return nil
}
