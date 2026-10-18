package models

// ExtensionStorage maps the extension_storage table
// (superset.extensions.storage.persistent_model.ExtensionStorage).
type ExtensionStorage struct {
	BaseModel
	UUID         string `gorm:"column:uuid;type:uuid;unique;not null" json:"uuid"`
	ExtensionID  string `gorm:"column:extension_id;not null" json:"extension_id"`
	UserFk       *int   `gorm:"column:user_fk" json:"user_fk"`
	ResourceType string `gorm:"column:resource_type" json:"resource_type"`
	ResourceUUID string `gorm:"column:resource_uuid" json:"resource_uuid"`
	Key          string `gorm:"column:key;not null" json:"key"`
	Value        []byte `gorm:"column:value;not null" json:"-"`
	ValueSize    int    `gorm:"column:value_size;not null" json:"value_size"`
	Codec        string `gorm:"column:codec;not null;default:json" json:"codec"`
	IsEncrypted  bool   `gorm:"column:is_encrypted;not null;default:false" json:"is_encrypted"`
}

// TableName returns the original table name.
func (ExtensionStorage) TableName() string { return "extension_storage" }
