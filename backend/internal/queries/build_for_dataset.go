package queries

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
)

// build_for_dataset.go exposes the chart SQL builder to callers that address a
// dataset by id instead of by query context (benchmark W-06).
//
// The Agent's semantic_query must produce the *same* SQL a chart would for the
// same intent. Re-implementing quoting, grouping and limits in the AI package
// would guarantee a slow drift where "the chart can do it but the Agent cannot",
// which is exactly the complaint W-06 exists to remove.

// BuildDatasetSQL loads a dataset and renders one query object against its
// dialect. The caller executes the result through ExecuteReadOnlySQL, which
// applies RLS and masking — so this function deliberately does not re-apply
// them, otherwise the predicates would nest twice.
func BuildDatasetSQL(ctx context.Context, db *gorm.DB, datasetID int, q *QueryObject, opts BuildOptions) (*BuiltQuery, *DatasetInfo, error) {
	if datasetID <= 0 {
		return nil, nil, apperrors.BadRequest("dataset_id is required")
	}
	if q == nil {
		return nil, nil, apperrors.BadRequest("Query object is required")
	}
	var table models.SqlaTable
	if err := db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", datasetID).First(&table).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, apperrors.NotFound("数据集 %d 不存在", datasetID)
		}
		return nil, nil, apperrors.Internal("读取数据集失败：%s", err.Error())
	}
	var columns []models.TableColumn
	if err := db.WithContext(ctx).Where("table_id = ?", datasetID).Order("column_name").Find(&columns).Error; err != nil {
		return nil, nil, apperrors.Internal("读取数据集列失败：%s", err.Error())
	}
	var metrics []models.SqlMetric
	if err := db.WithContext(ctx).Where("table_id = ?", datasetID).Order("metric_name").Find(&metrics).Error; err != nil {
		return nil, nil, apperrors.Internal("读取数据集指标失败：%s", err.Error())
	}
	var database models.Database
	if err := db.WithContext(ctx).First(&database, table.DatabaseID).Error; err != nil {
		return nil, nil, apperrors.Internal("读取数据库连接失败：%s", err.Error())
	}
	spec, err := engineSpec(database.SQLAlchemyURI)
	if err != nil {
		return nil, nil, err
	}
	ds := &DatasetInfo{Table: &table, Columns: columns, Metrics: metrics}
	built, err := BuildSQL(ds, q, spec, opts)
	if err != nil {
		return nil, ds, err
	}
	return built, ds, nil
}

// ColumnNames returns the physical column names of a dataset, used to validate
// semantic fields before they reach the builder.
func ColumnNames(ctx context.Context, db *gorm.DB, datasetID int) (map[string]bool, error) {
	var cols []models.TableColumn
	if err := db.WithContext(ctx).
		Where("table_id = ? AND is_active = ?", datasetID, true).Find(&cols).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(cols))
	for _, c := range cols {
		out[strings.ToLower(c.ColumnName)] = true
	}
	return out, nil
}
