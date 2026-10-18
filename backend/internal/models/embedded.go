package models

import (
	"time"

	"gorm.io/gorm"
)

// EmbeddedDashboard maps the embedded_dashboards table
// (superset.models.embedded_dashboard.EmbeddedDashboard). The primary key is a UUID.
type EmbeddedDashboard struct {
	UUID                    string     `gorm:"column:uuid;primaryKey;type:uuid" json:"uuid"`
	CreatedOn               *time.Time `gorm:"column:created_on" json:"created_on"`
	ChangedOn               *time.Time `gorm:"column:changed_on" json:"changed_on"`
	CreatedByFk             *int       `gorm:"column:created_by_fk" json:"created_by_fk"`
	ChangedByFk             *int       `gorm:"column:changed_by_fk" json:"changed_by_fk"`
	AllowDomainList         string     `gorm:"column:allow_domain_list" json:"allow_domain_list"`
	GuestTokenRevokedBefore *int       `gorm:"column:guest_token_revoked_before" json:"guest_token_revoked_before"`
	DashboardID             int        `gorm:"column:dashboard_id;not null" json:"dashboard_id"`
}

// TableName returns the original table name.
func (*EmbeddedDashboard) TableName() string { return "embedded_dashboards" }

// BeforeCreate generates the UUID primary key when unset.
func (m *EmbeddedDashboard) BeforeCreate(tx *gorm.DB) error {
	if m.UUID == "" {
		m.UUID = NewUUID()
	}
	return nil
}
