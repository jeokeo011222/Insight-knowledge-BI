package models

import (
	"time"

	"gorm.io/datatypes"
)

// Database maps the dbs table (superset.models.core.Database).
type Database struct {
	BaseModel
	UUID                  string `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	VerboseName           string `gorm:"column:verbose_name;unique" json:"verbose_name"`
	DatabaseName          string `gorm:"column:database_name;unique;not null" json:"database_name"`
	SQLAlchemyURI         string `gorm:"column:sqlalchemy_uri;not null" json:"sqlalchemy_uri"`
	Password              string `gorm:"column:password" json:"-"`
	CacheTimeout          *int   `gorm:"column:cache_timeout" json:"cache_timeout"`
	SelectAsCreateTableAs bool   `gorm:"column:select_as_create_table_as;default:false" json:"select_as_create_table_as"`
	ExposeInSQLlab        bool   `gorm:"column:expose_in_sqllab;default:true" json:"expose_in_sqllab"`
	ConfigurationMethod   string `gorm:"column:configuration_method;default:sqlalchemy_form" json:"configuration_method"`
	AllowRunAsync         bool   `gorm:"column:allow_run_async;default:false" json:"allow_run_async"`
	AllowFileUpload       bool   `gorm:"column:allow_file_upload;default:false" json:"allow_file_upload"`
	AllowCTAS             bool   `gorm:"column:allow_ctas;default:false" json:"allow_ctas"`
	AllowCVAS             bool   `gorm:"column:allow_cvas;default:false" json:"allow_cvas"`
	AllowDML              bool   `gorm:"column:allow_dml;default:false" json:"allow_dml"`
	// AllowMultiSchemaMetadataFetch mirrors Superset's flag of the same name:
	// when off, the dataset browser only walks the default schema instead of
	// every schema the connection can see.
	AllowMultiSchemaMetadataFetch bool `gorm:"column:allow_multi_schema_metadata_fetch;default:false" json:"allow_multi_schema_metadata_fetch"`
	// ExploreDatabase controls whether this connection shows up in Explore /
	// chart building. Disabling it keeps a connection available to SQL Lab only.
	ExploreDatabase     bool   `gorm:"column:explore_database;default:true" json:"explore_database"`
	ForceCTASSchema     string `gorm:"column:force_ctas_schema" json:"force_ctas_schema"`
	Extra               string `gorm:"column:extra" json:"extra"`
	EncryptedExtra      string `gorm:"column:encrypted_extra" json:"-"`
	ImpersonateUser     bool   `gorm:"column:impersonate_user;default:false" json:"impersonate_user"`
	ServerCert          string `gorm:"column:server_cert" json:"-"`
	IsManagedExternally bool   `gorm:"column:is_managed_externally;not null;default:false" json:"is_managed_externally"`
	ExternalURL         string `gorm:"column:external_url" json:"external_url"`

	// Connection health probe (数据源健康检查). The scheduler refreshes these
	// daily; the engine list paints a status dot from them.
	LastProbeAt    *time.Time `gorm:"column:last_probe_at" json:"last_probe_at"`
	LastProbeOK    *bool      `gorm:"column:last_probe_ok" json:"last_probe_ok"`
	LastProbeMs    int        `gorm:"column:last_probe_ms;default:0" json:"last_probe_ms"`
	LastProbeError string     `gorm:"column:last_probe_error;type:text" json:"last_probe_error"`
}

// TableName returns the original table name.
func (Database) TableName() string { return "dbs" }

// SSHTunnel maps the ssh_tunnels table (superset.databases.ssh_tunnel.models.SSHTunnel).
type SSHTunnel struct {
	BaseModel
	UUID               string         `gorm:"column:uuid;type:uuid;unique" json:"uuid"`
	ExtraJSON          datatypes.JSON `gorm:"column:extra_json" json:"extra_json"`
	DatabaseID         int            `gorm:"column:database_id;unique;not null" json:"database_id"`
	ServerAddress      string         `gorm:"column:server_address" json:"server_address"`
	ServerPort         *int           `gorm:"column:server_port" json:"server_port"`
	Username           string         `gorm:"column:username" json:"-"`
	Password           string         `gorm:"column:password" json:"-"`
	PrivateKey         string         `gorm:"column:private_key" json:"-"`
	PrivateKeyPassword string         `gorm:"column:private_key_password" json:"-"`
	ServerHostKey      string         `gorm:"column:server_host_key" json:"server_host_key"`
}

// TableName returns the original table name.
func (SSHTunnel) TableName() string { return "ssh_tunnels" }

// DatabaseUserOAuth2Tokens maps the database_user_oauth2_tokens table.
type DatabaseUserOAuth2Tokens struct {
	BaseModel
	UserID                int        `gorm:"column:user_id;not null;uniqueIndex:idx_user_id_database_id" json:"user_id"`
	DatabaseID            int        `gorm:"column:database_id;not null;uniqueIndex:idx_user_id_database_id" json:"database_id"`
	AccessToken           string     `gorm:"column:access_token" json:"-"`
	AccessTokenExpiration *time.Time `gorm:"column:access_token_expiration" json:"access_token_expiration"`
	RefreshToken          string     `gorm:"column:refresh_token" json:"-"`
}

// TableName returns the original table name.
func (DatabaseUserOAuth2Tokens) TableName() string { return "database_user_oauth2_tokens" }
