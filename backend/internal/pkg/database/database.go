// Package database initializes the GORM connection to the metadata database
// and runs migrations (superset init / db upgrade).
package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"superset/internal/models"
)

// Connect opens the metadata database connection.
func Connect(dsn string, debug bool) (*gorm.DB, error) {
	lg := logger.Default
	if debug {
		lg = logger.Default.LogMode(logger.Info)
	} else {
		lg = lg.LogMode(logger.Silent)
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:                                   lg,
		DisableForeignKeyConstraintWhenMigrating: true,
		PrepareStmt:                              true,
	})
	if err != nil {
		return nil, fmt.Errorf("connect metadata db: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(time.Hour)
	return db, nil
}

// Migrate creates or updates the schema for all registered models.
//
// The core schema must migrate successfully. The AI Agent tables are optional
// infrastructure: the pgvector extension may be absent on the metadata
// database, so their migration (and the best-effort extension creation) never
// blocks server startup.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(models.CoreModels()...); err != nil {
		return err
	}
	// Best-effort: enable pgvector for ai_meta_embedding. Requires the
	// extension to be installed in the PostgreSQL server.
	_ = db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error
	if err := db.AutoMigrate(models.AIModels()...); err != nil {
		// GORM runs the batch in one transaction, so a single unsupported model
		// (ai_meta_embedding needs pgvector) rolled back *every* AI table — the
		// batch silently stopped creating new ones while the old ones survived,
		// which made a newly registered table invisible with no error in sight.
		// Migrating them one by one means a missing extension costs exactly one
		// table (the vector one) instead of the whole plane.
		var failed []string
		for _, model := range models.AIModels() {
			if mErr := db.AutoMigrate(model); mErr != nil {
				failed = append(failed, fmt.Sprintf("%T: %v", model, mErr))
			}
		}
		if len(failed) > 0 {
			db.Logger.Warn(context.Background(), "ai tables migration skipped: %v", failed)
		}
	}
	migrateSemanticIndexes(db)
	seedDefaultThemes(db)
	seedDefaultGovernance(db)
	// Seeds and DDL above leave every pooled session holding PostgreSQL
	// prepared statements described against the *old* column set.
	recyclePool(db)
	return nil
}

// migrateSemanticIndexes converts the MDL-lite unique keys to partial indexes.
//
// Both keys must ignore soft-deleted rows, because deletion in this schema is a
// flag rather than a DELETE:
//   - semantic_models: "delete the model, then regenerate it from the dataset"
//     used to fail with a duplicate key naming a row the user could no longer
//     see (409 forever).
//   - semantic_fields: saving a model replaces its whole field list (soft-delete
//   - re-insert the same names), so every save returned 500.
//
// AutoMigrate cannot express partial indexes, hence the explicit DDL. It runs on
// every boot and is idempotent: DROP IF EXISTS + CREATE IF NOT EXISTS.
func migrateSemanticIndexes(db *gorm.DB) {
	stmts := []string{
		`DROP INDEX IF EXISTS uq_semantic_models_dataset_name`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_semantic_models_dataset_name
			ON semantic_models (dataset_id, name) WHERE deleted = false`,
		`DROP INDEX IF EXISTS uq_semantic_fields_model_name`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_semantic_fields_model_name
			ON semantic_fields (model_id, name) WHERE deleted = false`,
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			db.Logger.Warn(context.Background(), "semantic index migration failed: %v", err)
		}
	}
}

// recyclePool closes the idle sessions of the metadata pool.
//
// AutoMigrate emits DDL (ADD COLUMN). PostgreSQL pins the result shape into the
// prepared statements of each session, so a session that prepared a statement
// before the DDL keeps failing with SQLSTATE 0A000 ("cached plan must not
// change result type") — and because GORM runs with PrepareStmt and the pool
// keeps sessions for an hour, that surfaces as an unrelated 500 on any endpoint
// touching the migrated table (e.g. "Could not load columns"). Dropping the
// idle sessions discards the server side plans; database/sql prepares again on
// the next use. Migrations run before the server starts serving, so no session
// is in flight here.
func recyclePool(db *gorm.DB) {
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	sqlDB.SetMaxIdleConns(0)
	sqlDB.SetMaxIdleConns(10)
}

