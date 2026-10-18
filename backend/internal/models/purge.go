package models

import (
	"time"

	"gorm.io/gorm"
)

// PurgeAuditCoordination maps the purge_audit_coordination singleton table
// (superset.models.purge_audit_log.PurgeAuditCoordination). The primary key is a UUID.
type PurgeAuditCoordination struct {
	ID          string `gorm:"column:id;primaryKey;type:uuid" json:"id"`
	LockVersion int    `gorm:"column:lock_version;not null;default:0" json:"lock_version"`
}

// TableName returns the original table name.
func (*PurgeAuditCoordination) TableName() string { return "purge_audit_coordination" }

// BeforeCreate generates the UUID primary key when unset.
func (m *PurgeAuditCoordination) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = NewUUID()
	}
	return nil
}

// PurgeAuditLog maps the purge_audit_log table
// (superset.models.purge_audit_log.PurgeAuditLog). The primary key is a UUID.
type PurgeAuditLog struct {
	ID                     string     `gorm:"column:id;primaryKey;type:uuid" json:"id"`
	Status                 string     `gorm:"column:status;not null;default:pending;index:ix_purge_audit_log_status_created_on" json:"status"`
	Trigger                string     `gorm:"column:trigger;not null" json:"trigger"`
	Actor                  string     `gorm:"column:actor;not null" json:"actor"`
	EntityType             string     `gorm:"column:entity_type;not null" json:"entity_type"`
	EntityUUID             string     `gorm:"column:entity_uuid;index" json:"entity_uuid"`
	AffectedReferrers      string     `gorm:"column:affected_referrers" json:"affected_referrers"`
	Reason                 string     `gorm:"column:reason" json:"reason"`
	RemovedDashboardSlices int        `gorm:"column:removed_dashboard_slices;not null;default:0" json:"removed_dashboard_slices"`
	CreatedOn              time.Time  `gorm:"column:created_on;not null" json:"created_on"`
	ConfirmedOn            *time.Time `gorm:"column:confirmed_on" json:"confirmed_on"`
}

// TableName returns the original table name.
func (*PurgeAuditLog) TableName() string { return "purge_audit_log" }

// BeforeCreate generates the UUID primary key when unset.
func (m *PurgeAuditLog) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = NewUUID()
	}
	if m.CreatedOn.IsZero() {
		m.CreatedOn = time.Now()
	}
	return nil
}
