// Package examples recreates the sample content that ships with upstream
// Superset (`superset load_examples`): a small examples database holding the
// birth_names table, the matching dataset, the six demo charts and the
// "Birth Names Overview" dashboard.
//
// Every step looks its row up before writing and leaves an existing row
// untouched, so a second run only reports what is already there. That makes
// the loader safe to call on every start (scripts/start.* does exactly that)
// and on an installation whose example assets were created by hand.
package examples

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/config"
	"superset/internal/connectors"
	"superset/internal/datasets"
	"superset/internal/models"
)

const (
	// DatabaseName is the fixed name of the sample connection row (upstream
	// calls it "examples" too). Keeping the name stable is what lets the
	// loader find its own row again on the next run.
	DatabaseName = "examples"
	// schema is the default schema the sample tables live in.
	schema = "public"
	// tableName is the sample table the shipped charts read from.
	tableName = "birth_names"

	dashboardTitle = "Birth Names Overview"
	dashboardSlug  = "birth-names"
)

// Load materialises the example content and returns the first error it meets.
// The CLI turns that into a failure; the start scripts only warn, because an
// unreachable examples database must not keep the application from booting.
func Load(ctx context.Context, db *gorm.DB, cfg *config.Config, log *zap.Logger) error {
	if log == nil {
		log = zap.NewNop()
	}
	uri := strings.TrimSpace(cfg.SQLAlchemyExamplesURI)
	if uri == "" {
		return fmt.Errorf("SQLALCHEMY_EXAMPLES_URI is not configured")
	}

	conn, spec, err := connectors.Open(uri)
	if err != nil {
		return fmt.Errorf("could not open the examples database: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := ensureTable(ctx, conn, spec, log); err != nil {
		return err
	}
	database, err := ensureDatabase(ctx, db, uri, log)
	if err != nil {
		return err
	}
	dataset, err := ensureDataset(ctx, db, cfg, database, log)
	if err != nil {
		return err
	}
	if err := ensureCharts(ctx, db, database.DatabaseName, dataset, log); err != nil {
		return err
	}
	if err := ensureDashboard(ctx, db, dataset, log); err != nil {
		return err
	}

	log.Info("example content ready",
		zap.String("database", database.DatabaseName),
		zap.String("dataset", dataset.TableNameStr),
		zap.Int("dataset_id", dataset.ID))
	return nil
}

// ensureTable creates the sample table when it is missing and fills it only
// when it is still empty, so a re-run never duplicates rows.
func ensureTable(ctx context.Context, conn *sql.DB, spec connectors.EngineSpec, log *zap.Logger) error {
	qualified := spec.QuoteIdentifier(schema) + "." + spec.QuoteIdentifier(tableName)
	ddl := "CREATE TABLE IF NOT EXISTS " + qualified + " (" +
		strings.Join(birthNamesDDL(spec), ", ") + ")"
	if _, err := conn.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("could not create %s: %w", tableName, err)
	}

	var present int64
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+qualified).Scan(&present); err != nil {
		return fmt.Errorf("could not count the rows of %s: %w", tableName, err)
	}
	if present > 0 {
		log.Info("example table already loaded",
			zap.String("table", tableName), zap.Int64("rows", present))
		return nil
	}

	rows := birthNamesRows()
	columns := []string{"id", "ds", "gender", "name", "num", "state"}
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = spec.QuoteIdentifier(c)
	}
	prefix := "INSERT INTO " + qualified + " (" + strings.Join(quoted, ", ") + ") VALUES "

	const batch = 200
	for start := 0; start < len(rows); start += batch {
		end := start + batch
		if end > len(rows) {
			end = len(rows)
		}
		values := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*len(columns))
		marker := 0
		for _, row := range rows[start:end] {
			placeholders := make([]string, len(columns))
			for j := range columns {
				marker++
				placeholders[j] = placeholder(spec.Engine(), marker)
			}
			values = append(values, "("+strings.Join(placeholders, ", ")+")")
			args = append(args, row...)
		}
		if _, err := conn.ExecContext(ctx, prefix+strings.Join(values, ", "), args...); err != nil {
			return fmt.Errorf("could not insert rows into %s: %w", tableName, err)
		}
	}
	log.Info("example table loaded", zap.String("table", tableName), zap.Int("rows", len(rows)))
	return nil
}

