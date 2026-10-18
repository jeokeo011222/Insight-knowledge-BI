package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"superset/internal/models"
	apperrors "superset/internal/pkg/errors"
	"superset/internal/reports"
	"superset/internal/security"
	"superset/internal/server"
)

// RegisterReports mounts /api/v1/report/* (superset/reports/api.py).
func RegisterReports(r chi.Router, deps *Deps) {
	a := &reportAPI{deps: deps}

	r.Route("/report", func(rr chi.Router) {
		rr.Use(security.RequireUser)

		rr.With(security.RequirePermission(security.ActionRead, security.ResReportSchedule)).Get("/", a.list)
		rr.With(security.RequirePermission(security.ActionWrite, security.ResReportSchedule)).Post("/", a.create)
		rr.With(security.RequirePermission(security.ActionRead, security.ResReportSchedule)).Get("/{pk}", a.get)
		rr.With(security.RequirePermission(security.ActionWrite, security.ResReportSchedule)).Put("/{pk}", a.update)
		rr.With(security.RequirePermission(security.ActionDelete, security.ResReportSchedule)).Delete("/{pk}", a.remove)

		rr.With(security.RequirePermission(security.ActionRead, security.ResReportSchedule)).Get("/{pk}/log/", a.listLogs)
		rr.With(security.RequirePermission(security.ActionWrite, security.ResReportSchedule)).Post("/{pk}/log/", a.createLog)
		rr.With(security.RequirePermission(security.ActionRead, security.ResReportSchedule)).Get("/{pk}/log/{log_id}/", a.getLog)
	})
}

type reportAPI struct{ deps *Deps }

type reportPayload struct {
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	Crontab             string   `json:"crontab"`
	Timezone            string   `json:"timezone"`
	Description         string   `json:"description"`
	ContextMarkdown     string   `json:"context_markdown"`
	Active              *bool    `json:"active"`
	ChartID             *int     `json:"chart_id"`
	DashboardID         *int     `json:"dashboard_id"`
	DatabaseID          *int     `json:"database_id"`
	SQL                 string   `json:"sql"`
	ValidatorType       string   `json:"validator_type"`
	ValidatorConfigJSON any      `json:"validator_config_json"`
	ReportFormat        string   `json:"report_format"`
	Recipients          []any    `json:"recipients"`
	Owners              []int    `json:"owners"`
	CreationMethod      string   `json:"creation_method"`
	LastEvalDttm        string   `json:"last_eval_dttm"`
	LastState           string   `json:"last_state"`
	LastValue           *float64 `json:"last_value"`
	WorkingTimeout      *int     `json:"working_timeout"`
	LogRetention        *int     `json:"log_retention"`
	GracePeriod         *int     `json:"grace_period"`
	ForceScreenshot     *bool    `json:"force_screenshot"`
	EmailSubject        string   `json:"email_subject"`
	IncludeCTA          *bool    `json:"include_cta"`
	CustomWidth         *int     `json:"custom_width"`
	CustomHeight        *int     `json:"custom_height"`
}

// reportJSONBytes serialises a JSON column, defaulting to an empty object.
func reportJSONBytes(v any) []byte {
	s := jsonString(v)
	if s == "" {
		s = "{}"
	}
	return []byte(s)
}

