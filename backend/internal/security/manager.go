package security

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"superset/internal/models"
)

// PermissionSet is the resolved set of permission strings for a user.
type PermissionSet map[string]struct{}

// Has reports whether the set contains the permission.
func (s PermissionSet) Has(perm string) bool {
	_, ok := s[perm]
	return ok
}

// HasAny reports whether any of the given permission strings is present.
func (s PermissionSet) HasAny(perms ...string) bool {
	for _, p := range perms {
		if _, ok := s[p]; ok {
			return true
		}
	}
	return false
}

// Admin reports whether the user holds the Admin role.
func (s PermissionSet) Admin() bool { return s.Has("* on *") }

// List returns the permission strings sorted for stable API output.
func (s PermissionSet) List() []string {
	out := make([]string, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	return out
}

// Manager resolves permissions and performs object-level access checks.
type Manager struct {
	db  *gorm.DB
	log *zap.Logger

	mu    sync.RWMutex
	cache map[int]cachedPerms
}

type cachedPerms struct {
	set     PermissionSet
	expires time.Time
}

// NewManager builds a permission manager.
func NewManager(db *gorm.DB, log *zap.Logger) *Manager {
	return &Manager{db: db, log: log, cache: map[int]cachedPerms{}}
}

// Invalidate drops the cached permission set of a user.
func (m *Manager) Invalidate(userID int) {
	m.mu.Lock()
	delete(m.cache, userID)
	m.mu.Unlock()
}

// InvalidateAll drops every cached permission set (after a role change).
func (m *Manager) InvalidateAll() {
	m.mu.Lock()
	m.cache = map[int]cachedPerms{}
	m.mu.Unlock()
}

// UserPermissions resolves "action on Resource" strings for a user.
func (m *Manager) UserPermissions(ctx context.Context, userID int) (PermissionSet, error) {
	m.mu.RLock()
	if c, ok := m.cache[userID]; ok && time.Now().Before(c.expires) {
		m.mu.RUnlock()
		return c.set, nil
	}
	m.mu.RUnlock()

	set := PermissionSet{}
	var adminCount int64
	err := m.db.WithContext(ctx).Raw(`
		SELECT COUNT(*) FROM (
			SELECT r.name FROM ab_user_role ur
			JOIN ab_role r ON r.id = ur.role_id
			WHERE ur.user_id = ?
			UNION ALL
			SELECT r.name FROM ab_user_group ug
			JOIN ab_group_role gr ON gr.group_id = ug.group_id
			JOIN ab_role r ON r.id = gr.role_id
			WHERE ug.user_id = ?
		) t WHERE t.name = ?`, userID, userID, RoleAdmin).Scan(&adminCount).Error
	if err != nil {
		return nil, err
	}
	if adminCount > 0 {
		// Admins hold "* on *"; the concrete catalogue is still resolved so
		// that clients written against the Python API see the same list.
		set["* on *"] = struct{}{}
		set[PermAllDatasourceAccess] = struct{}{}
		set[PermAllDatabaseAccess] = struct{}{}
		set[PermAllQueryAccess] = struct{}{}
	}
	{
		var rows []struct {
			Action   string
			Resource string
		}
		// Permissions inherited through direct roles and through groups.
		err = m.db.WithContext(ctx).Raw(`
			SELECT p.name AS action, vm.name AS resource
			FROM ab_user_role ur
			JOIN ab_permission_view_role pvr ON pvr.role_id = ur.role_id
			JOIN ab_permission_view pv ON pv.id = pvr.permission_view_id
			JOIN ab_permission p ON p.id = pv.permission_id
			JOIN ab_view_menu vm ON vm.id = pv.view_menu_id
			WHERE ur.user_id = ?
			UNION
			SELECT p.name AS action, vm.name AS resource
			FROM ab_user_group ug
			JOIN ab_group_role gr ON gr.group_id = ug.group_id
			JOIN ab_permission_view_role pvr ON pvr.role_id = gr.role_id
			JOIN ab_permission_view pv ON pv.id = pvr.permission_view_id
			JOIN ab_permission p ON p.id = pv.permission_id
			JOIN ab_view_menu vm ON vm.id = pv.view_menu_id
			WHERE ug.user_id = ?`, userID, userID).Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			set[r.Action+" on "+r.Resource] = struct{}{}
			// Namespace permissions are stored as raw strings.
			if strings.HasPrefix(r.Action, "all_") || strings.HasPrefix(r.Action, "datasource_access") ||
				strings.HasPrefix(r.Action, "database_access") || strings.HasPrefix(r.Action, "schema_access") {
				set[r.Action] = struct{}{}
			}
		}
	}
	m.mu.Lock()
	m.cache[userID] = cachedPerms{set: set, expires: time.Now().Add(60 * time.Second)}
	m.mu.Unlock()
	return set, nil
}

