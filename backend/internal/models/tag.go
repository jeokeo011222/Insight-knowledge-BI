package models

// Tag maps the tag table (superset.tags.models.Tag).
type Tag struct {
	BaseModel
	Name        string `gorm:"column:name;unique" json:"name"`
	Type        int    `gorm:"column:type" json:"type"`
	Description string `gorm:"column:description" json:"description"`
}

// TableName returns the original table name.
func (Tag) TableName() string { return "tag" }

// TaggedObject maps the tagged_object table (superset.tags.models.TaggedObject).
type TaggedObject struct {
	ID         int `gorm:"column:id;primaryKey" json:"id"`
	TagID      int `gorm:"column:tag_id;uniqueIndex:uix_tagged_object" json:"tag_id"`
	ObjectID   int `gorm:"column:object_id;uniqueIndex:uix_tagged_object" json:"object_id"`
	ObjectType int `gorm:"column:object_type;uniqueIndex:uix_tagged_object" json:"object_type"`
}

// TableName returns the original table name.
func (TaggedObject) TableName() string { return "tagged_object" }

// UserFavoriteTag maps the user_favorite_tag association table.
type UserFavoriteTag struct {
	UserID int `gorm:"column:user_id;primaryKey" json:"user_id"`
	TagID  int `gorm:"column:tag_id;primaryKey" json:"tag_id"`
}

// TableName returns the original table name.
func (UserFavoriteTag) TableName() string { return "user_favorite_tag" }
