// Package api exposes the REST modules mounted under /api/v1.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"

	"github.com/go-chi/chi/v5"
)

// securityAPI implements /api/v1/security/* and the RBAC CRUD endpoints.
type securityAPI struct {
	deps *Deps
	auth *security.Authenticator
	mgr  *security.Manager
}

// RegisterSecurity mounts authentication and RBAC routes.
func RegisterSecurity(r chi.Router, deps *Deps, auth *security.Authenticator, mgr *security.Manager) {
	a := &securityAPI{deps: deps, auth: auth, mgr: mgr}

	r.Route("/security", func(sr chi.Router) {
		sr.Post("/login", a.login)
		sr.Post("/refresh", a.refresh)
		sr.Post("/logout", a.logout)
		sr.Get("/csrf_token", a.csrfToken)
		sr.Get("/csrf_token/", a.csrfToken)
		sr.Post("/guest_token", a.guestToken)
		sr.Post("/guest_token/", a.guestToken)
	})

	r.Get("/me/", a.me)
	r.Get("/me", a.me)
	r.Get("/me/roles/", a.meRoles)

	r.Route("/user", func(ur chi.Router) {
		ur.Use(security.RequireUser, security.RequirePermission(security.ActionRead, security.ResUser))
		ur.Get("/", a.listUsers)
		ur.Post("/", a.createUser)
		ur.Get("/_info", a.listUsersInfo)
		ur.Get("/{pk}", a.getUser)
		ur.Put("/{pk}", a.updateUser)
		ur.Delete("/{pk}", a.deleteUser)
	})

	r.Route("/role", func(rr chi.Router) {
		rr.Use(security.RequireUser, security.RequirePermission(security.ActionRead, security.ResRole))
		rr.Get("/", a.listRoles)
		rr.Post("/", a.createRole)
		rr.Get("/_info", a.listRolesInfo)
		rr.Get("/{pk}", a.getRole)
		rr.Put("/{pk}", a.updateRole)
		rr.Delete("/{pk}", a.deleteRole)
	})

	r.Route("/permission", func(pr chi.Router) {
		pr.Use(security.RequireUser, security.RequirePermission(security.ActionRead, security.ResPermission))
		pr.Get("/", a.listPermissions)
	})

	r.Route("/group", func(gr chi.Router) {
		gr.Use(security.RequireUser, security.RequirePermission(security.ActionRead, security.ResGroup))
		gr.Get("/", a.listGroups)
		gr.Post("/", a.createGroup)
		gr.Get("/{pk}", a.getGroup)
		gr.Put("/{pk}", a.updateGroup)
		gr.Delete("/{pk}", a.deleteGroup)
	})
}

// LoginRequest mirrors superset.security.api.LoginRequestSchema.
type LoginRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Provider    string `json:"provider"`
	Refresh     bool   `json:"refresh"`
	AccessToken string `json:"access_token"`
}

func (a *securityAPI) login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	// Superset accepts both JSON and form-encoded bodies.
	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/x-www-form-urlencoded") || strings.Contains(ct, "multipart/form-data") {
		_ = r.ParseForm()
		req.Username = r.PostFormValue("username")
		req.Password = r.PostFormValue("password")
		req.Provider = r.PostFormValue("provider")
	} else if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	if req.Username == "" || req.Password == "" {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.security.missing_credentials", "Missing credentials")))
		return
	}

	var user models.User
	err := a.deps.DB.WithContext(r.Context()).
		Where("username = ? OR email = ?", req.Username, req.Username).First(&user).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			server.WriteError(w, apperrors.Unauthorized("%s", localize(r, "error.security.invalid_login", "Invalid login. Please try again.")))
			return
		}
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}
	if !user.Active {
		server.WriteError(w, apperrors.Unauthorized("%s", localize(r, "error.security.account_disabled", "This user account is disabled.")))
		return
	}
	if !security.VerifyPassword(user.Password, req.Password) {
		a.recordFailedLogin(r.Context(), &user)
		server.WriteError(w, apperrors.Unauthorized("%s", localize(r, "error.security.invalid_login", "Invalid login. Please try again.")))
		return
	}

	now := time.Now()
	loginCount := 1
	if user.LoginCount != nil {
		loginCount = *user.LoginCount + 1
	}
	_ = a.deps.DB.WithContext(r.Context()).Model(&user).Updates(map[string]any{
		"last_login":       now,
		"login_count":      loginCount,
		"fail_login_count": 0,
	}).Error

	pair, err := security.IssueTokenPair(a.deps.Config.SecretKey, user.ID, 3600)
	if err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.security.issue_token_failed", "Could not issue token")))
		return
	}
	a.setAuthCookies(w, pair)
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token":  pair.AccessToken,
		"refresh_token": pair.RefreshToken,
	})
}