// HasPermission reports whether the user holds "action on Resource".
func (m *Manager) HasPermission(ctx context.Context, userID int, action, resource string) bool {
	set, err := m.UserPermissions(ctx, userID)
	if err != nil {
		m.log.Warn("permission lookup failed", zap.Error(err), zap.Int("user_id", userID))
		return false
	}
	if set.Admin() {
		return true
	}
	return set.Has(action + " on " + resource)
}

// CanAccessDatasource applies Superset's rule chain:
// all_datasource_access > database_access > schema_access > datasource_access.
// An empty permission set is treated as "no restriction" only for admins.
func (m *Manager) CanAccessDatasource(ctx context.Context, userID int, dbName, schema, tableName string, datasetID int) bool {
	set, err := m.UserPermissions(ctx, userID)
	if err != nil {
		return false
	}
	if set.Admin() || set.Has(PermAllDatasourceAccess) || set.Has(PermAllDatabaseAccess) {
		return true
	}
	if set.Has(DatabaseAccessPerm(dbName, 0).Action) {
		return true
	}
	if set.Has(SchemaAccessPerm(dbName, schema).Action) {
		return true
	}
	if set.Has(DatasourceAccessPerm(dbName, tableName, datasetID).Action) {
		return true
	}
	// Fall back to a prefix match so that id-less grants resolve.
	prefix := PermDatasourceAccess + " on [" + dbName + "].[" + tableName + "]"
	for p := range set {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// Seed creates the built-in roles and the permission catalogue. It is
// idempotent: existing rows are reused.
func (m *Manager) Seed(ctx context.Context) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 0. Carry a deployment over from the pre-localisation role names.
		// Must run before step 2 so the FirstOrCreate below finds the renamed
		// row and keeps its permission links instead of minting a duplicate.
		if err := renameLegacyRoles(tx); err != nil {
			return err
		}

		// 1. Materialise permissions and view menus.
		actionIDs := map[string]int{}
		for _, a := range allActions() {
			p := models.Permission{Name: a}
			if err := tx.Where("name = ?", a).FirstOrCreate(&p).Error; err != nil {
				return err
			}
			actionIDs[a] = p.ID
		}
		resourceIDs := map[string]int{}
		for _, r := range AllResources() {
			vm := models.ViewMenu{Name: r}
			if err := tx.Where("name = ?", r).FirstOrCreate(&vm).Error; err != nil {
				return err
			}
			resourceIDs[r] = vm.ID
		}
		// Namespace pseudo-permissions live in ab_permission as raw names.
		for _, ns := range []string{PermAllDatasourceAccess, PermAllDatabaseAccess, PermAllQueryAccess} {
			p := models.Permission{Name: ns}
			if err := tx.Where("name = ?", ns).FirstOrCreate(&p).Error; err != nil {
				return err
			}
			vm := models.ViewMenu{Name: ns}
			if err := tx.Where("name = ?", ns).FirstOrCreate(&vm).Error; err != nil {
				return err
			}
			actionIDs[ns] = p.ID
			resourceIDs[ns] = vm.ID
		}

		pvID := func(action, resource string) (int, error) {
			aid, ok1 := actionIDs[action]
			rid, ok2 := resourceIDs[resource]
			if !ok1 || !ok2 {
				return 0, fmt.Errorf("unknown permission %q on %q", action, resource)
			}
			pv := models.PermissionView{PermissionID: aid, ViewMenuID: rid}
			err := tx.Where("permission_id = ? AND view_menu_id = ?", aid, rid).FirstOrCreate(&pv).Error
			return pv.ID, err
		}

		// 2. Built-in roles.
		roles := map[string][]Perm{
			RoleAdmin:  nil, // handled specially: every permission
			RoleAlpha:  AlphaPermissions(),
			RoleGamma:  GammaPermissions(),
			RoleSqlLab: SqlLabPermissions(),
			RolePublic: PublicPermissions(),
		}
		for name, perms := range roles {
			role := models.Role{Name: name}
			if err := tx.Where("name = ?", name).FirstOrCreate(&role).Error; err != nil {
				return err
			}
			if name == RoleAdmin {
				// Admin receives the full catalogue.
				for _, res := range AllResources() {
					for _, act := range allActions() {
						id, err := pvID(act, res)
						if err != nil {
							return err
						}
						if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
							Create(&models.PermissionViewRole{PermissionViewID: id, RoleID: role.ID}).Error; err != nil {
							return err
						}
					}
				}
				for _, ns := range []string{PermAllDatasourceAccess, PermAllDatabaseAccess, PermAllQueryAccess} {
					id, err := pvID(ns, ns)
					if err != nil {
						return err
					}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
						Create(&models.PermissionViewRole{PermissionViewID: id, RoleID: role.ID}).Error; err != nil {
						return err
					}
				}
				continue
			}
			for _, p := range perms {
				var id int
				var err error
				if p.Action == PermAllDatasourceAccess || p.Action == PermDatabaseAccess || p.Action == PermSchemaAccess {
					id, err = pvID(p.Action, p.Action)
				} else {
					id, err = pvID(p.Action, p.Resource)
				}
				if err != nil {
					m.log.Warn("skip unknown permission", zap.String("perm", p.String()))
					continue
				}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).
					Create(&models.PermissionViewRole{PermissionViewID: id, RoleID: role.ID}).Error; err != nil {
					return err
				}
			}
		}

		// 3. Revoke the permissions a non-admin built-in role must never hold.
		// Steps 1-2 only ever add, so a grant that code stopped issuing would
		// otherwise survive forever in an existing deployment. See
		// revokeAdminOnlyPerms for the scope of this correction.
		return revokeAdminOnlyPerms(tx, resourceIDs)
	})
}

