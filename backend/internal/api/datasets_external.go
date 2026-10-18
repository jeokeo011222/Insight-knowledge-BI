package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"superset/internal/datasets"
	"superset/internal/ingest"
	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/security"
	"superset/internal/server"
)

// datasetSource is the JSON payload stored in SqlaTable.extra["source"]. It
// records how an external dataset was produced so it can be re-pulled later.
// The type lives in internal/datasets because the background refresher needs the
// same view of it as the HTTP handlers.
type datasetSource = datasets.Source

// extraSource reads the external source description from a dataset, if any.
func (d *datasetAPI) extraSource(t *models.SqlaTable) (*datasetSource, bool) {
	return datasets.SourceOf(t)
}

// encodeExtra renders a source description as the dataset's extra JSON.
func encodeExtra(src *datasetSource) string {
	return datasets.EncodeExtra("", src)
}

// maxUploadBytes returns the configured upload ceiling with a sane default.
func (d *datasetAPI) maxUploadBytes() int64 {
	if size := d.deps.Config.UploadMaxFileSizeBytes; size > 0 {
		return size
	}
	return 100 << 20
}

// uploadDataset ingests a multipart CSV/Excel upload, materialises it into the
// managed SQLite store and creates a dataset pointing at it. An Excel workbook
// with several worksheets yields one dataset per selected worksheet.
func (d *datasetAPI) uploadDataset(w http.ResponseWriter, r *http.Request) {
	maxBytes := d.maxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.upload_unreadable", "Could not read the uploaded file")))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.upload_required", "A file is required")))
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.upload_unreadable", "Could not read the uploaded file")))
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	sheetNames, excel, err := uploadedSheets(ext, data)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	if excel && len(sheetNames) == 0 {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.upload_empty_workbook", "The workbook has no worksheets")))
		return
	}

	// CSV and other flat files have no worksheets to pick.
	selected := selectSheets(sheetNames, r.MultipartForm.Value["sheets"])
	if !excel {
		selected = []string{""}
	} else if len(selected) == 0 {
		selected = sheetNames[:1]
	}

	baseName := strings.TrimSpace(r.FormValue("table_name"))
	if baseName == "" {
		baseName = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
	}
	baseName = ingest.SanitizeIdentifier(baseName)
	if baseName == "" {
		baseName = "imported_data"
	}

	// Keep a copy of the source file so the dataset can be re-parsed on refresh.
	sourceDir, err := ingest.SourceDir(d.deps.Config)
	if err != nil {
		server.WriteError(w, apperrors.Internal("%s", err.Error()))
		return
	}
	sourcePath := filepath.Join(sourceDir, uuid.NewString()+ext)
	if err := os.WriteFile(sourcePath, data, 0o644); err != nil {
		server.WriteError(w, apperrors.Internal("%s", err.Error()))
		return
	}

	// Parse everything before writing, so a bad worksheet cannot leave a
	// half-imported upload behind.
	tables := make([]*ingest.Table, len(selected))
	for i, sheet := range selected {
		tbl, err := parseUploaded(ext, data, sheet)
		if err != nil {
			server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
			return
		}
		tables[i] = tbl
	}

	uploadedOn := time.Now().UTC().Format(time.RFC3339)
	description := strings.TrimSpace(r.FormValue("description"))
	created := make([]any, 0, len(selected))
	for i, sheet := range selected {
		tableName := baseName
		// Several worksheets from one upload become one dataset each.
		if len(selected) > 1 && sheet != "" {
			tableName = ingest.SanitizeIdentifier(baseName + "_" + sheet)
		}
		src := &datasetSource{
			Type:       "file",
			Filename:   header.Filename,
			SourcePath: sourcePath,
			Sheet:      sheet,
			UploadedOn: uploadedOn,
		}
		item, err := d.materializeDataset(r, tableName, description, src, tables[i])
		if err != nil {
			server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
			return
		}
		created = append(created, d.datasetJSON(r, item))
	}

	server.WriteJSON(w, http.StatusCreated, map[string]any{
		"result":  created[0],
		"results": created,
		"sheets":  selected,
	})
}

