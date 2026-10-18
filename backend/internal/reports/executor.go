package reports

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/config"
	"superset/internal/models"
	"superset/internal/notifications"
	"superset/internal/queries"
	"superset/internal/thumbnails"
)

// Execution log states (superset.reports.models.ReportState).
const (
	StateWorking      = "Working"
	StateSuccess      = "Success"
	StateError        = "Error"
	StateFailed       = "Failed"
	StateTriggered    = "Triggered"
	StateNotTriggered = "Not triggered"
)

// Trigger values recorded in logs.
const (
	TriggerScheduler = "scheduler"
	TriggerManual    = "manual"
)

// Executor runs one alert or report schedule end to end.
type Executor struct {
	cfg      *config.Config
	log      *zap.Logger
	db       *gorm.DB
	queries  *queries.Executor
	notifier *notifications.Dispatcher
	thumbs   *thumbnails.Service
}

// NewExecutor builds the executor with its query engine, notifier and
// thumbnail service.
func NewExecutor(cfg *config.Config, log *zap.Logger, db *gorm.DB) *Executor {
	if log == nil {
		log = zap.NewNop()
	}
	e := &Executor{cfg: cfg, log: log, db: db}
	e.queries = queries.NewFromConfig(db, log, nil, nil, cfg)
	e.notifier = notifications.NewDispatcher(cfg, log)
	e.thumbs = thumbnails.NewService(cfg, log)
	return e
}

// SetThumbnails replaces the thumbnail service (the API shares its instance).
func (e *Executor) SetThumbnails(s *thumbnails.Service) *Executor {
	if s != nil {
		e.thumbs = s
	}
	return e
}

// Thumbnails returns the thumbnail service in use.
func (e *Executor) Thumbnails() *thumbnails.Service { return e.thumbs }

// Notifier returns the notification dispatcher.
func (e *Executor) Notifier() *notifications.Dispatcher { return e.notifier }

// result is the outcome of one execution.
type result struct {
	lastState string // written to report_schedule.last_state
	status    string // report_execution_log.state
	value     *float64
	rowJSON   []byte
	err       error
}

// Execute runs a schedule. A report_execution_log row is always written, even
// when the run fails; the returned log reflects the persisted row.
func (e *Executor) Execute(ctx context.Context, scheduleID int, trigger string) (*models.ReportExecutionLog, error) {
	if e.db == nil {
		return nil, errors.New("reports: executor has no database")
	}
	started := time.Now().UTC()

	var s models.ReportSchedule
	if err := e.db.WithContext(ctx).First(&s, scheduleID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("reports: schedule %d not found", scheduleID)
		}
		return nil, fmt.Errorf("reports: load schedule %d: %w", scheduleID, err)
	}

	lg := e.log.With(
		zap.Int("schedule_id", s.ID),
		zap.String("schedule", s.Name),
		zap.String("type", s.Type),
		zap.String("trigger", trigger))

	entry := &models.ReportExecutionLog{
		UUID:             uuid.NewString(),
		ScheduledDttm:    started,
		StartDttm:        &started,
		State:            StateWorking,
		ReportScheduleID: s.ID,
	}
	if err := e.db.WithContext(ctx).Create(entry).Error; err != nil {
		return nil, fmt.Errorf("reports: create execution log: %w", err)
	}

	timeout := e.workingTimeout(&s)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	res := e.run(runCtx, &s, lg)
	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.status = StateError
		res.lastState = StateError
		res.err = fmt.Errorf("timeout after %s: %w", timeout, context.DeadlineExceeded)
	}

	ended := time.Now().UTC()
	entry.EndDttm = &ended
	entry.State = res.status
	entry.Value = res.value
	if len(res.rowJSON) > 0 {
		entry.ValueRowJSON = datatypes.JSON(res.rowJSON)
	}
	if res.err != nil {
		entry.ErrorMessage = res.err.Error()
	}
	if err := e.db.WithContext(ctx).Save(entry).Error; err != nil {
		lg.Error("persist execution log", zap.Error(err))
	}

	e.recordScheduleState(&s, res, ended, lg)
	lg.Info("schedule executed",
		zap.String("last_state", res.lastState),
		zap.String("status", res.status),
		zap.Duration("elapsed", ended.Sub(started)))
	return entry, res.err
}