// placeholder renders the n-th bind marker. PostgreSQL numbers its markers
// ($1, $2, …) while every other supported driver uses "?".
func placeholder(engine string, n int) string {
	switch strings.ToLower(engine) {
	case "postgresql", "postgres":
		return fmt.Sprintf("$%d", n)
	default:
		return "?"
	}
}

// birthNamesDDL describes the sample table in the engine's own dialect.
func birthNamesDDL(spec connectors.EngineSpec) []string {
	return []string{
		spec.QuoteIdentifier("id") + " " + spec.ColumnTypeMap("INTEGER"),
		spec.QuoteIdentifier("ds") + " " + spec.ColumnTypeMap("TIMESTAMP"),
		spec.QuoteIdentifier("gender") + " " + spec.ColumnTypeMap("VARCHAR"),
		spec.QuoteIdentifier("name") + " " + spec.ColumnTypeMap("VARCHAR"),
		spec.QuoteIdentifier("num") + " " + spec.ColumnTypeMap("INTEGER"),
		spec.QuoteIdentifier("state") + " " + spec.ColumnTypeMap("VARCHAR"),
	}
}

// ensureDatabase returns the sample connection, creating the row on first use.
// The URI comes from SQLALCHEMY_EXAMPLES_URI, which upstream Superset uses for
// the very same purpose.
func ensureDatabase(ctx context.Context, db *gorm.DB, uri string, log *zap.Logger) (*models.Database, error) {
	var existing models.Database
	err := db.WithContext(ctx).Where("database_name = ?", DatabaseName).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	created := models.Database{
		UUID:                uuid.NewString(),
		VerboseName:         DatabaseName,
		DatabaseName:        DatabaseName,
		SQLAlchemyURI:       uri,
		ConfigurationMethod: "sqlalchemy_form",
		ExposeInSQLlab:      true,
		ExploreDatabase:     true,
		AllowCTAS:           true,
		AllowDML:            true,
	}
	if err := db.WithContext(ctx).Create(&created).Error; err != nil {
		return nil, fmt.Errorf("could not create the examples database row: %w", err)
	}
	log.Info("examples database row created", zap.String("name", DatabaseName))
	return &created, nil
}