// listUploadSheets reports the worksheets of an uploaded workbook so the UI can
// ask the user which ones to import. Non-Excel payloads report none.
func (d *datasetAPI) listUploadSheets(w http.ResponseWriter, r *http.Request) {
	maxBytes := d.maxUploadBytes()
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseMultipartForm(maxBytes); err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.upload_unreadable", "Could not read the uploaded file")))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.upload_required", "A file is required")))
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.upload_unreadable", "Could not read the uploaded file")))
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	names, excel, err := uploadedSheets(ext, data)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	if !excel || names == nil {
		names = []string{}
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"filename": header.Filename,
		"excel":    excel,
		"sheets":   names,
	}})
}

// uploadedSheets lists the worksheets of an Excel payload. ok is false for
// formats that have no worksheets at all (CSV and friends).
func uploadedSheets(ext string, data []byte) ([]string, bool, error) {
	switch ext {
	case ".xlsx", ".xls":
		names, err := ingest.SheetNames(data)
		return names, true, err
	default:
		return nil, false, nil
	}
}

// selectSheets keeps the requested worksheet names that really exist, in the
// order the workbook declares them, so the import is deterministic.
func selectSheets(available, requested []string) []string {
	if len(requested) == 0 {
		return nil
	}
	wanted := make(map[string]bool, len(requested))
	for _, name := range requested {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			wanted[trimmed] = true
		}
	}
	out := make([]string, 0, len(wanted))
	for _, name := range available {
		if wanted[name] {
			out = append(out, name)
		}
	}
	return out
}

// apiSourcePayload is the body of the two REST-source endpoints.
type apiSourcePayload struct {
	TableName   string            `json:"table_name"`
	Description string            `json:"description"`
	Source      ingest.RESTSource `json:"source"`
}

// testAPISource fetches the configured REST endpoint and returns a preview,
// letting the UI validate a source before the dataset is created.
func (d *datasetAPI) testAPISource(w http.ResponseWriter, r *http.Request) {
	var p apiSourcePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	start := time.Now()
	tbl, err := ingest.Fetch(r.Context(), p.Source)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	preview := tbl.Rows
	if len(preview) > 20 {
		preview = preview[:20]
	}
	columns := make([]map[string]any, 0, len(tbl.Columns))
	for _, c := range tbl.Columns {
		columns = append(columns, map[string]any{
			"column_name": c.Name, "type": c.Type, "is_dttm": c.IsDttm,
		})
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": map[string]any{
		"columns":     columns,
		"data":        preview,
		"rowcount":    tbl.RowCount(),
		"duration_ms": time.Since(start).Milliseconds(),
	}})
}

// createAPISourceDataset fetches a REST endpoint and materialises the records
// into the managed store, creating a dataset bound to the source.
func (d *datasetAPI) createAPISourceDataset(w http.ResponseWriter, r *http.Request) {
	var p apiSourcePayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if strings.TrimSpace(p.Source.URL) == "" {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.api_url_required", "An API URL is required")))
		return
	}

	tableName := ingest.SanitizeIdentifier(p.TableName)
	if tableName == "" {
		tableName = ingest.SanitizeIdentifier(nameFromURL(p.Source.URL))
	}
	if tableName == "" {
		tableName = "api_data"
	}

	tbl, err := ingest.Fetch(r.Context(), p.Source)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}

	src := &datasetSource{
		Type:       "api",
		API:        &p.Source,
		UploadedOn: time.Now().UTC().Format(time.RFC3339),
	}
	created, err := d.materializeDataset(r, tableName, p.Description, src, tbl)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": d.datasetJSON(r, created)})
}

// refreshSource re-pulls an external dataset's data (re-parsing a stored file or
// re-calling the API) and rebuilds its table. It is a thin alias of the shared
// refresher so the explicit "re-pull" entry point and the generic refresh can
// never drift apart.
func (d *datasetAPI) refreshSource(w http.ResponseWriter, r *http.Request) {
	t, err := d.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if _, ok := d.extraSource(t); !ok {
		server.WriteError(w, apperrors.BadRequest("%s", localize(r, "error.dataset.no_external_source", "This dataset is not backed by an external source")))
		return
	}

	outcome, err := d.refresherService().Refresh(r.Context(), t.ID, datasets.TriggerManual)
	if err != nil {
		server.WriteError(w, apperrors.BadRequest("%s", err.Error()))
		return
	}
	_ = d.deps.DB.WithContext(r.Context()).First(t, t.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{
		"result":  d.datasetJSON(r, t),
		"refresh": outcome,
	})
}