// RecordSkipped writes an execution log for a run that never started, used when
// a schedule is still running from a previous tick.
func (e *Executor) RecordSkipped(ctx context.Context, scheduleID int, reason string) (*models.ReportExecutionLog, error) {
	now := time.Now().UTC()
	entry := &models.ReportExecutionLog{
		UUID:             uuid.NewString(),
		ScheduledDttm:    now,
		StartDttm:        &now,
		EndDttm:          &now,
		State:            StateFailed,
		ErrorMessage:     reason,
		ReportScheduleID: scheduleID,
	}
	if err := e.db.WithContext(ctx).Create(entry).Error; err != nil {
		return nil, fmt.Errorf("reports: create skipped execution log: %w", err)
	}
	return entry, nil
}

// recordScheduleState persists last_eval_dttm/last_state/last_value.
func (e *Executor) recordScheduleState(s *models.ReportSchedule, res result, ended time.Time, lg *zap.Logger) {
	updates := map[string]any{
		"last_eval_dttm": ended,
		"last_state":     res.lastState,
	}
	if res.value != nil {
		updates["last_value"] = *res.value
	}
	if len(res.rowJSON) > 0 {
		updates["last_value_row_json"] = string(res.rowJSON)
	}
	if err := e.db.Model(&models.ReportSchedule{}).Where("id = ?", s.ID).Updates(updates).Error; err != nil {
		lg.Error("persist schedule state", zap.Error(err))
	}
}

// run dispatches to the alert or report pipeline.
func (e *Executor) run(ctx context.Context, s *models.ReportSchedule, lg *zap.Logger) result {
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "alert":
		return e.runAlert(ctx, s, lg)
	case "report":
		return e.runReport(ctx, s, lg)
	default:
		return result{lastState: StateError, status: StateError,
			err: fmt.Errorf("unsupported schedule type %q", s.Type)}
	}
}

// runAlert observes the SQL value, validates it and notifies when triggered.
func (e *Executor) runAlert(ctx context.Context, s *models.ReportSchedule, lg *zap.Logger) result {
	obs, err := Observe(ctx, e.db, s)
	if err != nil {
		return result{lastState: StateError, status: StateError, err: err}
	}

	validatorCfg := ParseValidatorConfig(s.ValidatorConfigJSON)
	triggered, err := Validate(s.ValidatorType, validatorCfg, obs.Value)
	if err != nil {
		return result{lastState: StateError, status: StateError, value: obs.FloatValue,
			rowJSON: obs.RowJSON, err: err}
	}

	lastState := StateNotTriggered
	if triggered {
		lastState = StateTriggered
	}
	res := result{lastState: lastState, status: StateSuccess, value: obs.FloatValue, rowJSON: obs.RowJSON}
	if !triggered {
		return res
	}

	valueText := formatValue(obs.Value)
	notif := notifications.Notification{
		Subject: alertSubject(s, valueText),
		Text:    alertText(s, valueText),
		HTML:    alertHTML(s, valueText, obs),
	}
	status, warn := e.dispatch(ctx, s, notif)
	res.status = status
	res.err = warn
	if status == StateFailed {
		lg.Warn("alert notification failed", zap.Error(warn))
	}
	return res
}

// alertSubject builds a subject carrying the alert name and triggering value.
func alertSubject(s *models.ReportSchedule, value string) string {
	subject := strings.TrimSpace(s.EmailSubject)
	if subject == "" {
		subject = s.Name
	}
	if value == "" {
		return fmt.Sprintf("[Alert] %s", subject)
	}
	return fmt.Sprintf("[Alert] %s = %s", subject, value)
}

