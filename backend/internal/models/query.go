package models

import (
	"time"

	"gorm.io/datatypes"
)

// Query maps the query table (superset.models.sql_lab.Query).
type Query struct {
	BaseModel
	ExtraJSON            datatypes.JSON `gorm:"column:extra_json" json:"extra_json"`
	ClientID             string         `gorm:"column:client_id;unique;not null" json:"client_id"`
	DatabaseID           int            `gorm:"column:database_id;not null" json:"database_id"`
	TmpTableName         string         `gorm:"column:tmp_table_name" json:"tmp_table_name"`
	TmpSchemaName        string         `gorm:"column:tmp_schema_name" json:"tmp_schema_name"`
	UserID               *int           `gorm:"column:user_id" json:"user_id"`
	Status               string         `gorm:"column:status;default:pending" json:"status"`
	TabName              string         `gorm:"column:tab_name" json:"tab_name"`
	SQLEditorID          string         `gorm:"column:sql_editor_id;index" json:"sql_editor_id"`
	Schema               string         `gorm:"column:schema" json:"schema"`
	Catalog              string         `gorm:"column:catalog" json:"catalog"`
	SQL                  string         `gorm:"column:sql" json:"sql"`
	SelectSQL            string         `gorm:"column:select_sql" json:"select_sql"`
	ExecutedSQL          string         `gorm:"column:executed_sql" json:"executed_sql"`
	Limit                *int           `gorm:"column:limit" json:"limit"`
	LimitingFactor       string         `gorm:"column:limiting_factor;default:unknown" json:"limiting_factor"`
	SelectAsCTA          *bool          `gorm:"column:select_as_cta" json:"select_as_cta"`
	SelectAsCTAUsed      bool           `gorm:"column:select_as_cta_used;default:false" json:"select_as_cta_used"`
	CTASMethod           string         `gorm:"column:ctas_method;default:TABLE" json:"ctas_method"`
	Progress             int            `gorm:"column:progress;default:0" json:"progress"`
	Rows                 *int           `gorm:"column:rows" json:"rows"`
	ErrorMessage         string         `gorm:"column:error_message" json:"error_message"`
	ResultsKey           string         `gorm:"column:results_key;index" json:"results_key"`
	StartTime            *float64       `gorm:"column:start_time" json:"start_time"`
	StartRunningTime     *float64       `gorm:"column:start_running_time" json:"start_running_time"`
	EndTime              *float64       `gorm:"column:end_time" json:"end_time"`
	EndResultBackendTime *float64       `gorm:"column:end_result_backend_time" json:"end_result_backend_time"`
	TrackingURLRaw       string         `gorm:"column:tracking_url" json:"tracking_url_raw"`
}

// TableName returns the original table name.
func (Query) TableName() string { return "query" }

// SavedQuery maps the saved_query table (superset.models.sql_lab.SavedQuery).
type SavedQuery struct {
	BaseModel
	UUID               string         `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	ExtraJSON          datatypes.JSON `gorm:"column:extra_json" json:"extra_json"`
	UserID             *int           `gorm:"column:user_id" json:"user_id"`
	DbID               *int           `gorm:"column:db_id" json:"db_id"`
	Schema             string         `gorm:"column:schema" json:"schema"`
	Catalog            string         `gorm:"column:catalog" json:"catalog"`
	Label              string         `gorm:"column:label" json:"label"`
	Description        string         `gorm:"column:description" json:"description"`
	SQL                string         `gorm:"column:sql" json:"sql"`
	TemplateParameters string         `gorm:"column:template_parameters" json:"template_parameters"`
	Rows               *int           `gorm:"column:rows" json:"rows"`
	LastRun            *time.Time     `gorm:"column:last_run" json:"last_run"`
}

// TableName returns the original table name.
func (SavedQuery) TableName() string { return "saved_query" }

// TabState maps the tab_state table (superset.models.sql_lab.TabState).
type TabState struct {
	BaseModel
	ExtraJSON      datatypes.JSON `gorm:"column:extra_json" json:"extra_json"`
	UserID         int            `gorm:"column:user_id" json:"user_id"`
	Label          string         `gorm:"column:label" json:"label"`
	Active         bool           `gorm:"column:active;default:false" json:"active"`
	DatabaseID     int            `gorm:"column:database_id" json:"database_id"`
	Schema         string         `gorm:"column:schema" json:"schema"`
	Catalog        string         `gorm:"column:catalog" json:"catalog"`
	SQL            string         `gorm:"column:sql" json:"sql"`
	QueryLimit     *int           `gorm:"column:query_limit" json:"query_limit"`
	LatestQueryID  string         `gorm:"column:latest_query_id" json:"latest_query_id"`
	Autorun        bool           `gorm:"column:autorun;default:false" json:"autorun"`
	TemplateParams string         `gorm:"column:template_params" json:"template_params"`
	HideLeftBar    bool           `gorm:"column:hide_left_bar;default:false" json:"hide_left_bar"`
	SavedQueryID   *int           `gorm:"column:saved_query_id" json:"saved_query_id"`
}

// TableName returns the original table name.
func (TabState) TableName() string { return "tab_state" }

// TableSchema maps the table_schema table (superset.models.sql_lab.TableSchema).
type TableSchema struct {
	BaseModel
	ExtraJSON   datatypes.JSON `gorm:"column:extra_json" json:"extra_json"`
	TabStateID  int            `gorm:"column:tab_state_id" json:"tab_state_id"`
	DatabaseID  int            `gorm:"column:database_id;not null" json:"database_id"`
	Schema      string         `gorm:"column:schema" json:"schema"`
	Catalog     string         `gorm:"column:catalog" json:"catalog"`
	Table       string         `gorm:"column:table" json:"table"`
	Description string         `gorm:"column:description" json:"description"`
	Expanded    bool           `gorm:"column:expanded;default:false" json:"expanded"`
}

// TableName returns the original table name.
func (TableSchema) TableName() string { return "table_schema" }
