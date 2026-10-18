package security

import (
	"context"

	"superset/internal/models"
)

type ctxKey string

const (
	ctxUserKey  ctxKey = "superset.user"
	ctxPermKey  ctxKey = "superset.perms"
	ctxGuestKey ctxKey = "superset.guest"
)

// AuthUser is the authenticated principal attached to a request.
type AuthUser struct {
	ID        int
	Username  string
	FirstName string
	LastName  string
	Email     string
	Active    bool
	Roles     []string
	IsGuest   bool
	GuestRSL  []GuestRLSRule
	GuestRes  []GuestResource
}

// DisplayName mirrors FAB's get_full_name().
func (u *AuthUser) DisplayName() string {
	if u == nil {
		return ""
	}
	if u.FirstName == "" && u.LastName == "" {
		return u.Username
	}
	if u.FirstName == "" {
		return u.LastName
	}
	if u.LastName == "" {
		return u.FirstName
	}
	return u.FirstName + " " + u.LastName
}

// WithUser stores the authenticated user in the context.
func WithUser(ctx context.Context, u *AuthUser) context.Context {
	return context.WithValue(ctx, ctxUserKey, u)
}

// UserFrom returns the authenticated user, or nil for anonymous requests.
func UserFrom(ctx context.Context) *AuthUser {
	v, _ := ctx.Value(ctxUserKey).(*AuthUser)
	return v
}

// WithPermissions stores the resolved permission set in the context.
func WithPermissions(ctx context.Context, s PermissionSet) context.Context {
	return context.WithValue(ctx, ctxPermKey, s)
}

// PermissionsFrom returns the permission set from the context.
func PermissionsFrom(ctx context.Context) (PermissionSet, bool) {
	v, ok := ctx.Value(ctxPermKey).(PermissionSet)
	return v, ok
}

// WithGuest stores the guest token claims in the context.
func WithGuest(ctx context.Context, c *GuestClaims) context.Context {
	return context.WithValue(ctx, ctxGuestKey, c)
}

// GuestFrom returns guest claims when the request used a guest token.
func GuestFrom(ctx context.Context) (*GuestClaims, bool) {
	v, ok := ctx.Value(ctxGuestKey).(*GuestClaims)
	return v, ok
}

// ToModel converts a DB user into an AuthUser.
func ToModel(u *models.User, roles []string) *AuthUser {
	return &AuthUser{
		ID:        u.ID,
		Username:  u.Username,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		Email:     u.Email,
		Active:    u.Active,
		Roles:     roles,
	}
}
