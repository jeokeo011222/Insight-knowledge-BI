package models

// register.go registers every model with the migration registry so that
// models.All() returns the full set of superset tables.

func init() {
	register(
		// security.go (FAB + ab_group family)
		&User{},
		&Role{},
		&Permission{},
		&ViewMenu{},
		&PermissionView{},
		&PermissionViewRole{},
		&UserRole{},
		&Group{},
		&UserGroup{},
		&GroupRole{},
		&RegisterUser{},

		// database.go
		&Database{},
		&SSHTunnel{},
		&DatabaseUserOAuth2Tokens{},

		// dataset.go
		&SqlaTable{},
		&TableColumn{},
		&SqlMetric{},
		&SqlatableUser{},

		// slice.go
		&Slice{},
		&SliceUser{},

		// dashboard.go
		&Dashboard{},
		&DashboardSlices{},
		&DashboardUser{},
		&DashboardRoles{},

		// dashboard_share.go (public share links)
		&DashboardShare{},

		// screen.go (data-visualisation wall)
		&Screen{},
		&ScreenRevision{},

		// query.go
		&Query{},
		&SavedQuery{},
		&TabState{},
		&TableSchema{},

		// log.go
		&Log{},

		// annotation.go
		&AnnotationLayer{},
		&Annotation{},

		// tag.go
		&Tag{},
		&TaggedObject{},
		&UserFavoriteTag{},

		// rls.go
		&RowLevelSecurityFilter{},
		&RLSFilterTables{},
		&RLSFilterSubjects{},
		&RLSFilterRoles{},

		// report.go
		&ReportSchedule{},
		&ReportRecipient{},
		&ReportExecutionLog{},
		&ReportScheduleUser{},

		// embedded.go
		&EmbeddedDashboard{},

		// misc.go
		&KeyValue{},
		&KeyValueEntry{},
		&CssTemplate{},
		&Theme{},
		&CacheKey{},
		&FavStar{},
		&DynamicPlugin{},
		&UserAttribute{},

		// tasks.go
		&Task{},
		&TaskDependency{},
		&TaskSubscriber{},

		// semantic.go
		&SemanticLayer{},
		&SemanticView{},

		// semantic_model.go (MDL-lite: benchmark W-05/W-08)
		&SemanticModel{},
		&SemanticField{},
		&SemanticRelationship{},
		&SemanticRule{},
		&Subject{},
		&DashboardEditors{},
		&DashboardViewers{},
		&ChartEditors{},
		&ChartViewers{},
		&SqlatableEditors{},
		&ReportScheduleEditors{},
		&ThemeEditors{},

		// extension.go
		&ExtensionStorage{},

		// purge.go
		&PurgeAuditCoordination{},
		&PurgeAuditLog{},
	)

	// ai.go — AI Agent tables (optional: pgvector may be unavailable).
	// Order matters: ai_meta_embedding (vector column) is migrated last so a
	// missing pgvector extension only fails the tail of the list.
	registerAI(
		&AIProvider{},
		&AIAgent{},
		&AIAgentRun{},
		&AIToolCallLog{},
		&AISkillRegistry{},
		&AIToolRegistry{},
		&AIChatHistory{},
		&AIAuditLog{},
		&AIMetric{},
		// NL→SQL examples. Registered before the vector table so a deployment
		// without pgvector still gets the memory feature.
		&AISQLMemory{},
		// Answer replay (回答记忆): the SQL and answer of the last identical
		// question, reused while the data has not changed.
		&AIRunReplay{},
		&AIMetaEmbedding{},
	)
}
