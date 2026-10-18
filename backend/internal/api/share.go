package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// Public share links for dashboards.
//
// The share row lives in `dashboard_share` (see models.DashboardShare) and is
// deliberately separate from Dashboard so neither the token nor the password
// hash can leak through the regular dashboard payloads.
//
// The anonymous viewer never calls the dashboard APIs with a session: unlock
// mints a short-lived *guest token* scoped to ScopePublic, and the SPA then
// renders the board through the normal read-only endpoints. That reuses the
// whole rendering stack (layouts, native filters, chart data) instead of
// duplicating a second, divergent render path.

// publicShareTTL bounds how long an unlock stays valid.
const publicShareTTL = 2 * time.Hour

// RegisterPublicShares mounts the share management endpoints plus the
// anonymous viewer endpoints.
func RegisterPublicShares(r chi.Router, deps *Deps) {
	a := &shareAPI{deps: deps}

	// Concrete patterns (not Route) so the /dashboard subrouter owned by
	// RegisterDashboards keeps working.
	r.Group(func(gr chi.Router) {
		gr.Use(security.RequireUser)
		gr.With(security.RequirePermission(security.ActionRead, security.ResDashboard)).
			Get("/dashboard/{pk}/share", a.get)
		gr.With(security.RequirePermission(security.ActionWrite, security.ResDashboard)).
			Post("/dashboard/{pk}/share", a.save)
		gr.With(security.RequirePermission(security.ActionWrite, security.ResDashboard)).
			Delete("/dashboard/{pk}/share", a.revoke)
	})

	// Anonymous surface. No RequireUser: the token *is* the credential.
	r.Get("/public/dashboard/{token}", a.publicGet)
	r.Post("/public/dashboard/{token}/unlock", a.publicUnlock)
}

type shareAPI struct{ deps *Deps }

type sharePayload struct {
	Enabled        *bool  `json:"enabled"`
	Password       string `json:"password"`
	ClearPassword  bool   `json:"clear_password"`
	Regenerate     bool   `json:"regenerate"`
	ExpiresInHours *int   `json:"expires_in_hours"`
	AllowExport    *bool  `json:"allow_export"`
}

// shareJSON renders the owner-facing view of a share row.
func shareJSON(s *models.DashboardShare) map[string]any {
	if s == nil {
		return map[string]any{
			"enabled": false, "has_password": false, "allow_export": true,
			"view_count": 0, "token": "", "url": "", "embed_url": "",
		}
	}
	out := map[string]any{
		"dashboard_id": s.DashboardID,
		"enabled":      s.Enabled,
		"has_password": s.HasPassword(),
		"allow_export": s.AllowExport,
		"view_count":   s.ViewCount,
		"expires_at":   s.ExpiresAt,
		"created_on":   s.CreatedOn,
		"changed_on":   s.ChangedOn,
		"expired":      s.Expired(),
		"token":        "",
		"url":          "",
		"embed_url":    "",
	}
	// The token is only echoed while the link is actually live.
	if s.Active() {
		out["token"] = s.Token
		out["url"] = "/dashboard/public/" + s.Token
		out["embed_url"] = "/dashboard/public/" + s.Token + "?embed=1"
	}
	return out
}

// findShare loads the dashboard and its share row (nil when never shared).
func (a *shareAPI) findShare(r *http.Request) (*models.Dashboard, *models.DashboardShare, error) {
	pk, err := strconv.Atoi(chi.URLParam(r, "pk"))
	if err != nil || pk <= 0 {
		return nil, nil, apperrors.NotFound("Dashboard not found")
	}
	var dm models.Dashboard
	if err := a.deps.DB.WithContext(r.Context()).First(&dm, pk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, apperrors.NotFound("Dashboard not found")
		}
		return nil, nil, apperrors.Internal("Database error: %s", err.Error())
	}
	var share models.DashboardShare
	err = a.deps.DB.WithContext(r.Context()).
		Where("dashboard_id = ?", dm.ID).First(&share).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &dm, nil, nil
	}
	if err != nil {
		return nil, nil, apperrors.Internal("Database error: %s", err.Error())
	}
	return &dm, &share, nil
}

