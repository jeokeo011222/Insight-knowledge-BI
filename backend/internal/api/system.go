package api

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"superset/internal/config"
	"superset/internal/pkg/database"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterSystem mounts /api/v1/system/* — operations on the application's own
// metadata database connection (not the user-facing data sources). The whole
// module is admin-scoped: ResSystem is only granted to the Admin role by
// default, so other roles get 403 until an administrator grants it.
func RegisterSystem(r chi.Router, deps *Deps) {
	a := &systemAPI{deps: deps}

	r.Route("/system", func(sr chi.Router) {
		sr.Use(security.RequireUser,
			security.RequirePermission(security.ActionRead, security.ResSystem))

		sr.Get("/database", a.getDatabase)
		// Console landing data: module health, engine probes and theme slots
		// in one round-trip so the overview page paints without a waterfall.
		sr.Get("/overview", a.overview)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSystem)).
			Put("/database", a.updateDatabase)
		sr.With(security.RequirePermission(security.ActionWrite, security.ResSystem)).
			Post("/database/test", a.testDatabase)
	})
}

type systemAPI struct{ deps *Deps }

// metadataConn is the parsed view of the metadata DSN.
type metadataConn struct {
	Driver   string
	Host     string
	Port     string
	Database string
	Username string
	Password string
	// Extra keeps the original query string (e.g. sslmode) so rebuilding the
	// URI does not silently drop connection options.
	Extra string
}

// parseMetadataDSN parses a SQLAlchemy-style PostgreSQL URI into its parts.
// Non-URL (key=value) DSNs yield empty structured fields so the UI can fall
// back to the masked raw URI.
func parseMetadataDSN(raw string) metadataConn {
	c := metadataConn{Driver: "postgresql"}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return c
	}
	c.Driver = u.Scheme
	c.Host = u.Hostname()
	c.Port = u.Port()
	c.Database = strings.TrimPrefix(u.Path, "/")
	if u.User != nil {
		c.Username = u.User.Username()
		if p, ok := u.User.Password(); ok {
			c.Password = p
		}
	}
	c.Extra = u.RawQuery
	return c
}

// buildMetadataDSN assembles a PostgreSQL URI from structured parts.
func buildMetadataDSN(c metadataConn) string {
	u := url.URL{
		Scheme:   "postgresql",
		Host:     net.JoinHostPort(c.Host, c.Port),
		Path:     "/" + strings.TrimPrefix(c.Database, "/"),
		RawQuery: c.Extra,
	}
	if c.Username != "" {
		if c.Password != "" {
			u.User = url.UserPassword(c.Username, c.Password)
		} else {
			u.User = url.User(c.Username)
		}
	}
	return u.String()
}

// maskDSN hides the password in a URI for display.
func maskDSN(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User == nil {
		return raw
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "******")
	}
	return u.String()
}

// databasePayload is the safe, password-free view of the metadata connection.
func (a *systemAPI) databasePayload() map[string]any {
	raw := a.deps.Config.SQLAlchemyURI
	c := parseMetadataDSN(raw)
	path := config.ConfigFilePath()
	writable := false
	if path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			writable = true
		}
	}
	return map[string]any{
		"driver": c.Driver, "host": c.Host, "port": c.Port,
		"database": c.Database, "username": c.Username,
		"password_configured": c.Password != "",
		"uri_masked":          maskDSN(raw),
		"config_path":         path,
		"config_writable":     writable,
	}
}

func (a *systemAPI) getDatabase(w http.ResponseWriter, r *http.Request) {
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.databasePayload()})
}

// systemDatabasePayload is the editable subset sent by the client. An empty
// string means "keep the stored value", so the password is never echoed back.
type systemDatabasePayload struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// merge applies the non-empty overrides onto the stored connection.
func (p systemDatabasePayload) merge(base metadataConn) metadataConn {
	next := base
	if v := strings.TrimSpace(p.Host); v != "" {
		next.Host = v
	}
	if v := strings.TrimSpace(p.Port); v != "" {
		next.Port = v
	}
	if v := strings.TrimSpace(p.Database); v != "" {
		next.Database = v
	}
	if v := strings.TrimSpace(p.Username); v != "" {
		next.Username = v
	}
	if p.Password != "" {
		next.Password = p.Password
	}
	return next
}

func (a *systemAPI) updateDatabase(w http.ResponseWriter, r *http.Request) {
	var p systemDatabasePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	next := p.merge(parseMetadataDSN(a.deps.Config.SQLAlchemyURI))
	if next.Host == "" || next.Database == "" {
		server.WriteError(w, apperrors.BadRequest("host and database are required"))
		return
	}
	uri := buildMetadataDSN(next)
	path := config.ConfigFilePath()
	if err := config.PersistDatabaseURI(path, uri); err != nil {
		server.WriteError(w, apperrors.Internal("%s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"requires_restart": true,
		"config_path":      path,
		"uri_masked":       maskDSN(uri),
	}})
}

// testDatabase pings a connection built from the stored DSN plus any overrides
// in the (optional) request body. Failures are reported as ok=false with the
// driver message rather than an HTTP error, so the UI can show the reason.
func (a *systemAPI) testDatabase(w http.ResponseWriter, r *http.Request) {
	var p systemDatabasePayload
	_ = security.DecodeBody(r, &p) // an empty body tests the stored connection

	base := parseMetadataDSN(a.deps.Config.SQLAlchemyURI)
	uri := a.deps.Config.SQLAlchemyURI
	if base.Host != "" {
		uri = buildMetadataDSN(p.merge(base))
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	start := time.Now()
	db, err := database.Connect(uri, false)
	if err == nil {
		if sqlDB, derr := db.DB(); derr != nil {
			err = derr
		} else {
			err = sqlDB.PingContext(ctx)
			_ = sqlDB.Close()
		}
	}
	latency := time.Since(start).Milliseconds()
	if err != nil {
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
			"ok": false, "message": err.Error(), "latency_ms": latency,
		}})
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"ok": true, "message": "connection ok", "latency_ms": latency,
	}})
}
