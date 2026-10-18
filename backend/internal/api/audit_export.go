package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"superset/internal/models"
	"superset/internal/server"
)

// auditExportDefaultLimit / auditExportMaxLimit bound one export request. The
// audit trail grows without bound, so a compliance export is capped instead of
// paging the whole table into memory; the caller narrows the window with the
// same filters the list endpoint accepts.
const (
	auditExportDefaultLimit = 50000
	auditExportMaxLimit     = 200000
)

// auditCSVHeader is the column order of the exported file. It mirrors the list
// table plus the raw JSON payload, which is what an auditor needs to reproduce
// a request.
var auditCSVHeader = []string{
	"id", "dttm", "action", "user_id", "dashboard_id", "slice_id",
	"duration_ms", "referrer", "json",
}

// exportLogs serves GET /log/export/.
//
// The list endpoint is paginated, which is useless for an audit: the reviewer
// wants one file covering the whole window. Filters (q, user_id, filters=) are
// identical to the list so what is exported always matches what is on screen,
// and the payload starts with a UTF-8 BOM so Excel renders the Chinese action
// labels instead of mojibake.
func (a *miscAPI) exportLogs(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.Log{})
	if p.Search != "" {
		q = q.Where("action ILIKE ?", "%"+p.Search+"%")
	}
	if uid := server.QueryInt(r, "user_id", 0); uid > 0 {
		q = q.Where("user_id = ?", uid)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}

	limit := clampInt(intQueryParam(r, "limit", auditExportDefaultLimit), 1, auditExportMaxLimit)
	var rows []models.Log
	if err := q.Order("id desc").Limit(limit).Find(&rows).Error; err != nil {
		server.WriteError(w, err)
		return
	}

	filename := fmt.Sprintf("audit_logs_%s.csv", time.Now().UTC().Format("20060102T150405"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	// BOM: Excel guesses the encoding from the first bytes.
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})

	cw := csv.NewWriter(w)
	_ = cw.Write(auditCSVHeader)
	for i := range rows {
		l := &rows[i]
		record := []string{
			strconv.Itoa(l.ID),
			auditCSVTime(l.Dttm),
			l.Action,
			auditCSVInt(l.UserID),
			auditCSVInt(l.DashboardID),
			auditCSVInt(l.SliceID),
			auditCSVInt(l.DurationMs),
			l.Referrer,
			l.JSON,
		}
		if err := cw.Write(record); err != nil {
			// The client went away mid-download; nothing left to report to.
			return
		}
	}
	cw.Flush()
}

// auditCSVTime renders a timestamp in RFC3339 (UTC) so the file sorts and
// parses without depending on the reader's locale.
func auditCSVTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// auditCSVInt renders an optional foreign key / duration.
func auditCSVInt(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}