// alertText is the plain text body.
func alertText(s *models.ReportSchedule, value string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Alert: %s\n", s.Name)
	fmt.Fprintf(&b, "Value: %s\n", value)
	if s.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", s.Description)
	}
	fmt.Fprintf(&b, "Evaluated: %s\n", time.Now().UTC().Format(time.RFC3339))
	return b.String()
}

// alertHTML is the HTML body.
func alertHTML(s *models.ReportSchedule, value string, obs *Observation) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:Arial,Helvetica,sans-serif">`)
	fmt.Fprintf(&b, "<h2>%s</h2>", htmlEscape(s.Name))
	if s.Description != "" {
		fmt.Fprintf(&b, "<p>%s</p>", htmlEscape(s.Description))
	}
	b.WriteString(`<table cellspacing="0" cellpadding="6" border="1" style="border-collapse:collapse">`)
	fmt.Fprintf(&b, `<tr><th align="left">Value</th><td>%s</td></tr>`, htmlEscape(value))
	fmt.Fprintf(&b, `<tr><th align="left">Validator</th><td>%s</td></tr>`, htmlEscape(s.ValidatorType))
	if len(obs.Columns) > 0 {
		fmt.Fprintf(&b, `<tr><th align="left">Column</th><td>%s</td></tr>`, htmlEscape(obs.Columns[0]))
	}
	b.WriteString("</table>")
	if s.ContextMarkdown != "" {
		fmt.Fprintf(&b, `<pre style="white-space:pre-wrap">%s</pre>`, htmlEscape(s.ContextMarkdown))
	}
	fmt.Fprintf(&b, `<p style="color:#888;font-size:12px">Evaluated %s</p>`, time.Now().UTC().Format(time.RFC3339))
	b.WriteString("</div>")
	return b.String()
}

// runReport gathers chart data, renders CSV + screenshot and notifies.
func (e *Executor) runReport(ctx context.Context, s *models.ReportSchedule, lg *zap.Logger) result {
	charts, err := e.reportCharts(ctx, s)
	if err != nil {
		return result{lastState: StateError, status: StateError, err: err}
	}

	userID := e.scheduleUser(ctx, s, false)
	var (
		csvData []byte
		qr      *queries.QueryResult
		csvName = safeFilename(s.Name) + ".csv"
	)
	for i := range charts {
		chart := &charts[i]
		qc, err := chartQueryContext(chart)
		if err != nil {
			return result{lastState: StateError, status: StateError, err: err}
		}
		out, err := e.queries.ExecuteQueryContext(ctx, userID, qc)
		if err != nil {
			return result{lastState: StateError, status: StateError,
				err: fmt.Errorf("chart %d %q: %w", chart.ID, chart.SliceName, err)}
		}
		qr = out
		csvData = renderCSV(out)
		break
	}

	var shot []byte
	warning := ""
	key, target, err := e.reportTarget(ctx, s, charts)
	if err != nil {
		warning = err.Error()
		lg.Warn("thumbnail target unavailable", zap.Error(err))
	} else if e.thumbs != nil {
		selector := func() thumbnails.Target { return target }
		if s.ForceScreenshot {
			_, shot, err = e.thumbs.Force(ctx, key, selector)
		} else {
			_, shot, err = e.thumbs.Ensure(ctx, key, selector)
		}
		if err != nil {
			warning = fmt.Sprintf("screenshot failed: %s", err)
			lg.Warn("screenshot failed", zap.Error(err))
		}
	}

	if len(csvData) == 0 && len(shot) == 0 {
		return result{lastState: StateError, status: StateError,
			err: errors.New("report produced neither data nor a screenshot")}
	}

	notif := notifications.Notification{
		Subject:    reportSubject(s),
		Text:       reportText(s, qr),
		HTML:       reportHTML(s, qr),
		Screenshot: shot,
		CSV:        csvData,
		CSVName:    csvName,
	}
	status, warn := e.dispatch(ctx, s, notif)
	res := result{lastState: StateSuccess, status: status}
	if status == StateError {
		res.lastState = StateError
	}
	switch {
	case warn != nil && status == StateFailed:
		res.err = warn
	case warning != "" && warn != nil:
		res.err = fmt.Errorf("%s; %w", warning, warn)
	case warning != "":
		res.err = errors.New(warning)
	case warn != nil:
		res.err = warn
	}
	return res
}

