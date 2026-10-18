// Package datasets owns the data-refresh lifecycle of a dataset: what "refresh"
// means for each kind of datasource, how the outcome is recorded, and the
// background loop that keeps auto-refresh intervals honest.
//
// Refresh semantics differ by dataset kind, and conflating them is what makes
// "refresh" buttons confusing:
//
//   - external (uploaded file / REST API): the source is re-pulled, the managed
//     table is rewritten and its columns re-synced. This is the only kind that
//     actually moves rows.
//   - virtual (saved SQL): the SQL is re-probed to pick up schema drift; no rows
//     are stored, so there is nothing else to refresh.
//   - physical (a table living in the source database): the column metadata is
//     re-read from the source catalog.
//
// Every refresh also invalidates the chart-data cache: cached results were
// produced from the previous state of the data.
package datasets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"superset/internal/config"
	"superset/internal/ingest"
	"superset/internal/models"
	"superset/internal/pkg/cache"
	"superset/internal/queries"
)

// Refresh lifecycle values, mirrored into SqlaTable.LastRefreshStatus.
const (
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"

	TriggerManual   = "manual"
	TriggerSchedule = "schedule"
)

// Interval bounds. A sub-minute period would hammer the source; 30 days is the
// practical ceiling for "keep this table warm".
const (
	MinIntervalSec = 60
	MaxIntervalSec = 30 * 24 * 3600
)

// DefaultScanInterval is how often the scheduler looks for due datasets.
const DefaultScanInterval = 30 * time.Second

// maxConcurrentRefreshes bounds parallelism so a large installation does not
// open dozens of source-database connections at once.
const maxConcurrentRefreshes = 2

// Source describes where an external dataset's rows came from. It is persisted
// in SqlaTable.extra as {"source": {...}} and is intentionally secret-free.
type Source struct {
	Type       string             `json:"type"` // file | api
	Filename   string             `json:"filename,omitempty"`
	SourcePath string             `json:"source_path,omitempty"`
	Sheet      string             `json:"sheet,omitempty"`
	UploadedOn string             `json:"uploaded_on,omitempty"`
	API        *ingest.RESTSource `json:"api,omitempty"`
}

// SourceOf extracts the external source description from a dataset, if any.
func SourceOf(t *models.SqlaTable) (*Source, bool) {
	if t == nil || strings.TrimSpace(t.Extra) == "" {
		return nil, false
	}
	var payload struct {
		Source *Source `json:"source"`
	}
	if err := json.Unmarshal([]byte(t.Extra), &payload); err != nil {
		return nil, false
	}
	if payload.Source == nil || payload.Source.Type == "" {
		return nil, false
	}
	return payload.Source, true
}

// EncodeExtra renders a source description as the dataset's extra JSON. Any
// unrelated keys already present in extra are preserved.
func EncodeExtra(existing string, src *Source) string {
	out := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		_ = json.Unmarshal([]byte(existing), &out)
	}
	out["source"] = src
	raw, err := json.Marshal(out)
	if err != nil {
		return existing
	}
	return string(raw)
}

// ErrNotFound marks a dataset that no longer exists (or was soft-deleted).
var ErrNotFound = errors.New("dataset not found")

// Outcome is the observable result of one refresh run.
type Outcome struct {
	DatasetID      int       `json:"dataset_id"`
	Trigger        string    `json:"trigger"`
	Kind           string    `json:"kind"`
	RefreshedAt    time.Time `json:"refreshed_at"`
	DurationMS     int64     `json:"duration_ms"`
	ColumnsAdded   int       `json:"columns_added"`
	ColumnsUpdated int       `json:"columns_updated"`
	Rows           int64     `json:"rows"`
}

// Service refreshes datasets and schedules their automatic refreshes.
type Service struct {
	db    *gorm.DB
	cfg   *config.Config
	log   *zap.Logger
	cache *cache.Client

	running sync.Map // dataset id -> struct{}, guards re-entrancy
}

// New builds a refresh service. The cache client may be nil (the beat process
// runs without Redis); cache invalidation is then skipped.
func New(db *gorm.DB, cfg *config.Config, log *zap.Logger, c *cache.Client) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{db: db, cfg: cfg, log: log, cache: c}
}