// reportJSON renders a schedule with its recipients and owners.
func (a *reportAPI) reportJSON(r *http.Request, s *models.ReportSchedule) map[string]any {
	db := a.deps.DB.WithContext(r.Context())

	var recipients []models.ReportRecipient
	_ = db.Where("report_schedule_id = ?", s.ID).Order("id asc").Find(&recipients).Error
	recipientList := make([]map[string]any, 0, len(recipients))
	for i := range recipients {
		rc := &recipients[i]
		recipientList = append(recipientList, map[string]any{
			"id": rc.ID, "type": rc.Type,
			"recipient_config_json": jsonField(rc.RecipientConfigJSON),
		})
	}

	var owners []struct {
		ID       int
		Username string
	}
	_ = db.Raw(`
		SELECT u.id, u.username FROM ab_user u
		JOIN report_schedule_user rsu ON rsu.user_id = u.id
		WHERE rsu.report_schedule_id = ? ORDER BY u.username`, s.ID).Scan(&owners).Error
	ownerList := make([]map[string]any, 0, len(owners))
	for _, o := range owners {
		ownerList = append(ownerList, map[string]any{"id": o.ID, "username": o.Username})
	}

	return map[string]any{
		"id": s.ID, "name": s.Name, "type": s.Type, "description": s.Description,
		"context_markdown": s.ContextMarkdown, "active": s.Active, "crontab": s.Crontab,
		"timezone": s.Timezone, "creation_method": s.CreationMethod,
		"report_format": s.ReportFormat, "sql": s.SQL,
		"chart_id": s.ChartID, "dashboard_id": s.DashboardID, "database_id": s.DatabaseID,
		"validator_type":        s.ValidatorType,
		"validator_config_json": jsonField(s.ValidatorConfigJSON),
		"last_eval_dttm":        s.LastEvalDttm, "last_state": s.LastState,
		"last_value": s.LastValue, "last_value_row_json": jsonField(s.LastValueRowJSON),
		"working_timeout": s.WorkingTimeout, "log_retention": s.LogRetention,
		"grace_period": s.GracePeriod, "force_screenshot": s.ForceScreenshot,
		"email_subject": s.EmailSubject, "include_cta": s.IncludeCTA,
		"custom_width": s.CustomWidth, "custom_height": s.CustomHeight,
		"recipients": recipientList, "owners": ownerList,
		"created_on": s.CreatedOn, "changed_on": s.ChangedOn,
	}
}

// reportValidate enforces the alert/report specific requirements.
func reportValidate(r *http.Request, p *reportPayload) error {
	if strings.TrimSpace(p.Name) == "" {
		return apperrors.BadRequest("%s", localize(r, "error.common.name_required", "name is required"))
	}
	if strings.TrimSpace(p.Crontab) == "" {
		return apperrors.BadRequest("%s", localize(r, "error.report.crontab_required", "crontab is required"))
	}
	switch p.Type {
	case "Alert":
		if p.DatabaseID == nil || *p.DatabaseID == 0 {
			return apperrors.BadRequest("%s", localize(r, "error.report.database_required", "Alerts require a database"))
		}
		if strings.TrimSpace(p.SQL) == "" {
			return apperrors.BadRequest("%s", localize(r, "error.report.sql_required", "Alerts require SQL"))
		}
		if strings.TrimSpace(p.ValidatorType) == "" {
			return apperrors.BadRequest("%s", localize(r, "error.report.validator_type_required", "Alerts require a validator_type"))
		}
	case "Report":
		if (p.ChartID == nil || *p.ChartID == 0) && (p.DashboardID == nil || *p.DashboardID == 0) {
			return apperrors.BadRequest("%s", localize(r, "error.report.chart_or_dashboard_required", "Reports require a chart or dashboard"))
		}
	default:
		return apperrors.BadRequest("%s", localize(r, "error.report.invalid_type", "type must be Alert or Report"))
	}
	return nil
}

// reportRecipient normalises "a@b.c" or {type, recipient_config_json}.
func reportRecipient(r *http.Request, v any) (models.ReportRecipient, error) {
	switch t := v.(type) {
	case string:
		target := strings.TrimSpace(t)
		if target == "" {
			return models.ReportRecipient{}, apperrors.BadRequest("%s", localize(r, "error.report.recipient_required", "Recipient cannot be empty"))
		}
		return models.ReportRecipient{
			Type:                "email",
			RecipientConfigJSON: datatypes.JSON([]byte(mustJSON(map[string]any{"target": target}))),
		}, nil
	case map[string]any:
		rc := models.ReportRecipient{Type: "email"}
		if s, ok := t["type"].(string); ok && s != "" {
			rc.Type = s
		}
		cfg, ok := t["recipient_config_json"]
		if !ok {
			// Superset's v1 payload carries the address in `recipient_value`
			// (or `email`); normalise both to the canonical `target` key.
			normalised := map[string]any{}
			for k, v := range t {
				normalised[k] = v
			}
			for _, alias := range []string{"recipient_value", "email", "address"} {
				if v, ok := t[alias]; ok {
					if _, exists := normalised["target"]; !exists {
						normalised["target"] = v
					}
					delete(normalised, alias)
				}
			}
			cfg = normalised
		}
		rc.RecipientConfigJSON = datatypes.JSON(reportJSONBytes(cfg))
		return rc, nil
	}
	return models.ReportRecipient{}, apperrors.BadRequest("%s", localize(r, "error.report.invalid_recipient", "Invalid recipient"))
}

