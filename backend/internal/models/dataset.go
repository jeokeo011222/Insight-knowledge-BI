package models

import (
	"time"

	"gorm.io/datatypes"
)

// SqlaTable maps the tables table (superset.connectors.sqla.models.SqlaTable).
type SqlaTable struct {
	BaseModel
	UUID                 string         `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	DeletedAt            *time.Time     `gorm:"column:deleted_at;index" json:"deleted_at"`
	TableNameStr         string         `gorm:"column:table_name;not null" json:"table_name"`
	MainDttmCol          string         `gorm:"column:main_dttm_col" json:"main_dttm_col"`
	CurrencyCodeColumn   string         `gorm:"column:currency_code_column" json:"currency_code_column"`
	DatabaseID           int            `gorm:"column:database_id;not null" json:"database_id"`
	FetchValuesPredicate string         `gorm:"column:fetch_values_predicate" json:"fetch_values_predicate"`
	Schema               string         `gorm:"column:schema" json:"schema"`
	Catalog              string         `gorm:"column:catalog" json:"catalog"`
	SQL                  string         `gorm:"column:sql" json:"sql"`
	IsSQLLabView         bool           `gorm:"column:is_sqllab_view;default:false" json:"is_sqllab_view"`
	TemplateParams       string         `gorm:"column:template_params" json:"template_params"`
	Extra                string         `gorm:"column:extra" json:"extra"`
	NormalizeColumns     bool           `gorm:"column:normalize_columns;default:false" json:"normalize_columns"`
	AlwaysFilterMainDttm bool           `gorm:"column:always_filter_main_dttm;default:false" json:"always_filter_main_dttm"`
	Folders              datatypes.JSON `gorm:"column:folders" json:"folders"`
	Description          string         `gorm:"column:description" json:"description"`
	DefaultEndpoint      string         `gorm:"column:default_endpoint" json:"default_endpoint"`
	IsFeatured           bool           `gorm:"column:is_featured;default:false" json:"is_featured"`
	FilterSelectEnabled  bool           `gorm:"column:filter_select_enabled;default:true" json:"filter_select_enabled"`
	Offset               int            `gorm:"column:offset;default:0" json:"offset"`
	CacheTimeout         *int           `gorm:"column:cache_timeout" json:"cache_timeout"`
	Params               string         `gorm:"column:params" json:"params"`
	Perm                 string         `gorm:"column:perm" json:"perm"`
	SchemaPerm           string         `gorm:"column:schema_perm" json:"schema_perm"`
	CatalogPerm          string         `gorm:"column:catalog_perm" json:"catalog_perm"`
	IsManagedExternally  bool           `gorm:"column:is_managed_externally;not null;default:false" json:"is_managed_externally"`
	ExternalURL          string         `gorm:"column:external_url" json:"external_url"`

	// Data refresh (数据集刷新). RefreshIntervalSec == 0 means "manual only";
	// any positive value is the period the scheduler waits between refreshes.
	// The last_* columns are the observable outcome, so the UI can show whether
	// the most recent run succeeded without digging through logs.
	RefreshIntervalSec    int        `gorm:"column:refresh_interval_sec;not null;default:0" json:"refresh_interval_sec"`
	LastRefreshedAt       *time.Time `gorm:"column:last_refreshed_at" json:"last_refreshed_at"`
	LastRefreshStatus     string     `gorm:"column:last_refresh_status;size:16" json:"last_refresh_status"`
	LastRefreshError      string     `gorm:"column:last_refresh_error;type:text" json:"last_refresh_error"`
	LastRefreshDurationMS int64      `gorm:"column:last_refresh_duration_ms;not null;default:0" json:"last_refresh_duration_ms"`
	LastRefreshTrigger    string     `gorm:"column:last_refresh_trigger;size:16" json:"last_refresh_trigger"`
}

// TableName returns the original table name.
func (SqlaTable) TableName() string { return "tables" }

// TableColumn maps the table_columns table (superset.connectors.sqla.models.TableColumn).
type TableColumn struct {
	BaseModel
	UUID             string `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	ColumnName       string `gorm:"column:column_name;not null;uniqueIndex:uq_table_columns_table_id_column_name" json:"column_name"`
	VerboseName      string `gorm:"column:verbose_name" json:"verbose_name"`
	IsActive         bool   `gorm:"column:is_active;default:true" json:"is_active"`
	Type             string `gorm:"column:type" json:"type"`
	AdvancedDataType string `gorm:"column:advanced_data_type" json:"advanced_data_type"`
	Groupby          bool   `gorm:"column:groupby;default:true" json:"groupby"`
	Filterable       bool   `gorm:"column:filterable;default:true" json:"filterable"`
	Description      string `gorm:"column:description" json:"description"`
	TableID          int    `gorm:"column:table_id;index;uniqueIndex:uq_table_columns_table_id_column_name" json:"table_id"`
	IsDttm           bool   `gorm:"column:is_dttm;default:false" json:"is_dttm"`
	Expression       string `gorm:"column:expression" json:"expression"`
	PythonDateFormat string `gorm:"column:python_date_format" json:"python_date_format"`
	DatetimeFormat   string `gorm:"column:datetime_format" json:"datetime_format"`
	Extra            string `gorm:"column:extra" json:"extra"`

	// Column-level masking (合规脱敏). MaskType "" means "show the raw value".
	// Supported: phone, idcard, email, name, full, custom.
	MaskType string `gorm:"column:mask_type;size:32" json:"mask_type"`
	// MaskRule carries the custom rule (a regular expression whose matches are
	// replaced with '*'). Ignored for the built-in types.
	MaskRule string `gorm:"column:mask_rule;size:255" json:"mask_rule"`

	// Agent field exposure (Agent 可见性). A column that is not agent_visible
	// does not exist as far as the Agent plane is concerned: schema_reader omits
	// it and the SQL guard rejects any statement that names it. This is stronger
	// than result masking, because the model never learns the column exists and
	// therefore cannot put it in a SELECT list. Existing rows default to visible.
	AgentVisible bool `gorm:"column:agent_visible;not null;default:true" json:"agent_visible"`
	// PIILevel is a coarse sensitivity class (none|low|high), independent of the
	// mask shape. "high" columns are fully masked on the Agent path even when no
	// explicit MaskType is configured; "low" relies on MaskType alone.
	PIILevel string `gorm:"column:pii_level;size:16;not null;default:none" json:"pii_level"`
}

