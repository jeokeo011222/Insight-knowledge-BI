package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterEmbedded mounts the embedded dashboard endpoints
// (superset/embedded/api.py). Guest tokens are issued by RegisterSecurity.
func RegisterEmbedded(r chi.Router, deps *Deps) {
	e := &embeddedAPI{deps: deps}

	// Registered as concrete patterns (not Route) so the /dashboard subrouter
	// owned by RegisterDashboards is not shadowed.
	r.Group(func(gr chi.Router) {
		gr.Use(security.RequireUser)

		gr.With(security.RequirePermission(security.ActionWrite, security.ResDashboard)).
			Post("/dashboard/{pk}/embedded", e.create)
		gr.With(security.RequirePermission(security.ActionRead, security.ResDashboard)).
			Get("/dashboard/{pk}/embedded", e.get)
		gr.With(security.RequirePermission(security.ActionDelete, security.ResDashboard)).
			Delete("/dashboard/{pk}/embedded", e.remove)

		gr.With(security.RequirePermission(security.ActionRead, security.ResEmbeddedDashboard)).
			Get("/embedded/{uuid}", e.getByUUID)
	})
}

type embeddedAPI struct{ deps *Deps }

type embeddedPayload struct {
	AllowedDomains  []string `json:"allowed_domains"`
	AllowDomainList string   `json:"allow_domain_list"`
}

// embeddedJSON renders an embedded dashboard row.
func embeddedJSON(e *models.EmbeddedDashboard) map[string]any {
	domains := embeddedAllowedList(e.AllowDomainList)
	return map[string]any{
		"uuid": e.UUID, "dashboard_id": e.DashboardID,
		"allowed_domains": domains, "allow_domain_list": e.AllowDomainList,
		"guest_token_revoked_before": e.GuestTokenRevokedBefore,
		"created_on":                 e.CreatedOn, "changed_on": e.ChangedOn,
	}
}

func embeddedAllowedList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// AllowedDomainsCheck reports whether origin matches one of the configured
// domains. "*" allows everything and "*.example.com" matches subdomains.
func AllowedDomainsCheck(origin string, allowed []string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	host, hostname := embeddedOriginHost(origin)
	for _, entry := range allowed {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		if strings.EqualFold(entry, origin) {
			return true
		}
		entryHost, entryName := embeddedOriginHost(entry)
		if entryHost != "" && strings.EqualFold(entryHost, host) {
			return true
		}
		wildcard := entry
		if strings.HasPrefix(entryHost, "*.") {
			wildcard = entryHost
		}
		if strings.HasPrefix(wildcard, "*.") {
			suffix := strings.ToLower(wildcard[1:]) // ".example.com"
			if host != "" && strings.HasSuffix(strings.ToLower(host), suffix) {
				return true
			}
			if hostname != "" && strings.HasSuffix(strings.ToLower(hostname), suffix) {
				return true
			}
			if entryName != "" && strings.EqualFold(entryName, hostname) {
				return true
			}
		}
	}
	return false
}

// embeddedOriginHost splits an origin or URL into host[:port] and hostname.
func embeddedOriginHost(raw string) (host, hostname string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			return u.Host, u.Hostname()
		}
	}
	if strings.Contains(raw, "/") {
		if u, err := url.Parse("//" + raw); err == nil && u.Host != "" {
			return u.Host, u.Hostname()
		}
	}
	return raw, raw
}

func (e *embeddedAPI) findDashboard(r *http.Request) (*models.Dashboard, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var d models.Dashboard
	if err := e.deps.DB.WithContext(r.Context()).First(&d, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("Dashboard not found")
		}
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &d, nil
}

func (e *embeddedAPI) findByDashboard(r *http.Request, dashboardID int) (*models.EmbeddedDashboard, error) {
	var row models.EmbeddedDashboard
	err := e.deps.DB.WithContext(r.Context()).Where("dashboard_id = ?", dashboardID).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &row, nil
}

func (e *embeddedAPI) create(w http.ResponseWriter, r *http.Request) {
	d, err := e.findDashboard(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p embeddedPayload
	_ = security.DecodeBody(r, &p)
	domains := p.AllowedDomains
	if len(domains) == 0 && p.AllowDomainList != "" {
		domains = embeddedAllowedList(p.AllowDomainList)
	}

	row, err := e.findByDashboard(r, d.ID)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if row != nil {
		if len(domains) > 0 {
			updates := map[string]any{
				"allow_domain_list": strings.Join(domains, ","),
				"changed_on":        nowPtr(),
				"changed_by_fk":     intPtrNil(userIDOf(r)),
			}
			if err := e.deps.DB.WithContext(r.Context()).Model(row).Updates(updates).Error; err != nil {
				server.WriteError(w, apperrors.Conflict("Could not update embedded dashboard: %s", err.Error()))
				return
			}
			_ = e.deps.DB.WithContext(r.Context()).First(row, "uuid = ?", row.UUID).Error
		}
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": embeddedJSON(row)})
		return
	}

	now := time.Now().UTC()
	row = &models.EmbeddedDashboard{
		DashboardID:     d.ID,
		AllowDomainList: strings.Join(domains, ","),
		CreatedOn:       &now,
		ChangedOn:       &now,
		CreatedByFk:     intPtrNil(userIDOf(r)),
		ChangedByFk:     intPtrNil(userIDOf(r)),
	}
	if err := e.deps.DB.WithContext(r.Context()).Create(row).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not create embedded dashboard: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": embeddedJSON(row)})
}

func (e *embeddedAPI) get(w http.ResponseWriter, r *http.Request) {
	d, err := e.findDashboard(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	row, err := e.findByDashboard(r, d.ID)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if row == nil {
		server.WriteError(w, apperrors.NotFound("Dashboard is not embedded"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": embeddedJSON(row)})
}

func (e *embeddedAPI) remove(w http.ResponseWriter, r *http.Request) {
	d, err := e.findDashboard(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := e.deps.DB.WithContext(r.Context()).
		Where("dashboard_id = ?", d.ID).Delete(&models.EmbeddedDashboard{}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("Database error: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

func (e *embeddedAPI) getByUUID(w http.ResponseWriter, r *http.Request) {
	id := server.URLParam(r, "uuid")
	if id == "" {
		server.WriteError(w, apperrors.BadRequest("uuid is required"))
		return
	}
	var row models.EmbeddedDashboard
	if err := e.deps.DB.WithContext(r.Context()).First(&row, "uuid = ?", id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("Embedded dashboard not found"))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": embeddedJSON(&row)})
}
