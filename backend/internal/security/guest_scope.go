package security

// Guest token scopes and the permission sets they map to.
//
// Embedded dashboards have always been minted with a blanket grant, and the
// embedded flow relies on it (a guest has no roles in the database, so any
// "datasource_access" check would otherwise fail). Public share links are a
// different trust level: the holder clicked a URL, so the token must not be
// able to read users, logs, SQL Lab or anything writable.
//
// GuestPermissionSet returns the set that the Authenticate middleware attaches
// to a guest request.

// publicGuestResources is the read-only surface a public dashboard needs:
// the board itself, its charts, the datasets behind them and the engines the
// charts query. Everything else (User, Role, Log, SqlLab, ReportSchedule…) is
// deliberately absent.
var publicGuestResources = []string{
	ResDashboard,
	ResChart,
	ResDataset,
	ResDatabase,
	ResQuery,
	ResSavedQuery,
	ResAnnotation,
	ResAnnotationLayer,
	ResTheme,
}

// GuestPermissionSet maps a guest scope onto its permission set.
func GuestPermissionSet(scope string) PermissionSet {
	if scope != ScopePublic {
		// Legacy embedded grant: unchanged behaviour.
		return PermissionSet{"* on *": {}}
	}
	set := make(PermissionSet, len(publicGuestResources)+1)
	for _, res := range publicGuestResources {
		set[ActionRead+" on "+res] = struct{}{}
	}
	// Rendering a tile executes the chart's saved query.
	set[ActionQueryFormData+" on "+ResChart] = struct{}{}
	// Datasource-level access checks resolve through this grant.
	set[PermAllDatasourceAccess] = struct{}{}
	return set
}
