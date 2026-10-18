package models

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// SemanticLayer maps the semantic_layers table (superset.semantic_layers.models.SemanticLayer).
// The primary key is a UUID.
type SemanticLayer struct {
	UUID                 string         `gorm:"column:uuid;primaryKey;type:uuid" json:"uuid"`
	CreatedOn            *time.Time     `gorm:"column:created_on" json:"created_on"`
	ChangedOn            *time.Time     `gorm:"column:changed_on" json:"changed_on"`
	CreatedByFk          *int           `gorm:"column:created_by_fk" json:"created_by_fk"`
	ChangedByFk          *int           `gorm:"column:changed_by_fk" json:"changed_by_fk"`
	Name                 string         `gorm:"column:name;not null" json:"name"`
	Description          string         `gorm:"column:description" json:"description"`
	Type                 string         `gorm:"column:type;not null" json:"type"`
	Configuration        datatypes.JSON `gorm:"column:configuration" json:"configuration"`
	ConfigurationVersion int            `gorm:"column:configuration_version;not null;default:1" json:"configuration_version"`
	CacheVersion         int            `gorm:"column:cache_version;not null;default:0" json:"cache_version"`
	CacheTimeout         *int           `gorm:"column:cache_timeout" json:"cache_timeout"`
	Perm                 string         `gorm:"column:perm" json:"perm"`
}

// TableName returns the original table name.
func (*SemanticLayer) TableName() string { return "semantic_layers" }

// BeforeCreate generates the UUID primary key when unset.
func (m *SemanticLayer) BeforeCreate(tx *gorm.DB) error {
	if m.UUID == "" {
		m.UUID = NewUUID()
	}
	return nil
}

// SemanticView maps the semantic_views table (superset.semantic_layers.models.SemanticView).
type SemanticView struct {
	BaseModel
	UUID                 string         `gorm:"column:uuid;type:uuid;unique;not null" json:"uuid"`
	Name                 string         `gorm:"column:name;not null" json:"name"`
	Description          string         `gorm:"column:description" json:"description"`
	Configuration        datatypes.JSON `gorm:"column:configuration" json:"configuration"`
	ConfigurationVersion int            `gorm:"column:configuration_version;not null;default:1" json:"configuration_version"`
	CacheTimeout         *int           `gorm:"column:cache_timeout" json:"cache_timeout"`
	Perm                 string         `gorm:"column:perm" json:"perm"`
	SemanticLayerUUID    string         `gorm:"column:semantic_layer_uuid;type:uuid;not null" json:"semantic_layer_uuid"`
}

// TableName returns the original table name.
func (SemanticView) TableName() string { return "semantic_views" }

// Subject maps the subjects table (superset.subjects.models.Subject).
type Subject struct {
	BaseModel
	UUID           string `gorm:"column:uuid;type:uuid" json:"uuid"`
	Label          string `gorm:"column:label;not null;index:ix_subjects_label" json:"label"`
	SecondaryLabel string `gorm:"column:secondary_label" json:"secondary_label"`
	Active         *bool  `gorm:"column:active" json:"active"`
	ExtraSearch    string `gorm:"column:extra_search;index:ix_subjects_extra_search" json:"extra_search"`
	Type           int    `gorm:"column:type;not null" json:"type"`
	UserID         *int   `gorm:"column:user_id;unique" json:"user_id"`
	RoleID         *int   `gorm:"column:role_id;unique" json:"role_id"`
	GroupID        *int   `gorm:"column:group_id;unique" json:"group_id"`
}

// TableName returns the original table name.
func (Subject) TableName() string { return "subjects" }

// DashboardEditors maps the dashboard_editors association table.
type DashboardEditors struct {
	ID          int `gorm:"column:id;primaryKey" json:"id"`
	SubjectID   int `gorm:"column:subject_id;not null" json:"subject_id"`
	DashboardID int `gorm:"column:dashboard_id;not null" json:"dashboard_id"`
}

// TableName returns the original table name.
func (DashboardEditors) TableName() string { return "dashboard_editors" }

// DashboardViewers maps the dashboard_viewers association table.
type DashboardViewers struct {
	ID          int `gorm:"column:id;primaryKey" json:"id"`
	SubjectID   int `gorm:"column:subject_id;not null" json:"subject_id"`
	DashboardID int `gorm:"column:dashboard_id;not null" json:"dashboard_id"`
}

// TableName returns the original table name.
func (DashboardViewers) TableName() string { return "dashboard_viewers" }

// ChartEditors maps the chart_editors association table.
type ChartEditors struct {
	ID        int `gorm:"column:id;primaryKey" json:"id"`
	SubjectID int `gorm:"column:subject_id;not null" json:"subject_id"`
	ChartID   int `gorm:"column:chart_id;not null" json:"chart_id"`
}

// TableName returns the original table name.
func (ChartEditors) TableName() string { return "chart_editors" }

// ChartViewers maps the chart_viewers association table.
type ChartViewers struct {
	ID        int `gorm:"column:id;primaryKey" json:"id"`
	SubjectID int `gorm:"column:subject_id;not null" json:"subject_id"`
	ChartID   int `gorm:"column:chart_id;not null" json:"chart_id"`
}

// TableName returns the original table name.
func (ChartViewers) TableName() string { return "chart_viewers" }

// SqlatableEditors maps the sqlatable_editors association table.
type SqlatableEditors struct {
	ID        int `gorm:"column:id;primaryKey" json:"id"`
	SubjectID int `gorm:"column:subject_id;not null" json:"subject_id"`
	TableID   int `gorm:"column:table_id;not null" json:"table_id"`
}

// TableName returns the original table name.
func (SqlatableEditors) TableName() string { return "sqlatable_editors" }

// ReportScheduleEditors maps the report_schedule_editors association table.
type ReportScheduleEditors struct {
	ID               int `gorm:"column:id;primaryKey" json:"id"`
	SubjectID        int `gorm:"column:subject_id;not null" json:"subject_id"`
	ReportScheduleID int `gorm:"column:report_schedule_id;not null" json:"report_schedule_id"`
}

// TableName returns the original table name.
func (ReportScheduleEditors) TableName() string { return "report_schedule_editors" }

// ThemeEditors maps the theme_editors association table.
type ThemeEditors struct {
	ID        int `gorm:"column:id;primaryKey" json:"id"`
	SubjectID int `gorm:"column:subject_id;not null" json:"subject_id"`
	ThemeID   int `gorm:"column:theme_id;not null" json:"theme_id"`
}

// TableName returns the original table name.
func (ThemeEditors) TableName() string { return "theme_editors" }
