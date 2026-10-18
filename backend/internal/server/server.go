// Package server wires the HTTP layer: chi router, middleware chain,
// FAB-style response helpers and health endpoints.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	apperrors "superset/internal/pkg/errors"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

// Version is reported via the X-Superset-Version header and /health.
var Version = "6.0.0"

// Server holds shared HTTP dependencies.
type Server struct {
	Router *chi.Mux
	Log    *zap.Logger
}

// New builds the server with the standard middleware chain:
// RequestID -> Logger -> Recover -> CORS -> handler.
func New(log *zap.Logger) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(Logger(log))
	r.Use(Recover(log))
	r.Use(CORS)
	r.Use(VersionHeader)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, apperrors.NotFound("Not found"))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, apperrors.BadRequest("Method not allowed"))
	})
	return &Server{Router: r, Log: log}
}

// VersionHeader adds X-Superset-Version to every response.
func VersionHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Superset-Version", Version)
		next.ServeHTTP(w, r)
	})
}

// Logger is the request logging middleware.
func Logger(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			log.Info("request",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", ww.Status()),
				zap.Duration("duration", time.Since(start)),
				zap.String("request_id", middleware.GetReqID(r.Context())),
			)
		})
	}
}

// Recover converts panics into 500 JSON responses.
func Recover(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered",
						zap.Any("error", rec),
						zap.ByteString("stack", debug.Stack()),
					)
					WriteError(w, apperrors.Internal("Internal server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS enables cross-origin requests (mirrors ENABLE_CORS default true).
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-CSRFToken, X-GuestToken, X-Requested-With")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSON writes a JSON body with a status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// WriteError writes the superset-style error body {"message": ...}.
func WriteError(w http.ResponseWriter, err error) {
	ae := apperrors.From(err)
	WriteJSON(w, ae.Status(), map[string]any{"message": ae.Message})
}

// WriteResult writes a single resource JSON.
func WriteResult(w http.ResponseWriter, v any) {
	WriteJSON(w, http.StatusOK, v)
}

// Paginated is the FAB-style list response.
type Paginated struct {
	Count    int64 `json:"count"`
	Result   any   `json:"result"`
	IDsAbove []int `json:"ids_above,omitempty"`
	IDsBelow []int `json:"ids_below,omitempty"`
}

// WritePaginated writes a FAB-style paginated response.
func WritePaginated(w http.ResponseWriter, count int64, result any, idsAbove, idsBelow []int) {
	WriteJSON(w, http.StatusOK, Paginated{Count: count, Result: result, IDsAbove: idsAbove, IDsBelow: idsBelow})
}

// Decoder helpers ---------------------------------------------------------

// DecodeJSON decodes a JSON request body into v.
func DecodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return apperrors.BadRequest("Empty request body")
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return apperrors.BadRequest("Invalid JSON body: %s", err.Error())
	}
	return nil
}

// Query helpers ------------------------------------------------------------

// QueryInt parses an integer query parameter with a default.
func QueryInt(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

// QueryBool parses a boolean query parameter with a default.
func QueryBool(r *http.Request, key string, def bool) bool {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	return strings.EqualFold(raw, "true") || raw == "1"
}

// QueryStr returns a string query parameter.
func QueryStr(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

// URLParam returns a chi URL parameter.
func URLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

// URLParamInt returns a chi URL parameter as int.
func URLParamInt(r *http.Request, key string) (int, error) {
	raw := chi.URLParam(r, key)
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, apperrors.BadRequest("Invalid id %q", raw)
	}
	return n, nil
}

// FilterOp constants for FAB-style list filters.
type FilterOp string

const (
	FilterEq         FilterOp = "eq"
	FilterNe         FilterOp = "ne"
	FilterGt         FilterOp = "gt"
	FilterGe         FilterOp = "ge"
	FilterLt         FilterOp = "lt"
	FilterLe         FilterOp = "le"
	FilterIn         FilterOp = "in"
	FilterNotIn      FilterOp = "not in"
	FilterLike       FilterOp = "ct"
	FilterNotLike    FilterOp = "nct"
	FilterStartsWith FilterOp = "sw"
)

// Filter is a FAB-style list filter {col, opr, value}.
type Filter struct {
	Col   string `json:"col"`
	Opr   string `json:"opr"`
	Value any    `json:"value"`
}

// ParseFilters extracts the filters=[...] query parameter.
func ParseFilters(r *http.Request) ([]Filter, error) {
	raw := r.URL.Query().Get("filters")
	if raw == "" {
		return nil, nil
	}
	var filters []Filter
	if err := json.Unmarshal([]byte(raw), &filters); err != nil {
		return nil, apperrors.BadRequest("Invalid filters parameter")
	}
	return filters, nil
}

// Health endpoints ----------------------------------------------------------

// RegisterHealth mounts /health, /healthz and /ping.
func (s *Server) RegisterHealth() {
	handler := func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{
			"status":  "healthy",
			"version": Version,
			"time":    time.Now().UTC().Format(time.RFC3339),
		})
	}
	s.Router.Get("/health", handler)
	s.Router.Get("/healthz", handler)
	s.Router.Get("/ping", handler)
}

// Listen starts the HTTP server with graceful shutdown.
func (s *Server) Listen(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.Log.Info("starting superset server", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// Err renders an error as a plain Go error for handler chaining.
func Err(err error) error { return err }

// Fmt is a tiny helper for formatted errors in handlers.
func Fmt(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// RegisterSPA serves the built React single-page application. During
// development Vite serves the assets and proxies /api to this server, so the
// handler only needs to exist for production builds.
func (s *Server) RegisterSPA() {
	s.Router.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		// API routes are matched before this catch-all; anything else is a
		// client-side route.
		WriteJSON(w, http.StatusNotFound, map[string]any{"message": "Not found"})
	})
}
