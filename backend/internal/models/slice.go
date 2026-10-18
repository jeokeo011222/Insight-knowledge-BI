package models

import (
	"time"

	"gorm.io/datatypes"
)

// Slice maps the slices table (superset.models.slice.Slice).
type Slice struct {
	BaseModel
	UUID                 string         `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	DeletedAt            *time.Time     `gorm:"column:deleted_at;index" json:"deleted_at"`
	SliceName            string         `gorm:"column:slice_name" json:"slice_name"`
	DatasourceID         int            `gorm:"column:datasource_id;index:ix_slices_datasource_type_datasource_id" json:"datasource_id"`
	DatasourceType       string         `gorm:"column:datasource_type;index:ix_slices_datasource_type_datasource_id" json:"datasource_type"`
	DatasourceName       string         `gorm:"column:datasource_name" json:"datasource_name"`
	VizType              string         `gorm:"column:viz_type" json:"viz_type"`
	Params               datatypes.JSON `gorm:"column:params" json:"params"`
	QueryContext         datatypes.JSON `gorm:"column:query_context" json:"query_context"`
	Description          string         `gorm:"column:description" json:"description"`
	CacheTimeout         *int           `gorm:"column:cache_timeout" json:"cache_timeout"`
	Perm                 string         `gorm:"column:perm" json:"perm"`
	SchemaPerm           string         `gorm:"column:schema_perm" json:"schema_perm"`
	CatalogPerm          string         `gorm:"column:catalog_perm" json:"catalog_perm"`
	LastSavedAt          *time.Time     `gorm:"column:last_saved_at" json:"last_saved_at"`
	LastSavedByFk        *int           `gorm:"column:last_saved_by_fk" json:"last_saved_by_fk"`
	CertifiedBy          string         `gorm:"column:certified_by" json:"certified_by"`
	CertificationDetails string         `gorm:"column:certification_details" json:"certification_details"`
	IsManagedExternally  bool           `gorm:"column:is_managed_externally;not null;default:false" json:"is_managed_externally"`
	ExternalURL          string         `gorm:"column:external_url" json:"external_url"`
}

// TableName returns the original table name.
func (Slice) TableName() string { return "slices" }

// SliceUser maps the slice_user association table.
type SliceUser struct {
	UserID  int `gorm:"column:user_id;primaryKey" json:"user_id"`
	SliceID int `gorm:"column:slice_id;primaryKey" json:"slice_id"`
}

// TableName returns the original table name.
func (SliceUser) TableName() string { return "slice_user" }