func (a *shareAPI) get(w http.ResponseWriter, r *http.Request) {
	_, share, err := a.findShare(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": shareJSON(share)})
}

func (a *shareAPI) save(w http.ResponseWriter, r *http.Request) {
	dm, share, err := a.findShare(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p sharePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if share == nil {
		share = &models.DashboardShare{DashboardID: dm.ID, AllowExport: true}
	}
	if p.Enabled != nil {
		share.Enabled = *p.Enabled
	}
	if p.AllowExport != nil {
		share.AllowExport = *p.AllowExport
	}
	if p.ExpiresInHours != nil {
		switch hours := *p.ExpiresInHours; {
		case hours > 0:
			exp := time.Now().Add(time.Duration(hours) * time.Hour)
			share.ExpiresAt = &exp
		default:
			share.ExpiresAt = nil
		}
	}
	switch {
	case p.ClearPassword:
		share.PasswordHash = ""
	case strings.TrimSpace(p.Password) != "":
		hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
		if err != nil {
			server.WriteError(w, apperrors.Internal("Could not hash the password"))
			return
		}
		share.PasswordHash = string(hash)
	}
	// A live link always owns a token; regeneration invalidates the old URL.
	if share.Enabled && (share.Token == "" || p.Regenerate) {
		token, err := newShareToken()
		if err != nil {
			server.WriteError(w, apperrors.Internal("Could not create a share token"))
			return
		}
		share.Token = token
		share.ViewCount = 0
	}
	if err := a.deps.DB.WithContext(r.Context()).Save(share).Error; err != nil {
		server.WriteError(w, apperrors.Conflict("Could not save sharing settings: %s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": shareJSON(share)})
}

func (a *shareAPI) revoke(w http.ResponseWriter, r *http.Request) {
	_, share, err := a.findShare(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if share != nil {
		share.Enabled = false
		share.Token = ""
		share.PasswordHash = ""
		share.ExpiresAt = nil
		if err := a.deps.DB.WithContext(r.Context()).Save(share).Error; err != nil {
			server.WriteError(w, apperrors.Conflict("Could not revoke sharing: %s", err.Error()))
			return
		}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": shareJSON(nil)})
}

// newShareToken returns a 24-byte URL-safe random token.
func newShareToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// lookupPublic resolves a public token into its dashboard and share row.
func (a *shareAPI) lookupPublic(r *http.Request) (*models.Dashboard, *models.DashboardShare, error) {
	token := strings.TrimSpace(chi.URLParam(r, "token"))
	if token == "" {
		return nil, nil, apperrors.NotFound("Share link not found")
	}
	var share models.DashboardShare
	if err := a.deps.DB.WithContext(r.Context()).
		Where("token = ?", token).First(&share).Error; err != nil {
		// Same message for "unknown" and "revoked" so the endpoint cannot be
		// used to probe which tokens ever existed.
		return nil, nil, apperrors.NotFound("Share link not found")
	}
	if !share.Active() {
		return nil, nil, apperrors.NotFound("Share link not found")
	}
	var dm models.Dashboard
	if err := a.deps.DB.WithContext(r.Context()).First(&dm, share.DashboardID).Error; err != nil {
		return nil, nil, apperrors.NotFound("Share link not found")
	}
	return &dm, &share, nil
}

// publicMeta is the metadata a locked link is allowed to disclose.
func publicMeta(dm *models.Dashboard, share *models.DashboardShare) map[string]any {
	return map[string]any{
		"dashboard_id": dm.ID,
		"title":        dm.DashboardTitle,
		"slug":         dm.Slug,
		"description":  dm.Description,
		"allow_export": share.AllowExport,
		"expires_at":   share.ExpiresAt,
	}
}

// publicGet answers GET /public/dashboard/{token}.
//
// Without a password the response carries a freshly minted guest token, so the
// SPA can render immediately. With a password only the metadata is returned —
// the config never leaves the server before the gate is passed.
func (a *shareAPI) publicGet(w http.ResponseWriter, r *http.Request) {
	dm, share, err := a.lookupPublic(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if share.HasPassword() {
		payload := publicMeta(dm, share)
		payload["requires_password"] = true
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": payload})
		return
	}
	a.grant(w, r, dm, share)
}

// publicUnlock answers POST /public/dashboard/{token}/unlock.
func (a *shareAPI) publicUnlock(w http.ResponseWriter, r *http.Request) {
	dm, share, err := a.lookupPublic(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if !share.HasPassword() {
		a.grant(w, r, dm, share)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := security.DecodeBody(r, &body); err != nil {
		server.WriteError(w, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(share.PasswordHash), []byte(body.Password)) != nil {
		server.WriteError(w, apperrors.Forbidden("Incorrect password"))
		return
	}
	a.grant(w, r, dm, share)
}

// grant mints the scoped guest token and bumps the view counter.
func (a *shareAPI) grant(w http.ResponseWriter, r *http.Request, dm *models.Dashboard, share *models.DashboardShare) {
	secret := a.deps.Config.GuestTokenJWTSecret
	if secret == "" {
		secret = a.deps.Config.SecretKey
	}
	if secret == "" {
		server.WriteError(w, apperrors.Internal("Guest tokens are not configured"))
		return
	}
	ttl := publicShareTTL
	if share.ExpiresAt != nil {
		if left := time.Until(*share.ExpiresAt); left < ttl {
			ttl = left
		}
	}
	if ttl < time.Minute {
		server.WriteError(w, apperrors.NotFound("Share link not found"))
		return
	}
	claims := security.GuestClaims{
		User: security.GuestUser{
			Username:  "public-viewer",
			FirstName: "Public",
			LastName:  "Viewer",
		},
		Resources: []security.GuestResource{
			{Type: "dashboard", ID: strconv.Itoa(dm.ID)},
		},
		Scope: security.ScopePublic,
	}
	token, err := security.IssueGuestToken(secret, claims, int(ttl.Seconds()))
	if err != nil {
		server.WriteError(w, apperrors.Internal("Could not issue a viewer token"))
		return
	}
	// Best effort: a failed counter update must not block the viewer.
	_ = a.deps.DB.WithContext(r.Context()).Model(&models.DashboardShare{}).
		Where("id = ?", share.ID).
		UpdateColumn("view_count", gorm.Expr("view_count + 1")).Error

	payload := publicMeta(dm, share)
	payload["requires_password"] = false
	payload["guest_token"] = token
	payload["expires_in"] = int(ttl.Seconds())
	payload["can_export"] = share.AllowExport
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": payload})
}