// Refresh runs one refresh cycle for a dataset and records the outcome,
// including failures, so the UI can show a truthful state instead of the last
// successful run.
func (s *Service) Refresh(ctx context.Context, datasetID int, trigger string) (*Outcome, error) {
	start := time.Now()
	out := &Outcome{DatasetID: datasetID, Trigger: trigger}

	var dataset models.SqlaTable
	err := s.db.WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", datasetID).First(&dataset).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load dataset: %w", err)
	}

	kind, added, updated, rows, refreshErr := s.refreshDataset(ctx, &dataset)
	out.Kind = kind
	out.ColumnsAdded = added
	out.ColumnsUpdated = updated
	out.Rows = rows
	out.DurationMS = time.Since(start).Milliseconds()
	out.RefreshedAt = time.Now().UTC()

	status, errText := StatusSuccess, ""
	if refreshErr != nil {
		status, errText = StatusFailed, truncateError(refreshErr.Error())
		s.log.Warn("dataset refresh failed",
			zap.Int("dataset_id", datasetID),
			zap.String("trigger", trigger),
			zap.Error(refreshErr))
	} else {
		s.invalidateChartCache(ctx)
	}

	updates := map[string]any{
		"last_refreshed_at":        out.RefreshedAt,
		"last_refresh_status":      status,
		"last_refresh_error":       errText,
		"last_refresh_duration_ms": out.DurationMS,
		"last_refresh_trigger":     trigger,
	}
	if dbErr := s.db.WithContext(ctx).Model(&models.SqlaTable{}).
		Where("id = ?", datasetID).Updates(updates).Error; dbErr != nil {
		s.log.Warn("could not record dataset refresh outcome",
			zap.Int("dataset_id", datasetID), zap.Error(dbErr))
	}
	return out, refreshErr
}

// refreshDataset dispatches to the handler for the dataset's kind.
func (s *Service) refreshDataset(ctx context.Context, t *models.SqlaTable) (
	kind string, added, updated int, rows int64, err error,
) {
	if src, ok := SourceOf(t); ok {
		return s.refreshExternal(ctx, t, src)
	}
	if strings.TrimSpace(t.SQL) != "" || t.IsSQLLabView {
		cols, probeErr := s.probeColumns(ctx, t)
		if probeErr != nil {
			return "virtual", 0, 0, 0, probeErr
		}
		added, updated = s.syncProbedColumns(ctx, t, cols)
		return "virtual", added, updated, 0, nil
	}
	cols, probeErr := s.probeColumns(ctx, t)
	if probeErr != nil {
		return "physical", 0, 0, 0, probeErr
	}
	added, updated = s.syncProbedColumns(ctx, t, cols)
	return "physical", added, updated, 0, nil
}

// refreshExternal re-pulls an uploaded file or REST endpoint and rewrites the
// managed table, then re-syncs its columns.
func (s *Service) refreshExternal(ctx context.Context, t *models.SqlaTable, src *Source) (
	kind string, added, updated int, rows int64, err error,
) {
	tbl, err := s.fetchSource(ctx, src)
	if err != nil {
		return "external", 0, 0, 0, err
	}

	var database models.Database
	if err := s.db.WithContext(ctx).First(&database, t.DatabaseID).Error; err != nil {
		return "external", 0, 0, 0, fmt.Errorf("dataset database is missing: %w", err)
	}
	if err := ingest.WriteTable(ctx, database.SQLAlchemyURI, t.TableNameStr, tbl); err != nil {
		return "external", 0, 0, 0, fmt.Errorf("could not rewrite the managed table: %w", err)
	}

	added, updated, err = s.syncIngestedColumns(ctx, t, tbl)
	if err != nil {
		return "external", added, updated, 0, err
	}
	return "external", added, updated, int64(tbl.RowCount()), nil
}

// fetchSource materialises the external payload.
func (s *Service) fetchSource(ctx context.Context, src *Source) (*ingest.Table, error) {
	switch src.Type {
	case "file":
		data, err := os.ReadFile(src.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("the stored source file is missing: %w", err)
		}
		ext := strings.ToLower(filepath.Ext(src.SourcePath))
		switch ext {
		case ".xlsx":
			return ingest.ReadXLSXSheet(data, src.Sheet)
		case ".xls":
			return ingest.ReadXLSSheet(data, src.Sheet)
		case ".csv", ".tsv", ".txt", "":
			return ingest.ReadCSV(strings.NewReader(string(data)))
		default:
			return nil, fmt.Errorf("unsupported file type %q", ext)
		}
	case "api":
		if src.API == nil {
			return nil, errors.New("the REST source configuration is missing")
		}
		return ingest.Fetch(ctx, *src.API)
	default:
		return nil, fmt.Errorf("unknown external source type %q", src.Type)
	}
}

// columnMeta is the schema of one column as read from the source.
type columnMeta struct {
	Name   string
	Type   string
	IsDttm bool
}

