package security

// This file mirrors superset/security/manager.py: the permission strings,
// the built-in roles and the model-view registry used for seeding.

// Permission actions (FAB uses "can_<verb>").
const (
	ActionRead          = "can_read"
	ActionWrite         = "can_write"
	ActionEdit          = "can_edit"
	ActionDelete        = "can_delete"
	ActionList          = "can_list"
	ActionAdd           = "can_add"
	ActionShow          = "can_show"
	ActionExport        = "can_export"
	ActionImport        = "can_import"
	ActionDownload      = "can_download"
	ActionUpload        = "can_upload"
	ActionSave          = "can_save"
	ActionCopy          = "can_copy"
	ActionSync          = "can_sync"
	ActionGet           = "can_get"
	ActionPost          = "can_post"
	ActionPut           = "can_put"
	ActionTestConn      = "can_test_connection"
	ActionWarmUpCache   = "can_warm_up_cache"
	ActionFilter        = "can_filter"
	ActionShare         = "can_share"
	ActionDuplicate     = "can_duplicate"
	ActionBulkDelete    = "can_bulk_delete"
	ActionActivate      = "can_activate"
	ActionDeactivate    = "can_deactivate"
	ActionGetOrCreate   = "can_get_or_create"
	ActionValidate      = "can_validate_parameters"
	ActionSqllabExport  = "can_sqllab_export"
	ActionQuery         = "can_query"
	ActionQueryFormData = "can_query_form_data"
	ActionStatus        = "can_status"
	ActionRestore       = "can_restore"
	ActionCopyDash      = "can_copy_dash"
	ActionPermalink     = "can_dashboard_permalink"
	ActionExternalMeta  = "can_external_metadata"
	ActionExternalMetaN = "can_external_metadata_by_name"
	ActionThisFormGet   = "can_this_form_get"
	ActionThisFormPost  = "can_this_form_post"
)

// Resource names (ab_view_menu.name).
const (
	ResDatabase           = "Database"
	ResDataset            = "Dataset"
	ResChart              = "Chart"
	ResDashboard          = "Dashboard"
	ResScreen             = "Screen"
	ResQuery              = "Query"
	ResSavedQuery         = "SavedQuery"
	ResTableColumn        = "TableColumn"
	ResSqlMetric          = "SqlMetric"
	ResTabState           = "TabState"
	ResLog                = "Log"
	ResRowLevelSecurity   = "RowLevelSecurity"
	ResReportSchedule     = "ReportSchedule"
	ResAlert              = "Alert"
	ResAnnotation         = "Annotation"
	ResAnnotationLayer    = "AnnotationLayer"
	ResTag                = "Tag"
	ResTheme              = "Theme"
	ResCssTemplate        = "CssTemplate"
	ResDynamicPlugin      = "DynamicPlugin"
	ResEmbeddedDashboard  = "EmbeddedDashboard"
	ResKeyValue           = "KeyValue"
	ResFavStar            = "FavStar"
	ResImportExport       = "ImportExport"
	ResDashboardPermalink = "DashboardPermalink"
	ResExplore            = "Explore"
	ResSqllab             = "SqlLab"
	ResSecurity           = "Security"
	ResUser               = "User"
	ResRole               = "Role"
	ResPermission         = "Permission"
	ResViewMenu           = "ViewMenu"
	ResPermissionView     = "PermissionView"
	ResGroup              = "Group"
	ResRegisterUser       = "RegisterUser"
	ResUserAttribute      = "UserAttribute"
	ResTask               = "Task"
	ResSemanticLayer      = "SemanticLayer"
	ResSemanticView       = "SemanticView"
	ResSubject            = "Subject"
	ResAdvancedDataType   = "AdvancedDataType"
	ResDatasource         = "Datasource"
	ResSqlObserver        = "SqlObserver"
	ResAlertValidator     = "AlertValidator"
	ResThemeEditor        = "ThemeEditor"
	ResExtensionStorage   = "ExtensionStorage"
	ResAIAgent            = "AIAgent"
	// ResAIAnalysis gates the Agent 分析 plane: asking questions (invoke), the
	// capability menu the chat UI renders, and the caller's own run history. It
	// is split from ResAIAgent on purpose — ResAIAgent gates the 配置 plane
	// (providers / agents / skills / tools / global settings), which stays
	// administrator-only, while analysts are expected to use the Agent itself.
	ResAIAnalysis = "AIAnalysis"
	ResSystem     = "System"
)