// ensureDataset registers the sample table as a dataset, reflecting its columns
// from the engine catalog so the dataset carries real types instead of guesses.
func ensureDataset(ctx context.Context, db *gorm.DB, cfg *config.Config, database *models.Database, log *zap.Logger) (*models.SqlaTable, error) {
	var existing models.SqlaTable
	err := db.WithContext(ctx).
		Where("database_id = ? AND schema = ? AND table_name = ? AND deleted_at IS NULL",
			database.ID, schema, tableName).First(&existing).Error
	if err == nil {
		return &existing, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	t := models.SqlaTable{
		UUID:                uuid.NewString(),
		TableNameStr:        tableName,
		DatabaseID:          database.ID,
		Schema:              schema,
		MainDttmCol:         "ds",
		Description:         "Sample birth names",
		IsFeatured:          true,
		FilterSelectEnabled: true,
	}
	t.SchemaPerm = fmt.Sprintf("[%s].[%s](id:%d)", database.DatabaseName, schema, 0)
	t.Perm = fmt.Sprintf("[%s].[%s](id:%d)", database.DatabaseName, tableName, 0)
	if err := db.WithContext(ctx).Create(&t).Error; err != nil {
		return nil, fmt.Errorf("could not create the %s dataset: %w", tableName, err)
	}

	// Reuse the refresh pipeline: it reads the column catalog, writes the
	// table_columns rows and pins main_dttm_col to a real temporal column.
	refresher := datasets.New(db, cfg, log, nil)
	if _, err := refresher.Refresh(ctx, t.ID, "examples"); err != nil {
		return nil, fmt.Errorf("could not reflect the columns of %s: %w", tableName, err)
	}

	var metricCount int64
	if err := db.WithContext(ctx).Model(&models.SqlMetric{}).
		Where("table_id = ? AND metric_name = ?", t.ID, "count").Count(&metricCount).Error; err != nil {
		return nil, err
	}
	if metricCount == 0 {
		if err := db.WithContext(ctx).Create(&models.SqlMetric{
			UUID: uuid.NewString(), MetricName: "count", VerboseName: "Count",
			MetricType: "count", Expression: "COUNT(*)", TableID: t.ID,
		}).Error; err != nil {
			return nil, fmt.Errorf("could not create the count metric: %w", err)
		}
	}

	log.Info("example dataset created",
		zap.String("dataset", tableName), zap.Int("dataset_id", t.ID))
	return &t, nil
}

// ensureCharts creates the six demo charts. A chart is identified by name, so
// an installation that already has them (or renamed them) is left alone.
func ensureCharts(ctx context.Context, db *gorm.DB, dbName string, t *models.SqlaTable, log *zap.Logger) error {
	perm := fmt.Sprintf("[%s].[%s](id:%d)", dbName, t.TableNameStr, t.ID)
	schemaPerm := fmt.Sprintf("[%s].[%s]", dbName, t.Schema)
	for _, def := range chartDefs() {
		var existing models.Slice
		err := db.WithContext(ctx).
			Where("slice_name = ? AND deleted_at IS NULL", def.name).First(&existing).Error
		if err == nil {
			continue
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		slice := models.Slice{
			UUID:           uuid.NewString(),
			SliceName:      def.name,
			DatasourceID:   t.ID,
			DatasourceType: "table",
			DatasourceName: t.TableNameStr,
			VizType:        def.vizType,
			Params:         datatypes.JSON(fmt.Sprintf(def.params, t.ID)),
			Description:    def.description,
			Perm:           perm,
			SchemaPerm:     schemaPerm,
		}
		if err := db.WithContext(ctx).Create(&slice).Error; err != nil {
			return fmt.Errorf("could not create the chart %q: %w", def.name, err)
		}
		log.Info("example chart created", zap.String("chart", def.name))
	}
	return nil
}

// ensureDashboard builds the overview dashboard out of the charts this
// installation actually has, so the grid never points at a missing chart id.
func ensureDashboard(ctx context.Context, db *gorm.DB, t *models.SqlaTable, log *zap.Logger) error {
	var existing models.Dashboard
	err := db.WithContext(ctx).
		Where("dashboard_title = ? AND deleted_at IS NULL", dashboardTitle).First(&existing).Error
	if err == nil {
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}

	var slices []models.Slice
	if err := db.WithContext(ctx).
		Where("datasource_id = ? AND deleted_at IS NULL", t.ID).Find(&slices).Error; err != nil {
		return err
	}
	byName := make(map[string]int, len(slices))
	for _, s := range slices {
		byName[s.SliceName] = s.ID
	}

	positions, err := json.Marshal(dashboardPositions(byName))
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(dashboardMetadata(t.ID))
	if err != nil {
		return err
	}

	dashboard := models.Dashboard{
		UUID:           uuid.NewString(),
		DashboardTitle: dashboardTitle,
		Slug:           dashboardSlug,
		Description:    "示例看板：出生人口按月份、性别与州别的分布，用于快速体验探索分析与可视化。",
		PositionJSON:   datatypes.JSON(positions),
		JSONMetadata:   datatypes.JSON(metadata),
		Published:      true,
	}
	if err := db.WithContext(ctx).Create(&dashboard).Error; err != nil {
		return fmt.Errorf("could not create the example dashboard: %w", err)
	}
	for _, s := range slices {
		if err := db.WithContext(ctx).Create(&models.DashboardSlices{
			DashboardID: dashboard.ID, SliceID: s.ID,
		}).Error; err != nil {
			return fmt.Errorf("could not attach chart %d to the dashboard: %w", s.ID, err)
		}
	}
	log.Info("example dashboard created",
		zap.String("dashboard", dashboardTitle), zap.Int("charts", len(slices)))
	return nil
}