// probeColumns reads the current column schema from the source database.
func (s *Service) probeColumns(ctx context.Context, t *models.SqlaTable) ([]columnMeta, error) {
	var database models.Database
	if err := s.db.WithContext(ctx).First(&database, t.DatabaseID).Error; err != nil {
		return nil, fmt.Errorf("dataset database is missing: %w", err)
	}
	handle, err := openConn(database.SQLAlchemyURI)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	probeSQL := ""
	if strings.TrimSpace(t.SQL) != "" {
		probeSQL = "SELECT * FROM (" + t.SQL + ") AS virtual_table " + handle.spec.LimitClause(1)
	} else {
		probeSQL = handle.spec.SelectStar(qualified(t.Schema, t.TableNameStr)) + " " + handle.spec.LimitClause(1)
	}
	rows, err := handle.Query(ctx, probeSQL)
	if err != nil {
		return nil, err
	}

	// Physical tables can carry better type information than the probe query
	// reveals, so prefer the source catalog and fall back to the probe columns.
	if strings.TrimSpace(t.SQL) == "" {
		meta, metaErr := handle.Query(ctx, fmt.Sprintf(handle.spec.ColumnsQuery(),
			escapeSQL(t.Schema), escapeSQL(t.TableNameStr)))
		if metaErr == nil {
			out := make([]columnMeta, 0, len(meta))
			for _, row := range meta {
				name := rowText(row, "column_name", "column", "name")
				if name == "" {
					continue
				}
				dtype := strings.ToLower(rowText(row, "data_type", "type", "column_type"))
				out = append(out, columnMeta{Name: name, Type: dtype, IsDttm: isTemporalType(dtype)})
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("the source returned no columns; check the SQL or table name")
	}
	out := make([]columnMeta, 0, len(rows[0]))
	for name := range rows[0] {
		out = append(out, columnMeta{Name: name, Type: "STRING"})
	}
	return out, nil
}

// syncIngestedColumns upserts the columns of a freshly materialised table.
func (s *Service) syncIngestedColumns(ctx context.Context, t *models.SqlaTable, tbl *ingest.Table) (added, updated int, err error) {
	cols := make([]columnMeta, 0, len(tbl.Columns))
	for _, c := range tbl.Columns {
		cols = append(cols, columnMeta{Name: c.Name, Type: c.Type, IsDttm: c.IsDttm})
	}
	added, updated = s.syncProbedColumns(ctx, t, cols)
	return added, updated, nil
}

// syncProbedColumns inserts new columns, refreshes the type of existing ones and
// keeps main_dttm_col pointing at a real temporal column.
func (s *Service) syncProbedColumns(ctx context.Context, t *models.SqlaTable, cols []columnMeta) (added, updated int) {
	db := s.db.WithContext(ctx)
	for _, c := range cols {
		var existing models.TableColumn
		err := db.Where("table_id = ? AND column_name = ?", t.ID, c.Name).First(&existing).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			col := models.TableColumn{
				UUID: newUUID(), ColumnName: c.Name, Type: c.Type, IsDttm: c.IsDttm,
				Groupby: true, Filterable: true, IsActive: true, TableID: t.ID,
			}
			if createErr := db.Create(&col).Error; createErr != nil {
				s.log.Warn("could not add dataset column",
					zap.Int("dataset_id", t.ID), zap.String("column", c.Name), zap.Error(createErr))
				continue
			}
			added++
		case err == nil:
			changes := map[string]any{}
			if c.Type != "" && !strings.EqualFold(existing.Type, c.Type) {
				changes["type"] = c.Type
			}
			if existing.IsDttm != c.IsDttm {
				changes["is_dttm"] = c.IsDttm
			}
			if len(changes) == 0 {
				continue
			}
			if updErr := db.Model(&existing).Updates(changes).Error; updErr != nil {
				s.log.Warn("could not update dataset column",
					zap.Int("dataset_id", t.ID), zap.String("column", c.Name), zap.Error(updErr))
				continue
			}
			updated++
		}
	}

	// A stale main_dttm_col silently disables every time-series chart built on
	// the dataset, so re-validate it whenever the schema moves.
	if t.MainDttmCol != "" {
		var count int64
		db.Model(&models.TableColumn{}).
			Where("table_id = ? AND column_name = ?", t.ID, t.MainDttmCol).Count(&count)
		if count == 0 {
			db.Model(&models.SqlaTable{}).Where("id = ?", t.ID).Update("main_dttm_col", "")
			t.MainDttmCol = ""
		}
	}
	if t.MainDttmCol == "" {
		for _, c := range cols {
			if !c.IsDttm {
				continue
			}
			if updErr := db.Model(&models.SqlaTable{}).Where("id = ?", t.ID).
				Update("main_dttm_col", c.Name).Error; updErr == nil {
				t.MainDttmCol = c.Name
			}
			break
		}
	}
	return added, updated
}

// invalidateChartCache drops cached chart results after a successful refresh.
// Keys are derived from form_data + SQL and cannot be mapped back to a dataset,
// so the whole namespace is invalidated.
func (s *Service) invalidateChartCache(ctx context.Context) {
	if s.cache == nil {
		return
	}
	n, err := s.cache.DeletePrefix(ctx, queries.QueryCachePrefix)
	if err != nil {
		s.log.Warn("could not invalidate query cache", zap.Error(err))
		return
	}
	if n > 0 {
		s.log.Info("query cache invalidated after dataset refresh", zap.Int("keys", n))
	}
}

func truncateError(text string) string {
	if len(text) > 2000 {
		return text[:2000] + "…"
	}
	return text
}