// Special permission namespaces used by Superset's object-level security.
const (
	PermAllDatasourceAccess = "all_datasource_access"
	PermAllDatabaseAccess   = "all_database_access"
	PermAllQueryAccess      = "all_query_access"
	PermDatasourceAccess    = "datasource_access"
	PermSchemaAccess        = "schema_access"
	PermDatabaseAccess      = "database_access"
	PermMenuAccess          = "menu_access"
)

// Role names.
const (
	RoleAdmin  = "管理员"
	RoleAlpha  = "数据分析师"
	RoleGamma  = "数据查看者"
	RoleSqlLab = "SQL 用户"
	RolePublic = "公开用户"
	RoleGuest  = RoleGamma
)

// legacyRoleNames maps the pre-localisation role names onto the current ones.
// The name is both the identifier and the display name here — the role editor
// renders it verbatim on every user and RLS tag — so renaming is a data change,
// not a presentation one. Seed renames these rows in place (see
// renameLegacyRoles) so an existing deployment keeps every user assignment and
// permission link instead of being handed a duplicate empty role.
var legacyRoleNames = []struct{ Legacy, Current string }{
	{"Admin", RoleAdmin},
	{"Alpha", RoleAlpha},
	{"Gamma", RoleGamma},
	{"sql_lab", RoleSqlLab},
	{"Public", RolePublic},
}

// IsBuiltinRole reports whether name is one of the roles Seed creates. The name
// is the identity those roles are addressed by (admin detection in
// UserPermissions, seeding, the API payloads), so the API refuses to delete or
// rename them; their permission set stays editable.
func IsBuiltinRole(name string) bool {
	switch name {
	case RoleAdmin, RoleAlpha, RoleGamma, RoleSqlLab, RolePublic:
		return true
	}
	return false
}

// Perm is a (action, resource) pair.
type Perm struct {
	Action   string
	Resource string
}

// String renders the FAB permission string "action on Resource".
func (p Perm) String() string { return p.Action + " on " + p.Resource }

// ReadModelViews are the resources Alpha/Gamma may read by default.
var ReadModelViews = []string{
	ResChart, ResDashboard, ResScreen, ResDatabase, ResDataset, ResAnnotation,
	ResAnnotationLayer, ResCssTemplate, ResQuery, ResSavedQuery,
	ResTableColumn, ResSqlMetric, ResTabState, ResTag, ResTheme,
	ResReportSchedule, ResAlert, ResImportExport, ResFavStar,
	ResKeyValue, ResDynamicPlugin, ResEmbeddedDashboard, ResUserAttribute,
	ResSemanticLayer, ResSemanticView, ResSubject, ResAdvancedDataType,
	ResExtensionStorage, ResSqlObserver, ResAlertValidator,
	// Agent 分析 plane — 问数 is an analyst activity, not an admin one.
	ResAIAnalysis,
	// ResLog is absent on purpose: the audit trail is administrator-only so a
	// read-only role cannot enumerate who changed what. Same for ResAIAgent,
	// ResSystem, ResRowLevelSecurity and ResSecurity.
}