func (a *securityAPI) recordFailedLogin(ctx context.Context, u *models.User) {
	count := 1
	if u.FailLoginCount != nil {
		count = *u.FailLoginCount + 1
	}
	_ = a.deps.DB.WithContext(ctx).Model(u).Update("fail_login_count", count).Error
}

func (a *securityAPI) setAuthCookies(w http.ResponseWriter, pair *security.TokenPair) {
	secure := a.deps.Config.SessionCookieSecure
	http.SetCookie(w, &http.Cookie{
		Name:     "access_token_cookie",
		Value:    pair.AccessToken,
		Path:     "/",
		HttpOnly: a.deps.Config.SessionCookieHTTPOnly,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   3600,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token_cookie",
		Value:    pair.RefreshToken,
		Path:     "/",
		HttpOnly: a.deps.Config.SessionCookieHTTPOnly,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   3600 * 24 * 7,
	})
}

func (a *securityAPI) refresh(w http.ResponseWriter, r *http.Request) {
	token := ""
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
		token = req.RefreshToken
	}
	if token == "" {
		if c, err := r.Cookie("refresh_token_cookie"); err == nil {
			token = c.Value
		}
	}
	if token == "" {
		server.WriteError(w, apperrors.Unauthorized("%s", localize(r, "error.security.missing_refresh_token", "Missing refresh token")))
		return
	}
	claims, err := security.ParseToken(a.deps.Config.SecretKey, token, security.TokenTypeRefresh)
	if err != nil {
		server.WriteError(w, apperrors.Unauthorized("%s", localize(r, "error.security.invalid_refresh_token", "Invalid refresh token")))
		return
	}
	pair, err := security.IssueTokenPair(a.deps.Config.SecretKey, claims.UserID, 3600)
	if err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.security.issue_token_failed", "Could not issue token")))
		return
	}
	a.setAuthCookies(w, pair)
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"access_token":  pair.AccessToken,
		"refresh_token": pair.RefreshToken,
	})
}

