package models

import (
	"time"

	"gorm.io/datatypes"
)

// Dashboard maps the dashboards table (superset.models.dashboard.Dashboard).
type Dashboard struct {
	BaseModel
	UUID                 string         `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	DeletedAt            *time.Time     `gorm:"column:deleted_at;index" json:"deleted_at"`
	DashboardTitle       string         `gorm:"column:dashboard_title" json:"dashboard_title"`
	PositionJSON         datatypes.JSON `gorm:"column:position_json" json:"position_json"`
	Description          string         `gorm:"column:description" json:"description"`
	CSS                  string         `gorm:"column:css" json:"css"`
	ThemeID              *int           `gorm:"column:theme_id" json:"theme_id"`
	CertifiedBy          string         `gorm:"column:certified_by" json:"certified_by"`
	CertificationDetails string         `gorm:"column:certification_details" json:"certification_details"`
	JSONMetadata         datatypes.JSON `gorm:"column:json_metadata" json:"json_metadata"`
	Slug                 string         `gorm:"column:slug" json:"slug"`
	Published            bool           `gorm:"column:published;default:false" json:"published"`
	IsManagedExternally  bool           `gorm:"column:is_managed_externally;not null;default:false" json:"is_managed_externally"`
	ExternalURL          string         `gorm:"column:external_url" json:"external_url"`
}

// TableName returns the original table name.
func (Dashboard) TableName() string { return "dashboards" }

// DashboardSlices maps the dashboard_slices association table.
type DashboardSlices struct {
	DashboardID int `gorm:"column:dashboard_id;primaryKey" json:"dashboard_id"`
	SliceID     int `gorm:"column:slice_id;primaryKey" json:"slice_id"`
}

// TableName returns the original table name.
func (DashboardSlices) TableName() string { return "dashboard_slices" }

// DashboardUser maps the dashboard_user association table.
type DashboardUser struct {
	UserID      int `gorm:"column:user_id;primaryKey" json:"user_id"`
	DashboardID int `gorm:"column:dashboard_id;primaryKey" json:"dashboard_id"`
}

// TableName returns the original table name.
func (DashboardUser) TableName() string { return "dashboard_user" }

// DashboardRoles maps the dashboard_roles association table.
type DashboardRoles struct {
	DashboardID int `gorm:"column:dashboard_id;primaryKey" json:"dashboard_id"`
	RoleID      int `gorm:"column:role_id;primaryKey" json:"role_id"`
}

// TableName returns the original table name.
func (DashboardRoles) TableName() string { return "dashboard_roles" }