// reportTarget builds the screenshot request, preferring dashboards.
func (e *Executor) reportTarget(ctx context.Context, s *models.ReportSchedule, charts []models.Slice) (string, thumbnails.Target, error) {
	if e.thumbs == nil {
		return "", thumbnails.Target{}, errors.New("thumbnail service is not available")
	}
	width, height := 1440, 900
	if s.CustomWidth != nil && *s.CustomWidth > 0 {
		width = *s.CustomWidth
	}
	if s.CustomHeight != nil && *s.CustomHeight > 0 {
		height = *s.CustomHeight
	}
	userID := e.scheduleUser(ctx, s, false)
	token, err := e.thumbs.Token(userID, 30*time.Minute)
	if err != nil {
		return "", thumbnails.Target{}, err
	}
	base := e.thumbs.BaseURL()

	if s.DashboardID != nil && *s.DashboardID > 0 {
		id := *s.DashboardID
		return fmt.Sprintf("dashboard:%d", id), thumbnails.Target{
			URL:           fmt.Sprintf("%s/dashboard/%d/", base, id),
			Selector:      ".dashboard-page, .chart-card, main",
			Width:         width,
			Height:        height,
			Token:         token,
			WaitSelectors: []string{".dashboard-page"},
		}, nil
	}
	if s.ChartID != nil && *s.ChartID > 0 {
		id := *s.ChartID
		return fmt.Sprintf("chart:%d", id), thumbnails.Target{
			URL:      fmt.Sprintf("%s/explore/?slice_id=%d", base, id),
			Selector: ".chart-card, main",
			Width:    width,
			Height:   height,
			Token:    token,
		}, nil
	}
	if len(charts) > 0 {
		id := charts[0].ID
		return fmt.Sprintf("chart:%d", id), thumbnails.Target{
			URL:      fmt.Sprintf("%s/explore/?slice_id=%d", base, id),
			Selector: ".chart-card, main",
			Width:    width,
			Height:   height,
			Token:    token,
		}, nil
	}
	return "", thumbnails.Target{}, errors.New("report has neither a chart nor a dashboard")
}

// reportCharts resolves the charts backing a report.
func (e *Executor) reportCharts(ctx context.Context, s *models.ReportSchedule) ([]models.Slice, error) {
	db := e.db.WithContext(ctx)
	if s.ChartID != nil && *s.ChartID > 0 {
		var chart models.Slice
		if err := db.First(&chart, *s.ChartID).Error; err != nil {
			return nil, fmt.Errorf("reports: load chart %d: %w", *s.ChartID, err)
		}
		return []models.Slice{chart}, nil
	}
	if s.DashboardID != nil && *s.DashboardID > 0 {
		var charts []models.Slice
		err := db.Raw(`
			SELECT s.* FROM slices s
			JOIN dashboard_slices ds ON ds.slice_id = s.id
			WHERE ds.dashboard_id = ? AND s.deleted_at IS NULL
			ORDER BY s.id`, *s.DashboardID).Scan(&charts).Error
		if err != nil {
			return nil, fmt.Errorf("reports: load dashboard %d charts: %w", *s.DashboardID, err)
		}
		if len(charts) == 0 {
			return nil, fmt.Errorf("reports: dashboard %d has no charts", *s.DashboardID)
		}
		return charts, nil
	}
	return nil, errors.New("reports: report requires a chart_id or dashboard_id")
}