// seedDefaultGovernance inserts a few example governance records the first time
// the tables are empty, so the row-level-security and annotation screens are
// not blank on a fresh install. The RLS example deliberately carries no role or
// dataset associations: an unassociated filter is inert, so seeding can never
// change what an existing user is allowed to query.
func seedDefaultGovernance(db *gorm.DB) {
	var rlsCount int64
	if err := db.Model(&models.RowLevelSecurityFilter{}).Count(&rlsCount).Error; err == nil && rlsCount == 0 {
		db.Create(&models.RowLevelSecurityFilter{
			Name:        "示例：按区域过滤",
			Description: "示例规则，编辑并关联角色 / 数据集后才会生效",
			FilterType:  "Regular",
			Clause:      "region = '{{ current_user_name }}'",
		})
	}

	var layerCount int64
	if err := db.Model(&models.AnnotationLayer{}).Count(&layerCount).Error; err == nil && layerCount == 0 {
		layer := models.AnnotationLayer{
			UUID:  uuid.NewString(),
			Name:  "示例图层：产品发布",
			Descr: "在时间轴上标记产品发布 / 运营活动等节点",
		}
		if db.Create(&layer).Error == nil {
			now := time.Now().UTC()
			start := now.AddDate(0, -1, 0)
			db.Create(&models.Annotation{
				UUID: uuid.NewString(), LayerID: layer.ID,
				ShortDescr: "v3.0 发布", LongDescr: "AI 智能体能力上线",
				StartDttm: &start, EndDttm: timePtr(start.Add(48 * time.Hour)),
			})
			start2 := now.AddDate(0, 0, -3)
			db.Create(&models.Annotation{
				UUID: uuid.NewString(), LayerID: layer.ID,
				ShortDescr: "双十一大促", LongDescr: "流量高峰观测窗口",
				StartDttm: &start2, EndDttm: timePtr(start2.Add(72 * time.Hour)),
			})
		}
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// seedDefaultThemes creates the built-in system themes the first time the
// themes table is empty (upstream seeds "Superset default" / "Superset dark";
// here they carry the 智观 BI brand tokens so the shipped look is preserved
// while admins gain an editable starting point).
func seedDefaultThemes(db *gorm.DB) {
	var count int64
	if err := db.Model(&models.Theme{}).Count(&count).Error; err != nil || count > 0 {
		return
	}
	lightToken := map[string]any{
		"colorPrimary": "#1b5fd9", "colorInfo": "#22c3e6",
		"colorSuccess": "#28a06b", "colorWarning": "#e8a417", "colorError": "#dc3f4e",
		"colorBgBase": "#ffffff", "colorBgLayout": "#f4f5f7",
		"colorText": "#232629", "colorBorder": "#e6e8eb",
		"borderRadius": 6, "fontSize": 13,
		"fontFamily":     "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", Roboto, sans-serif",
		"fontFamilyCode": "\"JetBrains Mono\", \"SFMono-Regular\", Consolas, monospace",
	}
	darkToken := map[string]any{
		"colorPrimary": "#1b5fd9", "colorInfo": "#22c3e6",
		"colorSuccess": "#28a06b", "colorWarning": "#e8a417", "colorError": "#dc3f4e",
		"colorBgBase": "#0f1d2e", "colorBgLayout": "#0a1420",
		"colorText": "#e8f0f6", "colorBorder": "#1f3550",
		"borderRadius": 6, "fontSize": 13,
		"fontFamily":     "-apple-system, BlinkMacSystemFont, \"PingFang SC\", \"Microsoft YaHei\", \"Segoe UI\", Roboto, sans-serif",
		"fontFamilyCode": "\"JetBrains Mono\", \"SFMono-Regular\", Consolas, monospace",
	}
	lightJSON, _ := json.Marshal(map[string]any{"token": lightToken, "algorithm": "default"})
	darkJSON, _ := json.Marshal(map[string]any{"token": darkToken, "algorithm": "dark"})
	db.Create(&models.Theme{
		UUID: uuid.NewString(), ThemeName: "智观默认",
		JSONData: string(lightJSON), IsSystem: true, IsSystemDefault: true,
	})
	db.Create(&models.Theme{
		UUID: uuid.NewString(), ThemeName: "智观暗色",
		JSONData: string(darkJSON), IsSystem: true, IsSystemDark: true,
	})
}