// adminOnlyRevokedResources are the resources that must never be granted to a
// non-admin built-in role, whatever an earlier seed wrote. ResLog is the audit
// trail: listLogs / exportLogs return every user's activity, not just the
// caller's, so reading it is an administrator privilege. Operator-granted
// extras on any other resource are deliberately left alone.
var adminOnlyRevokedResources = []string{ResLog}

// renameLegacyRoles carries an existing deployment over from the role names
// shipped before localisation (Admin, Alpha, Gamma, sql_lab, Public) onto the
// current Chinese ones. Renaming in place preserves ab_user_role and
// ab_permission_view_role, so an operator's users keep their role and the role
// keeps its permission set. A role that is already localised, and a fresh
// deployment that has neither row, are both left untouched.
func renameLegacyRoles(tx *gorm.DB) error {
	for _, pair := range legacyRoleNames {
		var current models.Role
		err := tx.Where("name = ?", pair.Current).First(&current).Error
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var legacy models.Role
		if err := tx.Where("name = ?", pair.Legacy).First(&legacy).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		if err := tx.Model(&models.Role{}).Where("id = ?", legacy.ID).
			Update("name", pair.Current).Error; err != nil {
			return err
		}
	}
	return nil
}

// revokeAdminOnlyPerms deletes the seeded grants of adminOnlyRevokedResources
// from the non-admin built-in roles. It is the one non-additive step in Seed,
// and it runs inside Seed's transaction so a failure rolls the whole seed back.
func revokeAdminOnlyPerms(tx *gorm.DB, resourceIDs map[string]int) error {
	roleIDs := make([]int, 0, 4)
	for _, name := range []string{RoleAlpha, RoleGamma, RoleSqlLab, RolePublic} {
		var role models.Role
		if err := tx.Where("name = ?", name).First(&role).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		roleIDs = append(roleIDs, role.ID)
	}
	if len(roleIDs) == 0 {
		return nil
	}
	for _, res := range adminOnlyRevokedResources {
		rid, ok := resourceIDs[res]
		if !ok {
			continue
		}
		var pvIDs []int
		if err := tx.Model(&models.PermissionView{}).Where("view_menu_id = ?", rid).
			Pluck("id", &pvIDs).Error; err != nil {
			return err
		}
		if len(pvIDs) == 0 {
			continue
		}
		if err := tx.Where("role_id IN ? AND permission_view_id IN ?", roleIDs, pvIDs).
			Delete(&models.PermissionViewRole{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// EnsureAdminUser creates the bootstrap admin if no user exists yet.
func (m *Manager) EnsureAdminUser(ctx context.Context, username, password, first, last, email string) (bool, error) {
	var count int64
	if err := m.db.WithContext(ctx).Model(&models.User{}).Count(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	hash, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	now := time.Now()
	user := models.User{
		FirstName:  first,
		LastName:   last,
		Username:   username,
		Password:   hash,
		Active:     true,
		Email:      email,
		LastLogin:  &now,
		LoginCount: ptrInt(1),
	}
	if err := m.db.WithContext(ctx).Create(&user).Error; err != nil {
		return false, err
	}
	var role models.Role
	if err := m.db.WithContext(ctx).Where("name = ?", RoleAdmin).First(&role).Error; err != nil {
		return false, err
	}
	if err := m.db.WithContext(ctx).Create(&models.UserRole{UserID: user.ID, RoleID: role.ID}).Error; err != nil {
		return false, err
	}
	return true, nil
}

func ptrInt(v int) *int { return &v }
