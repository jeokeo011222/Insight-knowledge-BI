package models

import "time"

// Log maps the logs table (superset.models.core.Log).
type Log struct {
	BaseModel
	Action      string     `gorm:"column:action" json:"action"`
	UserID      *int       `gorm:"column:user_id" json:"user_id"`
	DashboardID *int       `gorm:"column:dashboard_id" json:"dashboard_id"`
	SliceID     *int       `gorm:"column:slice_id" json:"slice_id"`
	JSON        string     `gorm:"column:json" json:"json"`
	Dttm        *time.Time `gorm:"column:dttm" json:"dttm"`
	DurationMs  *int       `gorm:"column:duration_ms" json:"duration_ms"`
	Referrer    string     `gorm:"column:referrer" json:"referrer"`
}

// TableName returns the original table name.
func (Log) TableName() string { return "logs" }
