package models

import (
	"time"

	"gorm.io/datatypes"
)

// ReportSchedule maps the report_schedule table (superset.reports.models.ReportSchedule).
type ReportSchedule struct {
	BaseModel
	ExtraJSON             datatypes.JSON `gorm:"column:extra_json" json:"extra_json"`
	Type                  string         `gorm:"column:type;not null" json:"type"`
	Name                  string         `gorm:"column:name;not null" json:"name"`
	Description           string         `gorm:"column:description" json:"description"`
	ContextMarkdown       string         `gorm:"column:context_markdown" json:"context_markdown"`
	Active                bool           `gorm:"column:active;default:true;index" json:"active"`
	Crontab               string         `gorm:"column:crontab;not null" json:"crontab"`
	CreationMethod        string         `gorm:"column:creation_method;default:alerts_reports" json:"creation_method"`
	Timezone              string         `gorm:"column:timezone;not null;default:UTC" json:"timezone"`
	ReportFormat          string         `gorm:"column:report_format;default:PNG" json:"report_format"`
	SQL                   string         `gorm:"column:sql" json:"sql"`
	ChartID               *int           `gorm:"column:chart_id" json:"chart_id"`
	DashboardID           *int           `gorm:"column:dashboard_id" json:"dashboard_id"`
	DatabaseID            *int           `gorm:"column:database_id" json:"database_id"`
	RunAsType             string         `gorm:"column:run_as_type" json:"run_as_type"`
	RunAsFk               *int           `gorm:"column:run_as_fk" json:"run_as_fk"`
	RunAlertQueryAsType   string         `gorm:"column:run_alert_query_as_type" json:"run_alert_query_as_type"`
	RunAlertQueryAsFk     *int           `gorm:"column:run_alert_query_as_fk" json:"run_alert_query_as_fk"`
	LastEvalDttm          *time.Time     `gorm:"column:last_eval_dttm" json:"last_eval_dttm"`
	LastState             string         `gorm:"column:last_state;default:noop" json:"last_state"`
	LastValue             *float64       `gorm:"column:last_value" json:"last_value"`
	LastValueRowJSON      datatypes.JSON `gorm:"column:last_value_row_json" json:"last_value_row_json"`
	ValidatorType         string         `gorm:"column:validator_type" json:"validator_type"`
	ValidatorConfigJSON   datatypes.JSON `gorm:"column:validator_config_json" json:"validator_config_json"`
	LogRetention          int            `gorm:"column:log_retention;default:90" json:"log_retention"`
	GracePeriod           int            `gorm:"column:grace_period;default:14400" json:"grace_period"`
	WorkingTimeout        int            `gorm:"column:working_timeout;default:3600" json:"working_timeout"`
	ForceScreenshot       bool           `gorm:"column:force_screenshot;default:false" json:"force_screenshot"`
	CustomWidth           *int           `gorm:"column:custom_width" json:"custom_width"`
	CustomHeight          *int           `gorm:"column:custom_height" json:"custom_height"`
	RetryOnFailure        bool           `gorm:"column:retry_on_failure;not null;default:false" json:"retry_on_failure"`
	RetryMaxAttempts      int            `gorm:"column:retry_max_attempts;not null;default:3" json:"retry_max_attempts"`
	SendFailedReports     bool           `gorm:"column:send_failed_reports;not null;default:false" json:"send_failed_reports"`
	RetryNotifyOwners     bool           `gorm:"column:retry_notify_owners;not null;default:true" json:"retry_notify_owners"`
	RetryNotifyRecipients bool           `gorm:"column:retry_notify_recipients;not null;default:false" json:"retry_notify_recipients"`
	RetryAttempt          int            `gorm:"column:retry_attempt;not null;default:0" json:"retry_attempt"`
	RetryScheduledDttm    *time.Time     `gorm:"column:retry_scheduled_dttm" json:"retry_scheduled_dttm"`
	ExecutionOwner        string         `gorm:"column:execution_owner" json:"execution_owner"`
	ExecutionWindow       *time.Time     `gorm:"column:execution_window" json:"execution_window"`
	EmailSubject          string         `gorm:"column:email_subject" json:"email_subject"`
	IncludeCTA            *bool          `gorm:"column:include_cta;default:true" json:"include_cta"`
}

// TableName returns the original table name.
func (ReportSchedule) TableName() string { return "report_schedule" }

// ReportRecipient maps the report_recipient table (superset.reports.models.ReportRecipients).
type ReportRecipient struct {
	BaseModel
	Type                string         `gorm:"column:type;not null" json:"type"`
	RecipientConfigJSON datatypes.JSON `gorm:"column:recipient_config_json" json:"recipient_config_json"`
	ReportScheduleID    int            `gorm:"column:report_schedule_id;not null;index" json:"report_schedule_id"`
}

// TableName returns the original table name.
func (ReportRecipient) TableName() string { return "report_recipient" }

// ReportExecutionLog maps the report_execution_log table
// (superset.reports.models.ReportExecutionLog).
type ReportExecutionLog struct {
	BaseModel
	UUID             string         `gorm:"column:uuid;type:uuid" json:"uuid"`
	ScheduledDttm    time.Time      `gorm:"column:scheduled_dttm;not null" json:"scheduled_dttm"`
	StartDttm        *time.Time     `gorm:"column:start_dttm;index" json:"start_dttm"`
	EndDttm          *time.Time     `gorm:"column:end_dttm" json:"end_dttm"`
	Value            *float64       `gorm:"column:value" json:"value"`
	ValueRowJSON     datatypes.JSON `gorm:"column:value_row_json" json:"value_row_json"`
	State            string         `gorm:"column:state;not null" json:"state"`
	ErrorMessage     string         `gorm:"column:error_message" json:"error_message"`
	ReportScheduleID int            `gorm:"column:report_schedule_id;not null;index" json:"report_schedule_id"`
}

// TableName returns the original table name.
func (ReportExecutionLog) TableName() string { return "report_execution_log" }

// ReportScheduleUser maps the report_schedule_user association table.
type ReportScheduleUser struct {
	UserID           int `gorm:"column:user_id;primaryKey" json:"user_id"`
	ReportScheduleID int `gorm:"column:report_schedule_id;primaryKey" json:"report_schedule_id"`
}

// TableName returns the original table name.
func (ReportScheduleUser) TableName() string { return "report_schedule_user" }