// WriteModelViews are the resources Alpha may also write.
var WriteModelViews = []string{
	ResChart, ResDashboard, ResScreen, ResDatabase, ResDataset, ResAnnotation,
	ResAnnotationLayer, ResCssTemplate, ResQuery, ResSavedQuery,
	ResTableColumn, ResSqlMetric, ResTabState, ResTag, ResTheme,
	ResReportSchedule, ResAlert, ResImportExport, ResEmbeddedDashboard,
	ResSemanticLayer, ResSemanticView, ResSubject,
}

// AdminOnlyModelViews are reserved for the Admin role.
var AdminOnlyModelViews = []string{
	ResUser, ResRole, ResPermission, ResViewMenu, ResPermissionView,
	ResGroup, ResRegisterUser, ResLog, ResRowLevelSecurity,
	ResDynamicPlugin, ResTheme, ResCssTemplate, ResAdvancedDataType,
	ResSecurity, ResTask, ResExtensionStorage, ResDatabase, ResAIAgent,
}

// AdminPermissions returns every permission the Admin role holds.
// Superset grants Admin "* on *"; the concrete set is materialised at seed
// time from BasePermissions so that the UI can display it.
func AdminPermissions() []Perm {
	var perms []Perm
	for _, res := range AllResources() {
		for _, act := range allActions() {
			perms = append(perms, Perm{Action: act, Resource: res})
		}
	}
	return perms
}

// AlphaPermissions returns the permission set of the Alpha role.
func AlphaPermissions() []Perm {
	perms := []Perm{
		{ActionPermAllDatasourceAccess(), ResDatasource},
	}
	for _, res := range ReadModelViews {
		perms = append(perms, Perm{Action: ActionRead, Resource: res}, Perm{Action: ActionList, Resource: res})
	}
	for _, res := range WriteModelViews {
		perms = append(perms, Perm{Action: ActionWrite, Resource: res}, Perm{Action: ActionEdit, Resource: res})
	}
	perms = append(perms,
		Perm{Action: ActionExport, Resource: ResImportExport},
		Perm{Action: ActionImport, Resource: ResImportExport},
		Perm{Action: ActionSqllabExport, Resource: ResSqllab},
		Perm{Action: ActionWarmUpCache, Resource: ResChart},
		Perm{Action: ActionCopyDash, Resource: ResDashboard},
		Perm{Action: ActionPermalink, Resource: ResDashboard},
		Perm{Action: ActionQuery, Resource: ResDataset},
		Perm{Action: ActionQueryFormData, Resource: ResChart},
		Perm{Action: ActionShare, Resource: ResChart},
		Perm{Action: ActionShare, Resource: ResDashboard},
		Perm{Action: ActionFilter, Resource: ResDataset},
		// Run the Agent (问数 / 取数 / 洞察). Configuring it stays ResAIAgent,
		// which an analyst does not hold.
		Perm{Action: ActionQuery, Resource: ResAIAnalysis},
	)
	return perms
}

// GammaPermissions returns the read-only permission set of the Gamma role.
func GammaPermissions() []Perm {
	var perms []Perm
	for _, res := range ReadModelViews {
		perms = append(perms, Perm{Action: ActionRead, Resource: res}, Perm{Action: ActionList, Resource: res})
	}
	perms = append(perms,
		Perm{Action: ActionSqllabExport, Resource: ResSqllab},
		Perm{Action: ActionQuery, Resource: ResDataset},
		Perm{Action: ActionQueryFormData, Resource: ResChart},
		Perm{Action: ActionCopyDash, Resource: ResDashboard},
		Perm{Action: ActionPermalink, Resource: ResDashboard},
		// A read-only role may still ask the Agent questions: the 分析 plane only
		// reads data, and its answers inherit the caller's own RLS/RBAC. Write
		// tools stay behind ResAIAgent (and the global write switch).
		Perm{Action: ActionQuery, Resource: ResAIAnalysis},
	)
	return perms
}