// chartQueryContext prefers the stored query context and falls back to params.
func chartQueryContext(chart *models.Slice) (*queries.QueryContext, error) {
	if len(chart.QueryContext) > 0 {
		var probe map[string]any
		if err := json.Unmarshal(chart.QueryContext, &probe); err == nil {
			if _, ok := probe["queries"]; ok {
				qc, err := queries.ParseQueryContext(json.RawMessage(chart.QueryContext))
				if err == nil {
					ensureDatasource(qc, chart)
					return qc, nil
				}
			}
		}
	}
	formData := map[string]any{}
	if len(chart.Params) > 0 {
		_ = json.Unmarshal(chart.Params, &formData)
	}
	body := map[string]any{
		"datasource": map[string]any{
			"id":   chart.DatasourceID,
			"type": orDefault(chart.DatasourceType, "table"),
		},
		"form_data": formData,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("reports: encode chart %d query context: %w", chart.ID, err)
	}
	qc, err := queries.ParseQueryContext(raw)
	if err != nil {
		return nil, err
	}
	ensureDatasource(qc, chart)
	return qc, nil
}

// ensureDatasource points the context at the chart's dataset when missing.
func ensureDatasource(qc *queries.QueryContext, chart *models.Slice) {
	if qc == nil {
		return
	}
	if id, _ := qc.DatasourceID(); id == 0 {
		qc.Datasource = map[string]any{
			"id":   chart.DatasourceID,
			"type": orDefault(chart.DatasourceType, "table"),
		}
	}
}

// renderCSV renders query rows as RFC 4180 CSV.
func renderCSV(qr *queries.QueryResult) []byte {
	if qr == nil {
		return nil
	}
	cols := make([]string, 0, len(qr.Columns))
	for _, c := range qr.Columns {
		cols = append(cols, c.Name)
	}
	if len(cols) == 0 {
		cols = columnsFromRows(qr.Data)
	}
	if len(cols) == 0 {
		return nil
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	if err := w.Write(cols); err != nil {
		return nil
	}
	for _, row := range qr.Data {
		record := make([]string, len(cols))
		for i, col := range cols {
			record[i] = csvValue(row[col])
		}
		if err := w.Write(record); err != nil {
			return nil
		}
	}
	w.Flush()
	if w.Error() != nil {
		return nil
	}
	return buf.Bytes()
}

// columnsFromRows derives a stable column order from the first row.
func columnsFromRows(rows []map[string]any) []string {
	if len(rows) == 0 {
		return nil
	}
	cols := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		cols = append(cols, k)
	}
	sortStrings(cols)
	return cols
}

// csvValue renders a cell value.
func csvValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.6f", t), "0"), ".")
	default:
		return fmt.Sprint(t)
	}
}

// dispatch sends the notification and classifies the outcome.
func (e *Executor) dispatch(ctx context.Context, s *models.ReportSchedule, n notifications.Notification) (string, error) {
	var recipients []models.ReportRecipient
	if err := e.db.WithContext(ctx).Where("report_schedule_id = ?", s.ID).
		Order("id asc").Find(&recipients).Error; err != nil {
		return StateError, fmt.Errorf("reports: load recipients: %w", err)
	}
	if len(recipients) == 0 {
		return StateSuccess, nil
	}
	if e.notifier == nil {
		return StateError, errors.New("reports: notification dispatcher is not configured")
	}
	results := e.notifier.Send(ctx, n, recipients)
	delivered := 0
	for _, r := range results {
		if r.Success || r.DryRun {
			delivered++
		}
	}
	msg := notifications.Failures(results)
	if delivered == 0 && msg != "" {
		return StateFailed, errors.New(msg)
	}
	if msg != "" {
		return StateSuccess, errors.New(msg)
	}
	return StateSuccess, nil
}

// workingTimeout resolves the per schedule timeout.
func (e *Executor) workingTimeout(s *models.ReportSchedule) time.Duration {
	seconds := s.WorkingTimeout
	if seconds <= 0 && e.cfg != nil {
		seconds = e.cfg.AlertReportsDefaultWorkingTimeout
	}
	if seconds <= 0 {
		seconds = 3600
	}
	return time.Duration(seconds) * time.Second
}