// TableName returns the original table name.
func (TableColumn) TableName() string { return "table_columns" }

// SqlMetric maps the sql_metrics table (superset.connectors.sqla.models.SqlMetric).
type SqlMetric struct {
	BaseModel
	UUID        string `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	MetricName  string `gorm:"column:metric_name;not null;uniqueIndex:uq_sql_metrics_table_id_metric_name" json:"metric_name"`
	VerboseName string `gorm:"column:verbose_name" json:"verbose_name"`
	MetricType  string `gorm:"column:metric_type" json:"metric_type"`
	Description string `gorm:"column:description" json:"description"`
	D3Format    string `gorm:"column:d3format" json:"d3format"`
	Currency    string `gorm:"column:currency" json:"currency"`
	WarningText string `gorm:"column:warning_text" json:"warning_text"`
	TableID     int    `gorm:"column:table_id;index;uniqueIndex:uq_sql_metrics_table_id_metric_name" json:"table_id"`
	Expression  string `gorm:"column:expression;not null" json:"expression"`
	Extra       string `gorm:"column:extra" json:"extra"`
}

// TableName returns the original table name.
func (SqlMetric) TableName() string { return "sql_metrics" }

// SqlatableUser maps the sqlatable_user association table.
type SqlatableUser struct {
	UserID  int `gorm:"column:user_id;primaryKey" json:"user_id"`
	TableID int `gorm:"column:table_id;primaryKey" json:"table_id"`
}

// TableName returns the original table name.
func (SqlatableUser) TableName() string { return "sqlatable_user" }
