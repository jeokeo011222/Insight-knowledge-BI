package security

import (
	"net/http"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"

	apperrors "superset/internal/pkg/errors"
	"superset/internal/server"

	"superset/internal/models"
)

// Authenticator resolves the caller of every request. It accepts, in order:
//  1. a guest token in the X-GuestToken header,
//  2. a bearer access token,
//  3. the Superset session cookie (access_token_cookie).
//
// Requests without credentials proceed as anonymous; handlers that need a
// user call RequireUser.
type Authenticator struct {
	DB          *gorm.DB
	Mgr         *Manager
	Log         *zap.Logger
	Secret      string
	CookieName  string
	GuestSecret string
	GuestHeader string
}

// NewAuthenticator builds an authenticator from the runtime config.
func NewAuthenticator(db *gorm.DB, mgr *Manager, log *zap.Logger, secret, guestSecret, guestHeader string) *Authenticator {
	if guestHeader == "" {
		guestHeader = "X-GuestToken"
	}
	return &Authenticator{
		DB:          db,
		Mgr:         mgr,
		Log:         log,
		Secret:      secret,
		CookieName:  "access_token_cookie",
		GuestSecret: guestSecret,
		GuestHeader: guestHeader,
	}
}

// Authenticate is the middleware that populates the request context.
func (a *Authenticator) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// 1. Guest token.
		if raw := r.Header.Get(a.GuestHeader); raw != "" && a.GuestSecret != "" {
			claims, err := ParseGuestToken(a.GuestSecret, raw, "")
			if err != nil {
				server.WriteError(w, apperrors.Unauthorized("Invalid guest token"))
				return
			}
			guest := &AuthUser{
				Username:  claims.User.Username,
				FirstName: claims.User.FirstName,
				LastName:  claims.User.LastName,
				Active:    true,
				IsGuest:   true,
				GuestRSL:  claims.RLS,
				GuestRes:  claims.Resources,
			}
			ctx = WithUser(ctx, guest)
			ctx = WithGuest(ctx, claims)
			ctx = WithPermissions(ctx, GuestPermissionSet(claims.Scope))
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 2. Bearer token.
		raw := bearerToken(r)
		fromCookie := false
		if raw == "" {
			if c, err := r.Cookie(a.CookieName); err == nil && c.Value != "" {
				raw = c.Value
				fromCookie = true
			}
		}
		if raw == "" {
			next.ServeHTTP(w, r)
			return
		}

		claims, err := ParseToken(a.Secret, raw, "")
		if err != nil {
			if !fromCookie {
				server.WriteError(w, apperrors.Unauthorized("Invalid token"))
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		user, err := a.loadUser(r, claims.UserID)
		if err != nil {
			if !fromCookie {
				server.WriteError(w, apperrors.Unauthorized("User not found"))
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		perms, err := a.Mgr.UserPermissions(ctx, user.ID)
		if err != nil {
			a.Log.Warn("resolve permissions", zap.Error(err))
			perms = PermissionSet{}
		}
		ctx = WithUser(ctx, user)
		ctx = WithPermissions(ctx, perms)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func (a *Authenticator) loadUser(r *http.Request, userID int) (*AuthUser, error) {
	var u models.User
	if err := a.DB.WithContext(r.Context()).First(&u, userID).Error; err != nil {
		return nil, err
	}
	if !u.Active {
		return nil, apperrors.Unauthorized("User is inactive")
	}
	var roles []string
	if err := a.DB.WithContext(r.Context()).Raw(`
		SELECT r.name FROM ab_role r JOIN ab_user_role ur ON ur.role_id = r.id WHERE ur.user_id = ?
		UNION
		SELECT r.name FROM ab_role r JOIN ab_group_role gr ON gr.role_id = r.id
		JOIN ab_user_group ug ON ug.group_id = gr.group_id WHERE ug.user_id = ?`, userID, userID).
		Scan(&roles).Error; err != nil {
		return nil, err
	}
	return ToModel(&u, roles), nil
}

// RequireUser rejects anonymous requests with 401.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()) == nil {
			server.WriteError(w, apperrors.Unauthorized("Unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePermission enforces "action on Resource" for the current user.
func RequirePermission(action, resource string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFrom(r.Context())
			if u == nil {
				server.WriteError(w, apperrors.Unauthorized("Unauthorized"))
				return
			}
			perms, _ := PermissionsFrom(r.Context())
			if perms.Admin() || perms.Has(action+" on "+resource) {
				next.ServeHTTP(w, r)
				return
			}
			server.WriteError(w, apperrors.Forbidden("You don't have the permission to access the requested resource."))
		})
	}
}

// HasPerm is the context-based permission check used inside handlers.
func HasPerm(r *http.Request, action, resource string) bool {
	perms, ok := PermissionsFrom(r.Context())
	if !ok {
		return false
	}
	return perms.Admin() || perms.Has(action+" on "+resource)
}