func (a *reportAPI) list(w http.ResponseWriter, r *http.Request) {
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.ReportSchedule{})
	if p.Search != "" {
		like := "%" + p.Search + "%"
		q = q.Where("name ILIKE ? OR description ILIKE ?", like, like)
	}
	if t := server.QueryStr(r, "type"); t != "" {
		q = q.Where("type = ?", t)
	}
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "id desc", func(s *models.ReportSchedule) any { return a.reportJSON(r, s) })
}

func (a *reportAPI) find(r *http.Request) (*models.ReportSchedule, error) {
	id, err := server.URLParamInt(r, "pk")
	if err != nil {
		return nil, err
	}
	var s models.ReportSchedule
	if err := a.deps.DB.WithContext(r.Context()).First(&s, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperrors.NotFound("%s", localize(r, "error.report.not_found", "Report schedule not found"))
		}
		return nil, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error()))
	}
	return &s, nil
}

func (a *reportAPI) get(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.reportJSON(r, s)})
}

func (a *reportAPI) create(w http.ResponseWriter, r *http.Request) {
	var p reportPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	if err := reportValidate(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	timeout := derefInt(p.WorkingTimeout, a.deps.Config.AlertReportsDefaultWorkingTimeout)
	if timeout <= 0 {
		timeout = 3600
	}
	retention := derefInt(p.LogRetention, a.deps.Config.AlertReportsDefaultRetention)
	if retention <= 0 {
		retention = 90
	}
	s := models.ReportSchedule{
		Type: p.Type, Name: p.Name, Description: p.Description,
		ContextMarkdown: p.ContextMarkdown, Active: boolOr(p.Active, true),
		Crontab: p.Crontab, Timezone: orDefault(p.Timezone, "UTC"),
		CreationMethod: orDefault(p.CreationMethod, "alerts_reports"),
		ReportFormat:   orDefault(p.ReportFormat, "PNG"), SQL: p.SQL,
		ChartID: p.ChartID, DashboardID: p.DashboardID, DatabaseID: p.DatabaseID,
		ValidatorType:       p.ValidatorType,
		ValidatorConfigJSON: datatypes.JSON(reportJSONBytes(p.ValidatorConfigJSON)),
		LastState:           orDefault(p.LastState, "noop"), LastValue: p.LastValue,
		WorkingTimeout: timeout, LogRetention: retention,
		GracePeriod:     derefInt(p.GracePeriod, 14400),
		ForceScreenshot: boolOr(p.ForceScreenshot, false),
		EmailSubject:    p.EmailSubject, IncludeCTA: p.IncludeCTA,
		CustomWidth: p.CustomWidth, CustomHeight: p.CustomHeight,
	}
	if p.LastEvalDttm != "" {
		s.LastEvalDttm = annotationParseTime(p.LastEvalDttm)
	}
	owners := p.Owners
	if len(owners) == 0 {
		if uid := userIDOf(r); uid > 0 {
			owners = []int{uid}
		}
	}
	err := a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&s).Error; err != nil {
			return err
		}
		for _, raw := range p.Recipients {
			rc, err := reportRecipient(r, raw)
			if err != nil {
				return err
			}
			rc.ReportScheduleID = s.ID
			if err := tx.Create(&rc).Error; err != nil {
				return err
			}
		}
		for _, uid := range owners {
			if err := tx.Create(&models.ReportScheduleUser{UserID: uid, ReportScheduleID: s.ID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.report.create_failed", "Could not create report schedule: %s", err.Error())))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(&s, s.ID).Error
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": a.reportJSON(r, &s)})
}

func (a *reportAPI) update(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var p reportPayload
	if err := security.DecodeBody(r, &p); err != nil {
		server.WriteError(w, err)
		return
	}
	// Validate the merged view so partial updates keep the invariants.
	merged := reportPayload{
		Name: orDefault(p.Name, s.Name), Type: orDefault(p.Type, s.Type),
		Crontab: orDefault(p.Crontab, s.Crontab), SQL: orDefault(p.SQL, s.SQL),
		ValidatorType: orDefault(p.ValidatorType, s.ValidatorType),
		ChartID:       s.ChartID, DashboardID: s.DashboardID, DatabaseID: s.DatabaseID,
	}
	if p.ChartID != nil {
		merged.ChartID = p.ChartID
	}
	if p.DashboardID != nil {
		merged.DashboardID = p.DashboardID
	}
	if p.DatabaseID != nil {
		merged.DatabaseID = p.DatabaseID
	}
	if err := reportValidate(r, &merged); err != nil {
		server.WriteError(w, err)
		return
	}

	updates := map[string]any{"changed_by_fk": intPtrNil(userIDOf(r))}
	if p.Name != "" {
		updates["name"] = p.Name
	}
	if p.Type != "" {
		updates["type"] = p.Type
	}
	if p.Crontab != "" {
		updates["crontab"] = p.Crontab
	}
	if p.Timezone != "" {
		updates["timezone"] = p.Timezone
	}
	if p.Description != "" {
		updates["description"] = p.Description
	}
	if p.ContextMarkdown != "" {
		updates["context_markdown"] = p.ContextMarkdown
	}
	if p.Active != nil {
		updates["active"] = *p.Active
	}
	if p.ChartID != nil {
		updates["chart_id"] = *p.ChartID
	}
	if p.DashboardID != nil {
		updates["dashboard_id"] = *p.DashboardID
	}
	if p.DatabaseID != nil {
		updates["database_id"] = *p.DatabaseID
	}
	if p.SQL != "" {
		updates["sql"] = p.SQL
	}
	if p.ValidatorType != "" {
		updates["validator_type"] = p.ValidatorType
	}
	if p.ValidatorConfigJSON != nil {
		updates["validator_config_json"] = jsonString(p.ValidatorConfigJSON)
	}
	if p.ReportFormat != "" {
		updates["report_format"] = p.ReportFormat
	}
	if p.CreationMethod != "" {
		updates["creation_method"] = p.CreationMethod
	}
	if p.LastState != "" {
		updates["last_state"] = p.LastState
	}
	if p.LastValue != nil {
		updates["last_value"] = *p.LastValue
	}
	if p.LastEvalDttm != "" {
		updates["last_eval_dttm"] = annotationParseTime(p.LastEvalDttm)
	}
	if p.WorkingTimeout != nil {
		updates["working_timeout"] = *p.WorkingTimeout
	}
	if p.LogRetention != nil {
		updates["log_retention"] = *p.LogRetention
	}
	if p.GracePeriod != nil {
		updates["grace_period"] = *p.GracePeriod
	}
	if p.ForceScreenshot != nil {
		updates["force_screenshot"] = *p.ForceScreenshot
	}
	if p.EmailSubject != "" {
		updates["email_subject"] = p.EmailSubject
	}
	if p.IncludeCTA != nil {
		updates["include_cta"] = *p.IncludeCTA
	}
	if p.CustomWidth != nil {
		updates["custom_width"] = *p.CustomWidth
	}
	if p.CustomHeight != nil {
		updates["custom_height"] = *p.CustomHeight
	}

	err = a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(s).Updates(updates).Error; err != nil {
			return err
		}
		if p.Recipients != nil {
			if err := tx.Where("report_schedule_id = ?", s.ID).Delete(&models.ReportRecipient{}).Error; err != nil {
				return err
			}
			for _, raw := range p.Recipients {
				rc, err := reportRecipient(r, raw)
				if err != nil {
					return err
				}
				rc.ReportScheduleID = s.ID
				if err := tx.Create(&rc).Error; err != nil {
					return err
				}
			}
		}
		if p.Owners != nil {
			if err := tx.Where("report_schedule_id = ?", s.ID).Delete(&models.ReportScheduleUser{}).Error; err != nil {
				return err
			}
			for _, uid := range p.Owners {
				if err := tx.Create(&models.ReportScheduleUser{UserID: uid, ReportScheduleID: s.ID}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		server.WriteError(w, apperrors.Conflict("%s", localize(r, "error.report.update_failed", "Could not update report schedule: %s", err.Error())))
		return
	}
	_ = a.deps.DB.WithContext(r.Context()).First(s, s.ID).Error
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": a.reportJSON(r, s)})
}

func (a *reportAPI) remove(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	if err := a.deps.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("report_schedule_id = ?", s.ID).Delete(&models.ReportRecipient{}).Error; err != nil {
			return err
		}
		if err := tx.Where("report_schedule_id = ?", s.ID).Delete(&models.ReportScheduleUser{}).Error; err != nil {
			return err
		}
		if err := tx.Where("report_schedule_id = ?", s.ID).Delete(&models.ReportExecutionLog{}).Error; err != nil {
			return err
		}
		return tx.Delete(s).Error
	}); err != nil {
		server.WriteError(w, apperrors.Internal("%s", localize(r, "error.common.db_error_detail", "Database error: %s", err.Error())))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"message": "OK"})
}

// Execution log ----------------------------------------------------------

func reportLogJSON(l *models.ReportExecutionLog) map[string]any {
	return map[string]any{
		"id": l.ID, "uuid": l.UUID, "scheduled_dttm": l.ScheduledDttm,
		"start_dttm": l.StartDttm, "end_dttm": l.EndDttm, "value": l.Value,
		"value_row_json": jsonField(l.ValueRowJSON), "state": l.State,
		"error_message":      nullIfEmpty(l.ErrorMessage),
		"report_schedule_id": l.ReportScheduleID, "created_on": l.CreatedOn,
	}
}

func (a *reportAPI) listLogs(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	p := parseListParams(r)
	q := a.deps.DB.WithContext(r.Context()).Model(&models.ReportExecutionLog{}).
		Where("report_schedule_id = ?", s.ID)
	q, filtersOK := applyListFilters(w, q, p)
	if !filtersOK {
		return
	}
	listResultW(w, r, q, p, "id desc", func(l *models.ReportExecutionLog) any { return reportLogJSON(l) })
}

// createLog runs the schedule synchronously for a manual trigger and returns
// the persisted execution log. A failed run still returns 201 with the log so
// the UI can display the error message.
func (a *reportAPI) createLog(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	ex := reports.NewExecutor(a.deps.Config, a.deps.Log, a.deps.DB)
	if a.deps.Thumbs != nil {
		ex.SetThumbnails(a.deps.Thumbs)
	}
	// The run is bounded by working_timeout, not by the client connection.
	ctx := context.WithoutCancel(r.Context())
	entry, execErr := ex.Execute(ctx, s.ID, reports.TriggerManual)
	if entry == nil {
		msg := "unknown error"
		if execErr != nil {
			msg = execErr.Error()
		}
		server.WriteError(w, apperrors.Internal("%s",
			localize(r, "error.report.execute_failed", "Could not execute report: %s", msg)))
		return
	}
	payload := reportLogJSON(entry)
	if execErr != nil {
		payload["execution_error"] = execErr.Error()
	}
	server.WriteJSON(w, http.StatusCreated, map[string]any{"result": payload})
}

func (a *reportAPI) getLog(w http.ResponseWriter, r *http.Request) {
	s, err := a.find(r)
	if err != nil {
		server.WriteError(w, err)
		return
	}
	logID, err := server.URLParamInt(r, "log_id")
	if err != nil {
		server.WriteError(w, err)
		return
	}
	var log models.ReportExecutionLog
	if err := a.deps.DB.WithContext(r.Context()).
		Where("report_schedule_id = ? AND id = ?", s.ID, logID).First(&log).Error; err != nil {
		server.WriteError(w, apperrors.NotFound("%s", localize(r, "error.report.execution_log_not_found", "Execution log not found")))
		return
	}
	server.WriteJSON(w, http.StatusOK, map[string]any{"result": reportLogJSON(&log)})
}
