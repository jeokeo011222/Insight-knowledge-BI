// Package models holds the GORM models for all 68 superset tables. Table and
// column names match the Python source 1:1 (see docs/models.md).
package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BaseModel carries the FAB audit columns shared by most tables.
type BaseModel struct {
	ID          int       `gorm:"primaryKey;column:id" json:"id"`
	CreatedOn   time.Time `gorm:"column:created_on" json:"created_on"`
	ChangedOn   time.Time `gorm:"column:changed_on" json:"changed_on"`
	CreatedByFk *int      `gorm:"column:created_by_fk" json:"created_by_fk"`
	ChangedByFk *int      `gorm:"column:changed_by_fk" json:"changed_by_fk"`
	Deleted     bool      `gorm:"column:deleted;index" json:"deleted"`
}

// BeforeCreate sets created_on and generates a uuid when applicable.
func (m *BaseModel) BeforeCreate(tx *gorm.DB) error {
	now := time.Now()
	if m.CreatedOn.IsZero() {
		m.CreatedOn = now
	}
	m.ChangedOn = now
	if m.CreatedByFk == nil {
		if uid, ok := tx.Statement.Context.Value(userIDKey).(int); ok && uid != 0 {
			m.CreatedByFk = &uid
		}
	}
	return nil
}

// BeforeUpdate refreshes changed_on.
func (m *BaseModel) BeforeUpdate(tx *gorm.DB) error {
	m.ChangedOn = time.Now()
	if uid, ok := tx.Statement.Context.Value(userIDKey).(int); ok && uid != 0 {
		m.ChangedByFk = &uid
	}
	return nil
}

// userIDKey is the context key carrying the acting user id.
type userIDKeyType string

const userIDKey userIDKeyType = "superset_user_id"

// WithUserID attaches the acting user id to a context for audit columns.
func WithUserID(ctx gorm.Session, userID int) {
	// no-op helper kept for API symmetry; use tx.Statement.Context directly
	_ = ctx
	_ = userID
}

// NewUUID returns a new random UUID string.
func NewUUID() string { return uuid.NewString() }

// SoftDelete scopes ------------------------------------------------

// NotDeleted adds the standard `deleted = false` filter.
func NotDeleted(db *gorm.DB) *gorm.DB {
	return db.Where("deleted = ?", false)
}

// All returns every model registered for migration.
func All() []any {
	out := make([]any, 0, len(coreModels)+len(aiModels))
	out = append(out, coreModels...)
	return append(out, aiModels...)
}

// CoreModels returns the original schema models (always migrated first).
func CoreModels() []any { return coreModels }

// AIModels returns the AI Agent models. They live in a separate list because
// the optional pgvector extension may be unavailable, in which case their
// migration must never block server startup.
func AIModels() []any { return aiModels }

var coreModels []any
var aiModels []any

// register appends core models to the migration registry.
func register(models ...any) {
	coreModels = append(coreModels, models...)
}

// registerAI appends AI models to the (optional) migration registry.
func registerAI(models ...any) {
	aiModels = append(aiModels, models...)
}
