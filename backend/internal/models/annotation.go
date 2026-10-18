package models

import "time"

// AnnotationLayer maps the annotation_layer table (superset.models.annotations.AnnotationLayer).
type AnnotationLayer struct {
	BaseModel
	UUID  string `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	Name  string `gorm:"column:name" json:"name"`
	Descr string `gorm:"column:descr" json:"descr"`
}

// TableName returns the original table name.
func (AnnotationLayer) TableName() string { return "annotation_layer" }

// Annotation maps the annotation table (superset.models.annotations.Annotation).
type Annotation struct {
	BaseModel
	UUID         string     `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	StartDttm    *time.Time `gorm:"column:start_dttm" json:"start_dttm"`
	EndDttm      *time.Time `gorm:"column:end_dttm" json:"end_dttm"`
	LayerID      int        `gorm:"column:layer_id;not null;index:ti_dag_state" json:"layer_id"`
	ShortDescr   string     `gorm:"column:short_descr" json:"short_descr"`
	LongDescr    string     `gorm:"column:long_descr" json:"long_descr"`
	JSONMetadata string     `gorm:"column:json_metadata" json:"json_metadata"`
}

// TableName returns the original table name.
func (Annotation) TableName() string { return "annotation" }