// materializeDataset writes the parsed table into the managed SQLite store and
// creates the dataset plus its columns and owner.
func (d *datasetAPI) materializeDataset(r *http.Request, tableName, description string, src *datasetSource, tbl *ingest.Table) (*models.SqlaTable, error) {
	ctx := r.Context()
	extDB, err := ingest.EnsureExternalDatabase(ctx, d.deps.DB, d.deps.Config)
	if err != nil {
		return nil, err
	}
	tableName = d.uniqueTableName(ctx, extDB.ID, tableName)

	if err := ingest.WriteTable(ctx, extDB.SQLAlchemyURI, tableName, tbl); err != nil {
		return nil, fmt.Errorf("could not materialise the data: %w", err)
	}

	t := models.SqlaTable{
		UUID:                uuid.NewString(),
		TableNameStr:        tableName,
		DatabaseID:          extDB.ID,
		Description:         description,
		Extra:               encodeExtra(src),
		FilterSelectEnabled: true,
	}
	t.Perm = fmt.Sprintf("[%s].[%s](id:%d)", extDB.DatabaseName, tableName, 0)
	for _, c := range tbl.Columns {
		if c.IsDttm {
			t.MainDttmCol = c.Name
			break
		}
	}

	err = d.deps.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
		for _, c := range tbl.Columns {
			col := models.TableColumn{
				UUID: uuid.NewString(), ColumnName: c.Name, Type: c.Type, IsDttm: c.IsDttm,
				Groupby: true, Filterable: true, IsActive: true, TableID: t.ID,
			}
			if err := tx.Create(&col).Error; err != nil {
				return err
			}
		}
		if uid := userIDOf(r); uid > 0 {
			if err := tx.Create(&models.SqlatableUser{UserID: uid, TableID: t.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = d.deps.DB.WithContext(ctx).First(&t, t.ID).Error
	return &t, nil
}

// uniqueTableName appends a numeric suffix until the physical table name is free
// within the managed database, so two uploads never clash.
func (d *datasetAPI) uniqueTableName(ctx context.Context, databaseID int, name string) string {
	base := name
	for attempt := 1; ; attempt++ {
		var count int64
		d.deps.DB.WithContext(ctx).Model(&models.SqlaTable{}).
			Where("database_id = ? AND table_name = ? AND deleted_at IS NULL", databaseID, name).
			Count(&count)
		if count == 0 {
			return name
		}
		name = fmt.Sprintf("%s_%d", base, attempt+1)
	}
}

// parseUploaded dispatches an uploaded payload to the matching reader. Excel
// workbooks may hold several worksheets, so the caller names the one to parse
// ("" selects the first).
func parseUploaded(ext string, data []byte, sheet string) (*ingest.Table, error) {
	switch ext {
	case ".xlsx":
		return ingest.ReadXLSXSheet(data, sheet)
	case ".xls":
		return ingest.ReadXLSSheet(data, sheet)
	case ".csv", ".tsv", ".txt", "":
		return ingest.ReadCSV(bytes.NewReader(data))
	default:
		return nil, fmt.Errorf("unsupported file type %q", ext)
	}
}

// sourceJSON renders a compact, secret-free summary of an external source for
// API responses. Credentials are deliberately omitted.
func sourceJSON(src *datasetSource) map[string]any {
	if src == nil {
		return nil
	}
	out := map[string]any{"type": src.Type}
	if src.Filename != "" {
		out["filename"] = src.Filename
	}
	if src.Sheet != "" {
		out["sheet"] = src.Sheet
	}
	if src.API != nil {
		method := strings.ToUpper(strings.TrimSpace(src.API.Method))
		if method == "" {
			method = "GET"
		}
		out["url"] = src.API.URL
		out["method"] = method
		if src.API.RecordPath != "" {
			out["record_path"] = src.API.RecordPath
		}
	}
	return out
}

// nameFromURL derives a table name candidate from the last URL path segment.
func nameFromURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	if idx := strings.IndexAny(trimmed, "?#"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	if idx := strings.Index(trimmed, "."); idx > 0 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}