// SqlLabPermissions returns the permission set of the sql_lab role.
func SqlLabPermissions() []Perm {
	return []Perm{
		{Action: ActionRead, Resource: ResDatabase},
		{Action: ActionList, Resource: ResDatabase},
		{Action: ActionRead, Resource: ResQuery},
		{Action: ActionWrite, Resource: ResQuery},
		{Action: ActionRead, Resource: ResSavedQuery},
		{Action: ActionWrite, Resource: ResSavedQuery},
		{Action: ActionRead, Resource: ResTabState},
		{Action: ActionWrite, Resource: ResTabState},
		{Action: ActionRead, Resource: ResDataset},
		{Action: ActionList, Resource: ResDataset},
		{Action: ActionRead, Resource: ResTableColumn},
		{Action: ActionRead, Resource: ResSqlMetric},
		{Action: ActionSqllabExport, Resource: ResSqllab},
		{Action: ActionQuery, Resource: ResDataset},
	}
}

// PublicPermissions returns the (configurable) Public role permission set.
func PublicPermissions() []Perm {
	return nil
}

// allActions returns every permission action known to the system.
func allActions() []string {
	return []string{
		ActionRead, ActionWrite, ActionEdit, ActionDelete, ActionList,
		ActionAdd, ActionShow, ActionExport, ActionImport, ActionDownload,
		ActionUpload, ActionSave, ActionCopy, ActionSync, ActionGet,
		ActionPost, ActionPut, ActionTestConn, ActionWarmUpCache, ActionFilter,
		ActionShare, ActionDuplicate, ActionBulkDelete, ActionActivate,
		ActionDeactivate, ActionGetOrCreate, ActionValidate, ActionSqllabExport,
		ActionQuery, ActionQueryFormData, ActionStatus, ActionRestore,
		ActionCopyDash, ActionPermalink, ActionExternalMeta, ActionExternalMetaN,
		ActionThisFormGet, ActionThisFormPost,
	}
}

// AllResources returns every model-view name registered by the application.
func AllResources() []string {
	return []string{
		ResDatabase, ResDataset, ResChart, ResDashboard, ResScreen, ResQuery, ResSavedQuery,
		ResTableColumn, ResSqlMetric, ResTabState, ResLog, ResRowLevelSecurity,
		ResReportSchedule, ResAlert, ResAnnotation, ResAnnotationLayer, ResTag,
		ResTheme, ResCssTemplate, ResDynamicPlugin, ResEmbeddedDashboard,
		ResKeyValue, ResFavStar, ResImportExport, ResDashboardPermalink,
		ResExplore, ResSqllab, ResSecurity, ResUser, ResRole, ResPermission,
		ResViewMenu, ResPermissionView, ResGroup, ResRegisterUser,
		ResUserAttribute, ResTask, ResSemanticLayer, ResSemanticView, ResSubject,
		ResAdvancedDataType, ResSqlObserver, ResAlertValidator, ResThemeEditor,
		ResExtensionStorage, ResAIAgent, ResAIAnalysis, ResSystem,
	}
}

// ActionPermAllDatasourceAccess keeps the naming aligned with the constant.
func ActionPermAllDatasourceAccess() string { return PermAllDatasourceAccess }

// DatasourceAccessPerm builds "datasource_access on [db].[table](id)".
func DatasourceAccessPerm(dbName, tableName string, id int) Perm {
	return Perm{Action: PermDatasourceAccess, Resource: "[" + dbName + "].[" + tableName + "](" + itoa(id) + ")"}
}

// DatabaseAccessPerm builds "database_access on [db](id)".
func DatabaseAccessPerm(dbName string, id int) Perm {
	return Perm{Action: PermDatabaseAccess, Resource: "[" + dbName + "](" + itoa(id) + ")"}
}

// SchemaAccessPerm builds "schema_access on [db].[schema]".
func SchemaAccessPerm(dbName, schema string) Perm {
	return Perm{Action: PermSchemaAccess, Resource: "[" + dbName + "].[" + schema + "]"}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
