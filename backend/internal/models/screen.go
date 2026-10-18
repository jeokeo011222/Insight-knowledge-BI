package models

import (
	"time"

	"gorm.io/datatypes"
)

// Screen is a data-visualization wall (数据大屏).
//
// Unlike a Dashboard — which is a responsive row/grid layout driven by
// position_json — a Screen is an *absolutely positioned* canvas with a fixed
// design resolution (1920×1080 by default) that is scaled to the viewport at
// render time. Its whole document (components, layers, filters, interaction
// settings) lives in ConfigJSON so the editor can save atomically and a
// template is just a config blob.
type Screen struct {
	BaseModel
	UUID        string         `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	Name        string         `gorm:"column:name;not null" json:"name"`
	Slug        string         `gorm:"column:slug;index" json:"slug"`
	Description string         `gorm:"column:description" json:"description"`
	Category    string         `gorm:"column:category;size:32;index" json:"category"`
	Width       int            `gorm:"column:width;default:1920" json:"width"`
	Height      int            `gorm:"column:height;default:1080" json:"height"`
	ScaleMode   string         `gorm:"column:scale_mode;size:16;default:fit" json:"scale_mode"`
	Theme       string         `gorm:"column:theme;size:32" json:"theme"`
	Background  string         `gorm:"column:background;type:text" json:"background"`
	ConfigJSON  datatypes.JSON `gorm:"column:config_json" json:"config_json"`
	Thumbnail   string         `gorm:"column:thumbnail;type:text" json:"thumbnail"`
	Published   bool           `gorm:"column:published;default:false" json:"published"`
	TemplateKey string         `gorm:"column:template_key;size:64" json:"template_key"`
	OwnerID     int            `gorm:"column:owner_id;index" json:"owner_id"`
	RefreshSec  int            `gorm:"column:refresh_sec;default:0" json:"refresh_sec"`
	ConfigRev   int            `gorm:"column:config_rev;default:1" json:"config_rev"`
	// Xinchuang marks a screen that must stay within domestic-stack engines
	// (达梦 / 人大金仓 / GaussDB / openGauss). The editor warns when a bound
	// chart points at a non-approved engine.
	Xinchuang bool `gorm:"column:xinchuang;default:false" json:"xinchuang"`

	// Public sharing (the "大屏投屏" story: a TV or an iframe must be able to
	// open it without a login).
	PublicEnabled   bool       `gorm:"column:public_enabled;default:false" json:"public_enabled"`
	PublicToken     string     `gorm:"column:public_token;size:64;index" json:"public_token"`
	PublicPassword  string     `gorm:"column:public_password" json:"-"`
	PublicExpiresAt *time.Time `gorm:"column:public_expires_at" json:"public_expires_at"`
	AllowExport     bool       `gorm:"column:allow_export;default:true" json:"allow_export"`
	ViewCount       int64      `gorm:"column:view_count;default:0" json:"view_count"`
}

// TableName keeps the table name short and unambiguous.
func (Screen) TableName() string { return "screens" }

// ScreenRevision stores a bounded history of saved canvas documents so an
// accidental "save" can be rolled back without a database restore.
type ScreenRevision struct {
	BaseModel
	ScreenID   int            `gorm:"column:screen_id;index" json:"screen_id"`
	Rev        int            `gorm:"column:rev" json:"rev"`
	ConfigJSON datatypes.JSON `gorm:"column:config_json" json:"config_json"`
	SavedBy    int            `gorm:"column:saved_by" json:"saved_by"`
	Note       string         `gorm:"column:note;size:255" json:"note"`
}

// TableName returns the revision table name.
func (ScreenRevision) TableName() string { return "screen_revisions" }