// scheduleUser picks the acting user for query authorization and screenshots.
func (e *Executor) scheduleUser(ctx context.Context, s *models.ReportSchedule, alert bool) int {
	if alert && s.RunAlertQueryAsFk != nil && *s.RunAlertQueryAsFk > 0 {
		return *s.RunAlertQueryAsFk
	}
	if s.RunAsFk != nil && *s.RunAsFk > 0 {
		return *s.RunAsFk
	}
	var owner int
	_ = e.db.WithContext(ctx).Raw(
		`SELECT user_id FROM report_schedule_user WHERE report_schedule_id = ? ORDER BY user_id LIMIT 1`,
		s.ID).Scan(&owner).Error
	if owner > 0 {
		return owner
	}
	if s.CreatedByFk != nil && *s.CreatedByFk > 0 {
		return *s.CreatedByFk
	}
	return 1
}

// reportSubject builds the report email subject.
func reportSubject(s *models.ReportSchedule) string {
	if strings.TrimSpace(s.EmailSubject) != "" {
		return s.EmailSubject
	}
	return fmt.Sprintf("[Report] %s", s.Name)
}

// reportText renders a plain text summary.
func reportText(s *models.ReportSchedule, qr *queries.QueryResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Report: %s\n", s.Name)
	if s.Description != "" {
		fmt.Fprintf(&b, "%s\n", s.Description)
	}
	if qr != nil {
		fmt.Fprintf(&b, "Rows: %d, Columns: %d\n", qr.RowCount, len(qr.Columns))
	}
	fmt.Fprintf(&b, "Generated: %s\n", time.Now().UTC().Format(time.RFC3339))
	return b.String()
}

// reportHTML renders the first 10 rows as an HTML table.
func reportHTML(s *models.ReportSchedule, qr *queries.QueryResult) string {
	var b strings.Builder
	b.WriteString(`<div style="font-family:Arial,Helvetica,sans-serif">`)
	fmt.Fprintf(&b, "<h2>%s</h2>", htmlEscape(s.Name))
	if s.Description != "" {
		fmt.Fprintf(&b, "<p>%s</p>", htmlEscape(s.Description))
	}
	if qr == nil || len(qr.Data) == 0 {
		b.WriteString("<p>(no rows)</p></div>")
		return b.String()
	}
	cols := make([]string, 0, len(qr.Columns))
	for _, c := range qr.Columns {
		cols = append(cols, c.Name)
	}
	if len(cols) == 0 {
		cols = columnsFromRows(qr.Data)
	}
	b.WriteString(`<table cellspacing="0" cellpadding="6" border="1" style="border-collapse:collapse;font-size:13px"><thead><tr>`)
	for _, c := range cols {
		fmt.Fprintf(&b, "<th align=\"left\">%s</th>", htmlEscape(c))
	}
	b.WriteString("</tr></thead><tbody>")
	limit := 10
	if len(qr.Data) < limit {
		limit = len(qr.Data)
	}
	for _, row := range qr.Data[:limit] {
		b.WriteString("<tr>")
		for _, c := range cols {
			fmt.Fprintf(&b, "<td>%s</td>", htmlEscape(csvValue(row[c])))
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	if len(qr.Data) > limit {
		fmt.Fprintf(&b, `<p style="color:#888">Showing %d of %d rows, full data attached as CSV.</p>`, limit, len(qr.Data))
	}
	fmt.Fprintf(&b, `<p style="color:#888;font-size:12px">Generated %s</p>`, time.Now().UTC().Format(time.RFC3339))
	b.WriteString("</div>")
	return b.String()
}

// formatValue renders a scalar for subjects and bodies.
func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case []byte:
		return string(t)
	case float64:
		return strconvFloat(t)
	default:
		return fmt.Sprint(t)
	}
}

// strconvFloat formats a float without a trailing ".0" for integers.
func strconvFloat(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%v", f)
}

// safeFilename turns a schedule name into a file name stem.
func safeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "report"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "report"
	}
	if len([]rune(out)) > 60 {
		out = string([]rune(out)[:60])
	}
	return out
}

// orDefault returns v when non-empty, def otherwise.
func orDefault(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

// htmlEscape escapes text for HTML bodies.
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// sortStrings sorts a slice in place.
func sortStrings(v []string) { sort.Strings(v) }
