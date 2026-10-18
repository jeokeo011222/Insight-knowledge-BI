package models

import (
	"time"
)

// KeyValue maps the keyvalue table (superset.models.core.KeyValue).
type KeyValue struct {
	ID    int    `gorm:"column:id;primaryKey" json:"id"`
	Value string `gorm:"column:value;not null" json:"value"`
}

// TableName returns the original table name.
func (KeyValue) TableName() string { return "keyvalue" }

// KeyValueEntry maps the key_value table (superset.key_value.models.KeyValueEntry).
type KeyValueEntry struct {
	BaseModel
	UUID      string     `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	Resource  string     `gorm:"column:resource;not null" json:"resource"`
	Value     []byte     `gorm:"column:value;not null" json:"-"`
	ExpiresOn *time.Time `gorm:"column:expires_on" json:"expires_on"`
}

// TableName returns the original table name.
func (KeyValueEntry) TableName() string { return "key_value" }

// CssTemplate maps the css_templates table (superset.models.core.CssTemplate).
type CssTemplate struct {
	BaseModel
	UUID         string `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	TemplateName string `gorm:"column:template_name" json:"template_name"`
	CSS          string `gorm:"column:css" json:"css"`
}

// TableName returns the original table name.
func (CssTemplate) TableName() string { return "css_templates" }

// Theme maps the themes table (superset.models.core.Theme).
type Theme struct {
	BaseModel
	UUID            string `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	ThemeName       string `gorm:"column:theme_name" json:"theme_name"`
	JSONData        string `gorm:"column:json_data" json:"json_data"`
	IsSystem        bool   `gorm:"column:is_system;not null;default:false" json:"is_system"`
	IsSystemDefault bool   `gorm:"column:is_system_default;not null;default:false;index:idx_theme_is_system_default" json:"is_system_default"`
	IsSystemDark    bool   `gorm:"column:is_system_dark;not null;default:false;index:idx_theme_is_system_dark" json:"is_system_dark"`
}

// TableName returns the original table name.
func (Theme) TableName() string { return "themes" }

// CacheKey maps the cache_keys table (superset.models.cache.CacheKey).
type CacheKey struct {
	BaseModel
	CacheKey      string `gorm:"column:cache_key;not null" json:"cache_key"`
	CacheTimeout  *int   `gorm:"column:cache_timeout" json:"cache_timeout"`
	DatasourceUID string `gorm:"column:datasource_uid;not null;index" json:"datasource_uid"`
}

// TableName returns the original table name.
func (CacheKey) TableName() string { return "cache_keys" }

// FavStar maps the favstar table (superset.models.core.FavStar).
type FavStar struct {
	BaseModel
	UUID      string     `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	UserID    int        `gorm:"column:user_id" json:"user_id"`
	ClassName string     `gorm:"column:class_name" json:"class_name"`
	ObjID     int        `gorm:"column:obj_id" json:"obj_id"`
	Dttm      *time.Time `gorm:"column:dttm" json:"dttm"`
}

// TableName returns the original table name.
func (FavStar) TableName() string { return "favstar" }

// DynamicPlugin maps the dynamic_plugin table (superset.models.dynamic_plugins.DynamicPlugin).
type DynamicPlugin struct {
	BaseModel
	Name      string `gorm:"column:name;unique;not null" json:"name"`
	Key       string `gorm:"column:key;unique;not null" json:"key"`
	BundleURL string `gorm:"column:bundle_url;unique;not null" json:"bundle_url"`
}

// TableName returns the original table name.
func (DynamicPlugin) TableName() string { return "dynamic_plugin" }

// UserAttribute maps the user_attribute table (superset.models.user_attributes.UserAttribute).
type UserAttribute struct {
	BaseModel
	UserID                int        `gorm:"column:user_id;uniqueIndex:uq_user_attribute_user_id" json:"user_id"`
	WelcomeDashboardID    *int       `gorm:"column:welcome_dashboard_id" json:"welcome_dashboard_id"`
	AvatarURL             string     `gorm:"column:avatar_url" json:"avatar_url"`
	SessionsInvalidatedAt *time.Time `gorm:"column:sessions_invalidated_at;index" json:"sessions_invalidated_at"`
	PasswordMustChange    bool       `gorm:"column:password_must_change;not null;default:false" json:"password_must_change"`
}

// TableName returns the original table name.
func (UserAttribute) TableName() string { return "user_attribute" }