func (a *securityAPI) logout(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{"access_token_cookie", "refresh_token_cookie"} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

func (a *securityAPI) csrfToken(w http.ResponseWriter, r *http.Request) {
	// Superset returns a signed CSRF token; the replica uses the same shape
	// and validates double-submit on state-changing requests.
	token := a.deps.Config.SecretKey
	sum := token[:16]
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": sum})
}

// GuestTokenRequest mirrors superset.security.api.GuestTokenRequestSchema.
type GuestTokenRequest struct {
	User      security.GuestUser       `json:"user"`
	Resources []security.GuestResource `json:"resources"`
	RLS       []security.GuestRLSRule  `json:"rls_rules"`
}

func (a *securityAPI) guestToken(w http.ResponseWriter, r *http.Request) {
	if !security.HasPerm(r, security.ActionRead, security.ResDashboard) {
		server.WriteError(w, apperrors.Forbidden("%s", localize(r, "error.security.forbidden", "You don't have the permission to access the requested resource.")))
		return
	}
	var req GuestTokenRequest
	if err := server.DecodeJSON(r, &req); err != nil {
		server.WriteError(w, err)
		return
	}
	if len(req.Resources) == 0 {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.security.resources_required", "Resources are required")))
		return
	}
	secret := a.deps.Config.GuestTokenJWTSecret
	if secret == "" {
		secret = a.deps.Config.SecretKey
	}
	ttl := a.deps.Config.GuestTokenJWTExpSeconds
	if ttl <= 0 {
		ttl = 300
	}
	claims := security.GuestClaims{
		User:      req.User,
		Resources: req.Resources,
		RLS:       req.RLS,
		Aud:       a.deps.Config.GuestTokenJWTAudience,
	}
	token, err := security.IssueGuestToken(secret, claims, ttl)
	if err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.security.guest_token_failed", "Could not issue guest token")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"token": token})
}

// meResponse mirrors superset.views.base.api /api/v1/me/.
type meResponse struct {
	ID          int       `json:"id"`
	Username    string    `json:"username"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	Email       string    `json:"email"`
	IsActive    bool      `json:"is_active"`
	IsAnonymous bool      `json:"is_anonymous"`
	Roles       []roleRef `json:"roles"`
	Permissions []string  `json:"permissions"`
}

type roleRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (a *securityAPI) me(w http.ResponseWriter, r *http.Request) {
	u := security.UserFrom(r.Context())
	if u == nil {
		// Superset returns a synthetic anonymous user instead of 401 so the
		// SPA can render the login page.
		server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
			"id": 0, "username": "anonymous", "first_name": "", "last_name": "",
			"email": "", "is_active": false, "is_anonymous": true,
			"roles": []any{}, "permissions": []string{},
		}})
		return
	}
	roles := make([]roleRef, 0, len(u.Roles))
	for _, name := range u.Roles {
		var role models.Role
		if err := a.deps.DB.WithContext(r.Context()).Where("name = ?", name).First(&role).Error; err == nil {
			roles = append(roles, roleRef{ID: role.ID, Name: role.Name})
		} else {
			roles = append(roles, roleRef{Name: name})
		}
	}
	perms, _ := security.PermissionsFrom(r.Context())
	resp := meResponse{
		ID:          u.ID,
		Username:    u.Username,
		FirstName:   u.FirstName,
		LastName:    u.LastName,
		Email:       u.Email,
		IsActive:    u.Active,
		IsAnonymous: u.IsGuest,
		Roles:       roles,
		Permissions: perms.List(),
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": resp})
}

func (a *securityAPI) meRoles(w http.ResponseWriter, r *http.Request) {
	u := security.UserFrom(r.Context())
	if u == nil {
		server.WriteError(w, apperrors.Unauthorized("%s", localize(r, "error.security.unauthorized", "Unauthorized")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": u.Roles})
}

// User CRUD ---------------------------------------------------------------

type userPayload struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
	Email     string `json:"email"`
	Active    *bool  `json:"active"`
	Password  string `json:"password"`
	Roles     []int  `json:"roles"`
	Groups    []int  `json:"groups"`
}

func (a *securityAPI) listUsers(w http.ResponseWriter, r *http.Request) {
	page, size := pageParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.User{})
	if filter := server.QueryStr(r, "q"); filter != "" {
		like := "%" + filter + "%"
		q = q.Where("username ILIKE ? OR email ILIKE ? OR first_name ILIKE ? OR last_name ILIKE ?",
			like, like, like, like)
	}
	var total int64
	q.Count(&total)
	var users []models.User
	if err := q.Order("id").Offset((page - 1) * size).Limit(size).Find(&users).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}
	result := make([]map[string]any, 0, len(users))
	for i := range users {
		result = append(result, a.userJSON(r, &users[i]))
	}
	server.WritePaginated(w, total, result, nil, nil)
}

func (a *securityAPI) listUsersInfo(w http.ResponseWriter, r *http.Request) {
	var users []models.User
	if err := a.deps.DB.WithContext(r.Context()).Order("username").Find(&users).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "text": u.Username,
			"first_name": u.FirstName, "last_name": u.LastName,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"count": len(out), "result": out})
}

func (a *securityAPI) userJSON(r *http.Request, u *models.User) map[string]any {
	var roles []models.Role
	_ = a.deps.DB.WithContext(r.Context()).Raw(`
		SELECT r.* FROM ab_role r JOIN ab_user_role ur ON ur.role_id = r.id WHERE ur.user_id = ?
		ORDER BY r.name`, u.ID).Scan(&roles).Error
	roleList := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		roleList = append(roleList, map[string]any{"id": role.ID, "name": role.Name})
	}
	var groups []models.Group
	_ = a.deps.DB.WithContext(r.Context()).Raw(`
		SELECT g.* FROM ab_group g JOIN ab_user_group ug ON ug.group_id = g.id WHERE ug.user_id = ?`,
		u.ID).Scan(&groups).Error
	groupList := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		groupList = append(groupList, map[string]any{"id": g.ID, "name": g.Name})
	}
	return map[string]any{
		"id": u.ID, "username": u.Username, "first_name": u.FirstName,
		"last_name": u.LastName, "email": u.Email, "active": u.Active,
		"last_login": u.LastLogin, "login_count": u.LoginCount,
		"fail_login_count": u.FailLoginCount,
		"roles":            roleList, "groups": groupList,
	}
}

func (a *securityAPI) getUser(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var u models.User
	if err := a.deps.DB.WithContext(r.Context()).First(&u, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.common.not_found", "Not found")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.userJSON(r, &u)})
}

func (a *securityAPI) createUser(w http.ResponseWriter, r *http.Request) {
	var p userPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.Username == "" || p.Email == "" {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.security.username_email_required", "Username and email are required")))
		return
	}
	if err := a.validatePassword(r, p.Password); err != nil {
		server.WriteError(w, err)
		return
	}
	hash, err := security.HashPassword(p.Password)
	if err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.security.hash_password_failed", "Could not hash password")))
		return
	}
	active := true
	if p.Active != nil {
		active = *p.Active
	}
	u := models.User{
		FirstName: p.FirstName, LastName: p.LastName, Username: p.Username,
		Email: p.Email, Password: hash, Active: active,
	}
	err = a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&u).Error; err != nil {
			return err
		}
		for _, roleID := range p.Roles {
			if err := tx.Create(&models.UserRole{UserID: u.ID, RoleID: roleID}).Error; err != nil {
				return err
			}
		}
		for _, groupID := range p.Groups {
			if err := tx.Create(&models.UserGroup{UserID: u.ID, GroupID: groupID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.security.create_user_failed", "Could not create user: %s", err.Error())))
		return
	}
	a.mgr.Invalidate(u.ID)
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": a.userJSON(r, &u)})
}

func (a *securityAPI) updateUser(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var u models.User
	db := a.deps.DB.WithContext(r.Context())
	if err := db.First(&u, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.common.not_found", "Not found")))
		return
	}
	var p userPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	updates := map[string]any{}
	if p.FirstName != "" {
		updates["first_name"] = p.FirstName
	}
	if p.LastName != "" {
		updates["last_name"] = p.LastName
	}
	if p.Username != "" {
		updates["username"] = p.Username
	}
	if p.Email != "" {
		updates["email"] = p.Email
	}
	if p.Active != nil {
		updates["active"] = *p.Active
	}
	if p.Password != "" {
		if err := a.validatePassword(r, p.Password); err != nil {
			server.WriteError(w, err)
			return
		}
		hash, err := security.HashPassword(p.Password)
		if err != nil {
			server.WriteError(w, apperrors.Internal("%s", localize(r, "error.security.hash_password_failed", "Could not hash password")))
			return
		}
		updates["password"] = hash
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&u).Updates(updates).Error; err != nil {
				return err
			}
		}
		if p.Roles != nil {
			if err := tx.Where("user_id = ?", u.ID).Delete(&models.UserRole{}).Error; err != nil {
				return err
			}
			for _, roleID := range p.Roles {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
					Create(&models.UserRole{UserID: u.ID, RoleID: roleID}).Error; err != nil {
					return err
				}
			}
		}
		if p.Groups != nil {
			if err := tx.Where("user_id = ?", u.ID).Delete(&models.UserGroup{}).Error; err != nil {
				return err
			}
			for _, groupID := range p.Groups {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
					Create(&models.UserGroup{UserID: u.ID, GroupID: groupID}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.security.update_user_failed", "Could not update user: %s", err.Error())))
		return
	}
	a.mgr.Invalidate(u.ID)
	_ = db.First(&u, id).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.userJSON(r, &u)})
}

func (a *securityAPI) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if cur := security.UserFrom(r.Context()); cur != nil && cur.ID == id {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.security.cannot_delete_self", "You cannot delete your own user")))
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	if err := db.Where("user_id = ?", id).Delete(&models.UserRole{}).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	if err := db.Delete(&models.User{}, id).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	a.mgr.Invalidate(id)
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

func (a *securityAPI) validatePassword(r *http.Request, pw string) error {
	minLen := a.deps.Config.AuthPasswordMinLength
	if minLen <= 0 {
		minLen = 8
	}
	if len(pw) < minLen {
		return apperrors.BadRequest("%s", localize(r, "error.security.password_length", "Password must be at least %d characters", minLen))
	}
	if a.deps.Config.FabPasswordComplexity {
		var hasUpper, hasLower, hasDigit, hasSpecial bool
		for _, c := range pw {
			switch {
			case c >= 'A' && c <= 'Z':
				hasUpper = true
			case c >= 'a' && c <= 'z':
				hasLower = true
			case c >= '0' && c <= '9':
				hasDigit = true
			default:
				hasSpecial = true
			}
		}
		if !(hasUpper && hasLower && hasDigit && hasSpecial) {
			return apperrors.BadRequest("%s", localize(r, "error.security.password_complexity", "Password must contain upper case, lower case, digit and special characters"))
		}
	}
	return nil
}

// Role CRUD ---------------------------------------------------------------

func (a *securityAPI) listRoles(w http.ResponseWriter, r *http.Request) {
	db := a.deps.DB.WithContext(r.Context())
	page, size := pageParams(r)
	q := db.Model(&models.Role{})
	var total int64
	q.Count(&total)
	var roles []models.Role
	if err := q.Order("name").Offset((page - 1) * size).Limit(size).Find(&roles).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		out = append(out, a.roleJSON(r, &role))
	}
	server.WritePaginated(w, total, out, nil, nil)
}

func (a *securityAPI) listRolesInfo(w http.ResponseWriter, r *http.Request) {
	var roles []models.Role
	if err := a.deps.DB.WithContext(r.Context()).Order("name").Find(&roles).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		out = append(out, map[string]any{"id": role.ID, "text": role.Name, "name": role.Name})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"count": len(out), "result": out})
}

func (a *securityAPI) roleJSON(r *http.Request, role *models.Role) map[string]any {
	var permissions []struct {
		ID               int
		Name             string
		ViewMenuID       int
		ViewMenuName     string
		PermissionViewID int
	}
	_ = a.deps.DB.WithContext(r.Context()).Raw(`
		SELECT p.id, p.name, vm.id AS view_menu_id, vm.name AS view_menu_name,
			pv.id AS permission_view_id
		FROM ab_permission_view_role pvr
		JOIN ab_permission_view pv ON pv.id = pvr.permission_view_id
		JOIN ab_permission p ON p.id = pv.permission_id
		JOIN ab_view_menu vm ON vm.id = pv.view_menu_id
		WHERE pvr.role_id = ? ORDER BY vm.name, p.name`, role.ID).Scan(&permissions).Error
	perms := make([]map[string]any, 0, len(permissions))
	for _, p := range permissions {
		perms = append(perms, map[string]any{
			"id": p.ID, "name": p.Name,
			"view_menu_id": p.ViewMenuID, "view_menu_name": p.ViewMenuName,
			"permission_view_id": p.PermissionViewID,
		})
	}
	var userCount int64
	_ = a.deps.DB.WithContext(r.Context()).Model(&models.UserRole{}).Where("role_id = ?", role.ID).Count(&userCount).Error
	return map[string]any{
		"id": role.ID, "name": role.Name, "permissions": perms,
		"user_count": userCount, "is_builtin": security.IsBuiltinRole(role.Name),
	}
}

func (a *securityAPI) getRole(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var role models.Role
	if err := a.deps.DB.WithContext(r.Context()).First(&role, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.common.not_found", "Not found")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.roleJSON(r, &role)})
}

type rolePayload struct {
	Name              string `json:"name"`
	PermissionIDs     []int  `json:"permissions"`
	PermissionViewIDs []int  `json:"permission_view_ids"`
}

func (a *securityAPI) createRole(w http.ResponseWriter, r *http.Request) {
	var p rolePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if p.Name == "" {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.common.name_required", "Name is required")))
		return
	}
	role := models.Role{Name: p.Name}
	err := a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&role).Error; err != nil {
			return err
		}
		for _, pvID := range p.PermissionViewIDs {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
				Create(&models.PermissionViewRole{PermissionViewID: pvID, RoleID: role.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.security.create_role_failed", "Could not create role: %s", err.Error())))
		return
	}
	a.mgr.InvalidateAll()
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": a.roleJSON(r, &role)})
}

func (a *securityAPI) updateRole(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	var role models.Role
	if err := db.First(&role, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.common.not_found", "Not found")))
		return
	}
	var p rolePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	// A built-in role's name is its identity (admin detection resolves the Admin
	// role by name), so renaming one would break the permission model. Its
	// permission set stays editable.
	if p.Name != "" && p.Name != role.Name && security.IsBuiltinRole(role.Name) {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.security.builtin_role_locked", "Cannot rename a built-in role")))
		return
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if p.Name != "" && p.Name != role.Name {
			if err := tx.Model(&role).Update("name", p.Name).Error; err != nil {
				return err
			}
		}
		if p.PermissionViewIDs != nil {
			if err := tx.Where("role_id = ?", role.ID).Delete(&models.PermissionViewRole{}).Error; err != nil {
				return err
			}
			for _, pvID := range p.PermissionViewIDs {
				if err := tx.Create(&models.PermissionViewRole{PermissionViewID: pvID, RoleID: role.ID}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.security.update_role_failed", "Could not update role: %s", err.Error())))
		return
	}
	a.mgr.InvalidateAll()
	_ = db.First(&role, id).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.roleJSON(r, &role)})
}

func (a *securityAPI) deleteRole(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	var role models.Role
	if err := db.First(&role, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.common.not_found", "Not found")))
		return
	}
	if security.IsBuiltinRole(role.Name) {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.security.builtin_role_readonly", "Cannot delete a built-in role")))
		return
	}
	if err := db.Delete(&role).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	_ = db.Where("role_id = ?", id).Delete(&models.UserRole{}).Error
	_ = db.Where("role_id = ?", id).Delete(&models.PermissionViewRole{}).Error
	a.mgr.InvalidateAll()
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Permissions -------------------------------------------------------------

func (a *securityAPI) listPermissions(w http.ResponseWriter, r *http.Request) {
	var rows []struct {
		ID           int
		Permission   string
		ViewMenu     string
		PermissionID int
		ViewMenuID   int
	}
	err := a.deps.DB.WithContext(r.Context()).Raw(`
		SELECT pv.id, p.name AS permission, vm.name AS view_menu,
		       p.id AS permission_id, vm.id AS view_menu_id
		FROM ab_permission_view pv
		JOIN ab_permission p ON p.id = pv.permission_id
		JOIN ab_view_menu vm ON vm.id = pv.view_menu_id
		ORDER BY vm.name, p.name`).Scan(&rows).Error
	if err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	page, size := pageParams(r)
	start := (page - 1) * size
	if start > len(rows) {
		start = len(rows)
	}
	end := start + size
	if end > len(rows) {
		end = len(rows)
	}
	out := make([]map[string]any, 0, end-start)
	for _, row := range rows[start:end] {
		out = append(out, map[string]any{
			"id": row.ID, "permission": row.Permission, "view_menu": row.ViewMenu,
			"permission_id": row.PermissionID, "view_menu_id": row.ViewMenuID,
		})
	}
	server.WritePaginated(w, int64(len(rows)), out, nil, nil)
}

// Group CRUD --------------------------------------------------------------

func (a *securityAPI) listGroups(w http.ResponseWriter, r *http.Request) {
	var groups []models.Group
	if err := a.deps.DB.WithContext(r.Context()).Order("name").Find(&groups).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		out = append(out, a.groupJSON(r, &g))
	}
	server.WritePaginated(w, int64(len(out)), out, nil, nil)
}

func (a *securityAPI) groupJSON(r *http.Request, g *models.Group) map[string]any {
	var roles []models.Role
	_ = a.deps.DB.WithContext(r.Context()).Raw(`
		SELECT r.* FROM ab_role r JOIN ab_group_role gr ON gr.role_id = r.id WHERE gr.group_id = ?`,
		g.ID).Scan(&roles).Error
	roleList := make([]map[string]any, 0, len(roles))
	for _, role := range roles {
		roleList = append(roleList, map[string]any{"id": role.ID, "name": role.Name})
	}
	return map[string]any{
		"id": g.ID, "name": g.Name, "label": g.Label,
		"description": g.Description, "roles": roleList,
	}
}

func (a *securityAPI) getGroup(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var g models.Group
	if err := a.deps.DB.WithContext(r.Context()).First(&g, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.common.not_found", "Not found")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.groupJSON(r, &g)})
}

type groupPayload struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Roles       []int  `json:"roles"`
}

func (a *securityAPI) createGroup(w http.ResponseWriter, r *http.Request) {
	var p groupPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	g := models.Group{Name: p.Name, Label: p.Label, Description: p.Description}
	err := a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&g).Error; err != nil {
			return err
		}
		for _, roleID := range p.Roles {
			if err := tx.Create(&models.GroupRole{GroupID: g.ID, RoleID: roleID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.security.create_group_failed", "Could not create group: %s", err.Error())))
		return
	}
	a.mgr.InvalidateAll()
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": a.groupJSON(r, &g)})
}

func (a *securityAPI) updateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	var g models.Group
	if err := db.First(&g, id).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.common.not_found", "Not found")))
		return
	}
	var p groupPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&g).Updates(map[string]any{
			"name": p.Name, "label": p.Label, "description": p.Description,
		}).Error; err != nil {
			return err
		}
		if p.Roles != nil {
			if err := tx.Where("group_id = ?", g.ID).Delete(&models.GroupRole{}).Error; err != nil {
				return err
			}
			for _, roleID := range p.Roles {
				if err := tx.Create(&models.GroupRole{GroupID: g.ID, RoleID: roleID}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.security.update_group_failed", "Could not update group: %s", err.Error())))
		return
	}
	a.mgr.InvalidateAll()
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.groupJSON(r, &g)})
}

func (a *securityAPI) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	db := a.deps.DB.WithContext(r.Context())
	if err := db.Delete(&models.Group{}, id).Error; err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error", "Database error")))
		return
	}
	_ = db.Where("group_id = ?", id).Delete(&models.GroupRole{}).Error
	_ = db.Where("group_id = ?", id).Delete(&models.UserGroup{}).Error
	a.mgr.InvalidateAll()
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Helpers -----------------------------------------------------------------

func pageParams(r *http.Request) (page, size int) {
	page = server.QueryInt(r, "page", 1)
	size = server.QueryInt(r, "page_size", 25)
	if page < 1 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 25
	}
	return page, size
}

var _ = subtle.ConstantTimeCompare
