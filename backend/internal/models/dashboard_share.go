package models

import "time"

// DashboardShare holds the public-sharing settings of one dashboard.
//
// It lives in its own table rather than on Dashboard for two reasons:
//   - the password hash and the public token must never leak into the regular
//     dashboard payloads (`/api/v1/dashboard/{id}` is read by every viewer);
//   - the feature can be revoked by dropping rows without touching boards.
//
// The token is a 24-byte URL-safe random string. The password is bcrypt
// hashed; an empty hash means "no password". ExpiresAt nil means "never".
type DashboardShare struct {
	BaseModel
	DashboardID int    `gorm:"column:dashboard_id;uniqueIndex;not null" json:"dashboard_id"`
	Enabled     bool   `gorm:"column:enabled;not null;default:false" json:"enabled"`
	Token       string `gorm:"column:token;size:64;index" json:"token"`

	PasswordHash string `gorm:"column:password_hash;size:255" json:"-"`

	ExpiresAt   *time.Time `gorm:"column:expires_at" json:"expires_at"`
	AllowExport bool       `gorm:"column:allow_export;not null;default:true" json:"allow_export"`
	ViewCount   int64      `gorm:"column:view_count;not null;default:0" json:"view_count"`
}

// TableName keeps the table name short and unambiguous.
func (DashboardShare) TableName() string { return "dashboard_share" }

// HasPassword reports whether the share is password protected.
func (s *DashboardShare) HasPassword() bool { return s.PasswordHash != "" }

// Expired reports whether the share link is past its expiry.
func (s *DashboardShare) Expired() bool {
	return s.ExpiresAt != nil && time.Now().After(*s.ExpiresAt)
}

// Active reports whether the link can still be opened by an anonymous visitor.
func (s *DashboardShare) Active() bool {
	return s.Enabled && s.Token != "" && !s.Expired()
}
