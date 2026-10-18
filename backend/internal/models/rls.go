package models

// RowLevelSecurityFilter maps the row_level_security_filters table
// (superset.connectors.sqla.models.RowLevelSecurityFilter).
type RowLevelSecurityFilter struct {
	BaseModel
	Name        string `gorm:"column:name;unique;not null" json:"name"`
	Description string `gorm:"column:description" json:"description"`
	FilterType  string `gorm:"column:filter_type" json:"filter_type"`
	GroupKey    string `gorm:"column:group_key" json:"group_key"`
	Clause      string `gorm:"column:clause;not null" json:"clause"`
}

// TableName returns the original table name.
func (RowLevelSecurityFilter) TableName() string { return "row_level_security_filters" }

// RLSFilterTables maps the rls_filter_tables association table.
type RLSFilterTables struct {
	TableID     int `gorm:"column:table_id;primaryKey" json:"table_id"`
	RLSFilterID int `gorm:"column:rls_filter_id;primaryKey" json:"rls_filter_id"`
}

// TableName returns the original table name.
func (RLSFilterTables) TableName() string { return "rls_filter_tables" }

// RLSFilterSubjects maps the rls_filter_subjects association table.
type RLSFilterSubjects struct {
	ID          int `gorm:"column:id;primaryKey" json:"id"`
	SubjectID   int `gorm:"column:subject_id;not null" json:"subject_id"`
	RLSFilterID int `gorm:"column:rls_filter_id;not null" json:"rls_filter_id"`
}

// TableName returns the original table name.
func (RLSFilterSubjects) TableName() string { return "rls_filter_subjects" }

// RLSFilterRoles maps the rls_filter_roles association table.
type RLSFilterRoles struct {
	RoleID      int `gorm:"column:role_id;primaryKey" json:"role_id"`
	RLSFilterID int `gorm:"column:rls_filter_id;primaryKey" json:"rls_filter_id"`
}

// TableName returns the original table name.
func (RLSFilterRoles) TableName() string { return "rls_filter_roles" }
