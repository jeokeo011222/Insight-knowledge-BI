// Package config loads Superset-compatible configuration from environment
// variables and an optional YAML config file, mirroring superset/config.py.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Config is the root configuration structure. Field names map 1:1 to the
// Python config keys (uppercase snake case) via the `mapstructure` tags.
type Config struct {
	// Core
	SecretKey        string `mapstructure:"SUPERSET_SECRET_KEY"`
	SQLAlchemyURI    string `mapstructure:"SQLALCHEMY_DATABASE_URI"`
	RedisURL         string `mapstructure:"REDIS_URL"`
	SupersetEnv      string `mapstructure:"SUPERSET_ENV"`
	DataDir          string `mapstructure:"SUPERSET_HOME"`
	AppName          string `mapstructure:"APP_NAME"`
	AppIcon          string `mapstructure:"APP_ICON"`
	WebserverPort    int    `mapstructure:"WEBserver_PORT"`
	WebserverTimeout int    `mapstructure:"SUPERSET_WEBSERVER_TIMEOUT"`
	Debug            bool   `mapstructure:"FLASK_DEBUG"`
	ShowStacktrace   bool   `mapstructure:"SHOW_STACKTRACE"`
	HashAlgorithm    string `mapstructure:"HASH_ALGORITHM"`

	// Security / auth
	AuthType                   string `mapstructure:"AUTH_TYPE"`
	AuthPasswordMinLength      int    `mapstructure:"AUTH_PASSWORD_MIN_LENGTH"`
	FabPasswordComplexity      bool   `mapstructure:"FAB_PASSWORD_COMPLEXITY_ENABLED"`
	WTFCSRFEnabled             bool   `mapstructure:"WTF_CSRF_ENABLED"`
	WTFCSRFTimeLimit           int    `mapstructure:"WTF_CSRF_TIME_LIMIT"`
	PublicRoleLike             string `mapstructure:"PUBLIC_ROLE_LIKE"`
	GuestRoleName              string `mapstructure:"GUEST_ROLE_NAME"`
	GuestTokenJWTSecret        string `mapstructure:"GUEST_TOKEN_JWT_SECRET"`
	GuestTokenJWTAlgo          string `mapstructure:"GUEST_TOKEN_JWT_ALGO"`
	GuestTokenHeaderName       string `mapstructure:"GUEST_TOKEN_HEADER_NAME"`
	GuestTokenJWTExpSeconds    int    `mapstructure:"GUEST_TOKEN_JWT_EXP_SECONDS"`
	GuestTokenJWTAudience      string `mapstructure:"GUEST_TOKEN_JWT_AUDIENCE"`
	GuestTokenRevocation       bool   `mapstructure:"GUEST_TOKEN_REVOCATION_ENABLED"`
	FabAPIKeyEnabled           bool   `mapstructure:"FAB_API_KEY_ENABLED"`
	PreventUnsafeDBConnections bool   `mapstructure:"PREVENT_UNSAFE_DB_CONNECTIONS"`
	SessionCookieHTTPOnly      bool   `mapstructure:"SESSION_COOKIE_HTTPONLY"`
	SessionCookieSecure        bool   `mapstructure:"SESSION_COOKIE_SECURE"`
	SessionCookieSameSite      string `mapstructure:"SESSION_COOKIE_SAMESITE"`
	SessionServerSide          bool   `mapstructure:"SESSION_SERVER_SIDE"`

	// Database / metadata
	SQLAlchemyExamplesURI   string   `mapstructure:"SQLALCHEMY_EXAMPLES_URI"`
	DefaultDBID             *int     `mapstructure:"DEFAULT_DB_ID"`
	PreferredDatabases      []string `mapstructure:"PREFERRED_DATABASES"`
	TestDatabaseConnTimeout int      `mapstructure:"TEST_DATABASE_CONNECTION_TIMEOUT"`
	SQLADAOMaxPageSize      int      `mapstructure:"SQLALCHEMY_DAO_MAX_PAGE_SIZE"`

	// Cache
	CacheDefaultTimeout         int            `mapstructure:"CACHE_DEFAULT_TIMEOUT"`
	DataCacheConfig             map[string]any `mapstructure:"DATA_CACHE_CONFIG"`
	FilterStateCacheTimeout     int            `mapstructure:"FILTER_STATE_CACHE_TIMEOUT"`
	ExploreFormDataCacheTimeout int            `mapstructure:"EXPLORE_FORM_DATA_CACHE_TIMEOUT"`
	ThumbnailCacheTimeout       int            `mapstructure:"THUMBNAIL_CACHE_TIMEOUT"`
	StoreCacheKeysInMetadataDB  bool           `mapstructure:"STORE_CACHE_KEYS_IN_METADATA_DB"`

	// Workers / queues
	Workers                       int  `mapstructure:"WORKERS"`
	GLOBAL_ASYNC_QUERIES          bool `mapstructure:"GLOBAL_ASYNC_QUERIES"`
	GlobalAsyncQueriesMinCacheTTL int  `mapstructure:"GLOBAL_ASYNC_QUERIES_MIN_CACHE_TTL"`
	ResultsBackendUseMsgpack      bool `mapstructure:"RESULTS_BACKEND_USE_MSGPACK"`

	// SQL Lab / queries
	RowLimit                    int `mapstructure:"ROW_LIMIT"`
	SamplesRowLimit             int `mapstructure:"SAMPLES_ROW_LIMIT"`
	SQLMaxRow                   int `mapstructure:"SQL_MAX_ROW"`
	TableVizMaxRowServer        int `mapstructure:"TABLE_VIZ_MAX_ROW_SERVER"`
	DisplayMaxRow               int `mapstructure:"DISPLAY_MAX_ROW"`
	DefaultSqllabLimit          int `mapstructure:"DEFAULT_SQLLAB_LIMIT"`
	SqllabTimeout               int `mapstructure:"SQLLAB_TIMEOUT"`
	SqllabValidationTimeout     int `mapstructure:"SQLLAB_VALIDATION_TIMEOUT"`
	SqllabAsyncTimeLimitSec     int `mapstructure:"SQLLAB_ASYNC_TIME_LIMIT_SEC"`
	FilterSelectRowLimit        int `mapstructure:"FILTER_SELECT_ROW_LIMIT"`
	NativeFilterDefaultRowLimit int `mapstructure:"NATIVE_FILTER_DEFAULT_ROW_LIMIT"`

	// Alerts & reports
	AlertReportsDefaultWorkingTimeout int    `mapstructure:"ALERT_REPORTS_DEFAULT_WORKING_TIMEOUT"`
	AlertReportsDefaultRetention      int    `mapstructure:"ALERT_REPORTS_DEFAULT_RETENTION"`
	AlertReportsDefaultCronValue      string `mapstructure:"ALERT_REPORTS_DEFAULT_CRON_VALUE"`
	AlertReportsNotificationDryRun    bool   `mapstructure:"ALERT_REPORTS_NOTIFICATION_DRY_RUN"`
	AlertMinimumInterval              int    `mapstructure:"ALERT_MINIMUM_INTERVAL"`
	ReportMinimumInterval             int    `mapstructure:"REPORT_MINIMUM_INTERVAL"`

	// SMTP / Slack
	SMTPHost      string `mapstructure:"SMTP_HOST"`
	SMTPPort      int    `mapstructure:"SMTP_PORT"`
	SMTPUser      string `mapstructure:"SMTP_USER"`
	SMTPPassword  string `mapstructure:"SMTP_PASSWORD"`
	SMTPMailFrom  string `mapstructure:"SMTP_MAIL_FROM"`
	SMTPStartTLS  bool   `mapstructure:"SMTP_STARTTLS"`
	SMTPSSL       bool   `mapstructure:"SMTP_SSL"`
	SlackAPIToken string `mapstructure:"SLACK_API_TOKEN"`

	// Screenshot / webdriver
	WebdriverBaseURL             string `mapstructure:"WEBDRIVER_BASEURL"`
	WebdriverBaseURLUserFriendly string `mapstructure:"WEBDRIVER_BASEURL_USER_FRIENDLY"`
	ScreenshotLocateWait         int    `mapstructure:"SCREENSHOT_LOCATE_WAIT"`
	ScreenshotLoadWait           int    `mapstructure:"SCREENSHOT_LOAD_WAIT"`

	// Upload / import / export
	UploadFolder           string   `mapstructure:"UPLOAD_FOLDER"`
	UploadMaxFileSizeBytes int64    `mapstructure:"UPLOAD_MAX_FILE_SIZE_BYTES"`
	CSVExtensions          []string `mapstructure:"CSV_EXTENSIONS"`
	ExcelExtensions        []string `mapstructure:"EXCEL_EXTENSIONS"`
	AllowedExtensions      []string `mapstructure:"ALLOWED_EXTENSIONS"`
	ZippedFileMaxSize      int64    `mapstructure:"ZIPPED_FILE_MAX_SIZE"`

	// Retention / versioning
	EnableVersioningCapture     bool `mapstructure:"ENABLE_VERSIONING_CAPTURE"`
	VersionHistoryRetentionDays int  `mapstructure:"VERSION_HISTORY_RETENTION_DAYS"`
	SoftDeleteRetentionDays     int  `mapstructure:"SOFT_DELETE_RETENTION_DAYS"`

	// SSH tunneling
	SSHTunnelManagerClass     string  `mapstructure:"SSH_TUNNEL_MANAGER_CLASS"`
	SSHTunnelLocalBindAddress string  `mapstructure:"SSH_TUNNEL_LOCAL_BIND_ADDRESS"`
	SSHTunnelTimeoutSec       float64 `mapstructure:"SSH_TUNNEL_TIMEOUT_SEC"`

	// WebSocket / realtime
	WebsocketEnable               bool   `mapstructure:"WEBSOCKET_ENABLE"`
	WebsocketURL                  string `mapstructure:"WEBSOCKET_URL"`
	WebsocketJWTSecret            string `mapstructure:"WEBSOCKET_JWT_SECRET"`
	WebsocketJWTExpirationSeconds int    `mapstructure:"WEBSOCKET_JWT_EXPIRATION_SECONDS"`

	// i18n / format
	BabelDefaultLocale       string                       `mapstructure:"BABEL_DEFAULT_LOCALE"`
	Languages                map[string]map[string]string `mapstructure:"LANGUAGES"`
	Currencies               []string                     `mapstructure:"CURRENCIES"`
	DefaultTimeFilter        string                       `mapstructure:"DEFAULT_TIME_FILTER"`
	DefaultRelativeStartTime string                       `mapstructure:"DEFAULT_RELATIVE_START_TIME"`
	DefaultRelativeEndTime   string                       `mapstructure:"DEFAULT_RELATIVE_END_TIME"`
	CSVExportEncoding        string                       `mapstructure:"CSV_EXPORT_ENCODING"`

	// Feature flags
	FeatureFlags map[string]bool `mapstructure:"FEATURE_FLAGS"`

	// Logging
	LogFormat        string `mapstructure:"LOG_FORMAT"`
	LogLevel         string `mapstructure:"LOG_LEVEL"`
	EnableTimeRotate bool   `mapstructure:"ENABLE_TIME_ROTATE"`
	LogFilename      string `mapstructure:"FILENAME"`
	BackupCount      int    `mapstructure:"BACKUP_COUNT"`

	// Theme
	ThemeDefault     map[string]any `mapstructure:"THEME_DEFAULT"`
	ThemeDark        map[string]any `mapstructure:"THEME_DARK"`
	ThemeDefaultMode string         `mapstructure:"THEME_DEFAULT_MODE"`

	// Dashboard
	DashboardAutoRefreshMode           string `mapstructure:"DASHBOARD_AUTO_REFRESH_MODE"`
	SupersetDashboardPositionDataLimit int    `mapstructure:"SUPERSET_DASHBOARD_POSITION_DATA_LIMIT"`

	// Misc
	VizTypeDenylist     []string          `mapstructure:"VIZ_TYPE_DENYLIST"`
	TimeGrainDenylist   []string          `mapstructure:"TIME_GRAIN_DENYLIST"`
	TimeGrainAddons     map[string]string `mapstructure:"TIME_GRAIN_ADDONS"`
	MapboxAPIKey        string            `mapstructure:"MAPBOX_API_KEY"`
	DefaultMapRenderer  string            `mapstructure:"DEFAULT_MAP_RENDERER"`
	TroubleshootingLink string            `mapstructure:"TROUBLESHOOTING_LINK"`
	ScarfAnalytics      bool              `mapstructure:"SCARF_ANALYTICS"`

	// AI Agent Plane
	AIAssistantEnabled bool   `mapstructure:"AI_ASSISTANT_ENABLED"`
	AILLMProvider      string `mapstructure:"AI_LLM_PROVIDER"` // ollama|openai_compatible
	AILLMBaseURL       string `mapstructure:"AI_LLM_BASE_URL"`
	AILLMAPIKey        string `mapstructure:"AI_LLM_API_KEY"`
	AILLMModel         string `mapstructure:"AI_LLM_MODEL"`
	AILLMTimeoutSec    int    `mapstructure:"AI_LLM_TIMEOUT_SEC"`
	AIEmbeddingModel   string `mapstructure:"AI_EMBEDDING_MODEL"`
	// AITemperature pins sampling for every model call. It must be sent
	// explicitly (0 is a legal value), otherwise the provider default (~1.0)
	// makes "same question, same SQL" impossible to reproduce.
	//
	// The default is 0, not 0.2. A BI Agent is a deterministic function of the
	// question plus the schema: two runs that disagree produce two different
	// numbers for the same question, and nobody can tell which one to trust.
	// Operators who want a "creative" agent can raise it, but the product's
	// default claim — same question, same SQL — has to hold out of the box.
	AITemperature float64 `mapstructure:"AI_TEMPERATURE"`
	// AIMaxTokens caps the completion length; 0 keeps the provider default.
	AIMaxTokens int `mapstructure:"AI_MAX_TOKENS"`
	// AISeed enables a deterministic seed when the provider supports it. A
	// non-zero default is deliberate: temperature 0 removes most sampling
	// variance but not all of it (batching, MoE routing), and a pinned seed is
	// the only remaining lever the provider exposes.
	AISeed int `mapstructure:"AI_SEED"`

	// Agent guardrails
	AIAgentMaxSteps        int  `mapstructure:"AI_AGENT_MAX_STEPS"`
	AIAgentMaxToolCalls    int  `mapstructure:"AI_AGENT_MAX_TOOL_CALLS"`
	AIAgentRunTimeoutSec   int  `mapstructure:"AI_AGENT_RUN_TIMEOUT_SEC"`
	AIAgentTokenBudget     int  `mapstructure:"AI_AGENT_TOKEN_BUDGET"`
	AIWriteToolsEnabled    bool `mapstructure:"AI_WRITE_TOOLS_ENABLED"`
	AIWriteConfirmRequired bool `mapstructure:"AI_WRITE_CONFIRM_REQUIRED"`
	AISnapshotEnabled      bool `mapstructure:"AI_SNAPSHOT_ENABLED"`

	// Resource & cost
	AIMaxTokensPerDay int `mapstructure:"AI_MAX_TOKENS_PER_DAY"`
	AIQueryTimeoutSec int `mapstructure:"AI_QUERY_TIMEOUT_SEC"`
	AIQueryMaxRows    int `mapstructure:"AI_QUERY_MAX_ROWS"`
	// AIExplainMaxRows is the EXPLAIN cost gate: a plan whose sequential scan
	// is estimated above this many rows is rejected before execution.
	AIExplainMaxRows  int `mapstructure:"AI_EXPLAIN_MAX_ROWS"`
	AISamplesRowLimit int `mapstructure:"AI_SAMPLES_ROW_LIMIT"`
	AICacheTTLSec     int `mapstructure:"AI_CACHE_TTL_SEC"`
	// AIReplyReplayTTLSec is how long an answer may be replayed for an identical
	// question (same user, same dataset, unchanged result rows). 0 disables the
	// replay layer; the default is short on purpose, because "same question"
	// must not silently mean "last hour's answer".
	AIReplyReplayTTLSec int `mapstructure:"AI_REPLAY_TTL_SEC"`
	AIInsightMaxLen     int `mapstructure:"AI_INSIGHT_MAX_LEN"`
	// AISchemaCompactThreshold switches wide datasets from "whole schema" to
	// relevance-ranked context (see internal/ai/schema_context.go).
	AISchemaCompactThreshold int `mapstructure:"AI_SCHEMA_COMPACT_THRESHOLD"`
	// AISQLMemoryTopK is how many confirmed NL→SQL examples the planner injects.
	AISQLMemoryTopK int `mapstructure:"AI_SQL_MEMORY_TOP_K"`

	// Derived / runtime
	Version string
	SHA     string
}

// defaults mirrors the Python config.py defaults.
var defaults = map[string]any{
	"SUPERSET_SECRET_KEY":        "CHANGE_ME_TO_A_COMPLEX_RANDOM_SECRET",
	"SQLALCHEMY_DATABASE_URI":    "postgresql://superset:superset@127.0.0.1:5533/superset",
	"REDIS_URL":                  "redis://127.0.0.1:6379/0",
	"SUPERSET_ENV":               "production",
	"SUPERSET_HOME":              "~/.superset",
	"APP_NAME":                   "Superset",
	"APP_ICON":                   "/static/assets/images/superset-logo-horiz.png",
	"WEBserver_PORT":             8088,
	"SUPERSET_WEBSERVER_TIMEOUT": 60,
	"FLASK_DEBUG":                false,
	"SHOW_STACKTRACE":            false,
	"HASH_ALGORITHM":             "sha256",

	"AUTH_TYPE":                       "AUTH_DB",
	"AUTH_PASSWORD_MIN_LENGTH":        8,
	"FAB_PASSWORD_COMPLEXITY_ENABLED": true,
	"WTF_CSRF_ENABLED":                true,
	"WTF_CSRF_TIME_LIMIT":             604800,
	"PUBLIC_ROLE_LIKE":                "",
	"GUEST_ROLE_NAME":                 "Public",
	"GUEST_TOKEN_JWT_SECRET":          "test-guest-secret-change-me",
	"GUEST_TOKEN_JWT_ALGO":            "HS256",
	"GUEST_TOKEN_HEADER_NAME":         "X-GuestToken",
	"GUEST_TOKEN_JWT_EXP_SECONDS":     300,
	"GUEST_TOKEN_REVOCATION_ENABLED":  false,
	"FAB_API_KEY_ENABLED":             false,
	"PREVENT_UNSAFE_DB_CONNECTIONS":   true,
	"SESSION_COOKIE_HTTPONLY":         true,
	"SESSION_COOKIE_SECURE":           false,
	"SESSION_COOKIE_SAMESITE":         "Lax",
	"SESSION_SERVER_SIDE":             false,

	"SQLALCHEMY_EXAMPLES_URI":          "postgresql://superset:superset@127.0.0.1:5533/superset",
	"PREFERRED_DATABASES":              []string{"PostgreSQL", "Presto", "MySQL", "SQLite"},
	"TEST_DATABASE_CONNECTION_TIMEOUT": 30,
	"SQLALCHEMY_DAO_MAX_PAGE_SIZE":     1000,

	"CACHE_DEFAULT_TIMEOUT":           86400,
	"DATA_CACHE_CONFIG":               map[string]any{"CACHE_TYPE": "SimpleCache"},
	"FILTER_STATE_CACHE_TIMEOUT":      7776000,
	"EXPLORE_FORM_DATA_CACHE_TIMEOUT": 604800,
	"THUMBNAIL_CACHE_TIMEOUT":         604800,
	"STORE_CACHE_KEYS_IN_METADATA_DB": false,

	"WORKERS":                            4,
	"GLOBAL_ASYNC_QUERIES":               false,
	"GLOBAL_ASYNC_QUERIES_MIN_CACHE_TTL": 300,
	"RESULTS_BACKEND_USE_MSGPACK":        true,

	"ROW_LIMIT":                       50000,
	"SAMPLES_ROW_LIMIT":               1000,
	"SQL_MAX_ROW":                     100000,
	"TABLE_VIZ_MAX_ROW_SERVER":        500000,
	"DISPLAY_MAX_ROW":                 10000,
	"DEFAULT_SQLLAB_LIMIT":            1000,
	"SQLLAB_TIMEOUT":                  30,
	"SQLLAB_VALIDATION_TIMEOUT":       10,
	"SQLLAB_ASYNC_TIME_LIMIT_SEC":     21600,
	"FILTER_SELECT_ROW_LIMIT":         10000,
	"NATIVE_FILTER_DEFAULT_ROW_LIMIT": 1000,

	"ALERT_REPORTS_DEFAULT_WORKING_TIMEOUT": 3600,
	"ALERT_REPORTS_DEFAULT_RETENTION":       90,
	"ALERT_REPORTS_DEFAULT_CRON_VALUE":      "0 0 * * *",
	"ALERT_REPORTS_NOTIFICATION_DRY_RUN":    false,
	"ALERT_MINIMUM_INTERVAL":                0,
	"REPORT_MINIMUM_INTERVAL":               0,

	"SMTP_HOST":       "localhost",
	"SMTP_PORT":       25,
	"SMTP_USER":       "superset",
	"SMTP_PASSWORD":   "superset",
	"SMTP_MAIL_FROM":  "superset@superset.com",
	"SMTP_STARTTLS":   true,
	"SMTP_SSL":        false,
	"SLACK_API_TOKEN": "",

	"WEBDRIVER_BASEURL":               "http://0.0.0.0:8080/",
	"WEBDRIVER_BASEURL_USER_FRIENDLY": "http://0.0.0.0:8080/",
	"SCREENSHOT_LOCATE_WAIT":          10,
	"SCREENSHOT_LOAD_WAIT":            60,

	"UPLOAD_FOLDER":              "static/uploads/",
	"UPLOAD_MAX_FILE_SIZE_BYTES": 104857600,
	"CSV_EXTENSIONS":             []string{"csv", "tsv", "txt"},
	"EXCEL_EXTENSIONS":           []string{"xlsx", "xls"},
	"ALLOWED_EXTENSIONS":         []string{"xlsx", "xls", "csv", "tsv", "txt", "parquet", "zip"},
	"ZIPPED_FILE_MAX_SIZE":       104857600,

	"ENABLE_VERSIONING_CAPTURE":      true,
	"VERSION_HISTORY_RETENTION_DAYS": 30,
	"SOFT_DELETE_RETENTION_DAYS":     30,

	"SSH_TUNNEL_MANAGER_CLASS":      "superset.extensions.ssh.SSHManager",
	"SSH_TUNNEL_LOCAL_BIND_ADDRESS": "127.0.0.1",
	"SSH_TUNNEL_TIMEOUT_SEC":        10.0,

	"WEBSOCKET_ENABLE":                 false,
	"WEBSOCKET_URL":                    "ws://127.0.0.1:8080/",
	"WEBSOCKET_JWT_SECRET":             "test-ws-secret-change-me",
	"WEBSOCKET_JWT_EXPIRATION_SECONDS": 900,

	"BABEL_DEFAULT_LOCALE":        "en",
	"CURRENCIES":                  []string{"USD", "EUR", "GBP", "INR", "MXN", "JPY", "CNY"},
	"DEFAULT_TIME_FILTER":         "No filter",
	"DEFAULT_RELATIVE_START_TIME": "today",
	"DEFAULT_RELATIVE_END_TIME":   "today",
	"CSV_EXPORT_ENCODING":         "utf-8-sig",

	"LOG_FORMAT":         "%(asctime)s:%(levelname)s:%(name)s:%(message)s",
	"LOG_LEVEL":          "INFO",
	"ENABLE_TIME_ROTATE": false,
	"BACKUP_COUNT":       30,

	"THEME_DEFAULT_MODE":                     "system",
	"DASHBOARD_AUTO_REFRESH_MODE":            "force",
	"SUPERSET_DASHBOARD_POSITION_DATA_LIMIT": 65535,

	"VIZ_TYPE_DENYLIST":    []string{},
	"TIME_GRAIN_DENYLIST":  []string{},
	"DEFAULT_MAP_RENDERER": "maplibre",
	"SCARF_ANALYTICS":      true,

	// AI Agent Plane
	"AI_ASSISTANT_ENABLED":      false,
	"AI_LLM_PROVIDER":           "ollama",
	"AI_LLM_BASE_URL":           "http://127.0.0.1:11434/v1",
	"AI_LLM_API_KEY":            "",
	"AI_LLM_MODEL":              "qwen2.5:7b",
	"AI_LLM_TIMEOUT_SEC":        60,
	"AI_EMBEDDING_MODEL":        "bge-m3",
	"AI_TEMPERATURE":            0,
	"AI_MAX_TOKENS":             0,
	"AI_SEED":                   7,
	"AI_AGENT_MAX_STEPS":        6,
	"AI_AGENT_MAX_TOOL_CALLS":   8,
	"AI_AGENT_RUN_TIMEOUT_SEC":  120,
	"AI_AGENT_TOKEN_BUDGET":     120000,
	"AI_WRITE_TOOLS_ENABLED":    true,
	"AI_WRITE_CONFIRM_REQUIRED": false,
	"AI_SNAPSHOT_ENABLED":       true,
	"AI_MAX_TOKENS_PER_DAY":     200000,
	"AI_QUERY_TIMEOUT_SEC":      30,
	"AI_QUERY_MAX_ROWS":         5000,
	"AI_EXPLAIN_MAX_ROWS":       20000000,
	"AI_SAMPLES_ROW_LIMIT":      200,
	"AI_CACHE_TTL_SEC":          3600,
	"AI_REPLAY_TTL_SEC":         300,
	"AI_INSIGHT_MAX_LEN":        800,
	// AI_SCHEMA_COMPACT_THRESHOLD is the compact-schema size (in characters)
	// above which the Agent stops receiving every column and switches to
	// relevance-ranked context. Small datasets stay whole: retrieval has a cost
	// and no benefit when everything fits.
	"AI_SCHEMA_COMPACT_THRESHOLD": 8000,
	// AI_SQL_MEMORY_TOP_K is how many confirmed NL→SQL examples are injected
	// into the planning prompt.
	"AI_SQL_MEMORY_TOP_K": 3,

	"FEATURE_FLAGS": map[string]bool{
		"ALERT_REPORTS":               false,
		"ALERT_REPORT_SLACK_V2":       true,
		"ALERT_REPORT_WEBHOOK":        false,
		"ALLOW_FULL_CSV_EXPORT":       false,
		"CACHE_QUERY_BY_USER":         false,
		"CSS_TEMPLATES":               true,
		"DASHBOARD_VIRTUALIZATION":    true,
		"DRILL_BY":                    true,
		"DRILL_TO_DETAIL":             true,
		"DYNAMIC_PLUGINS":             false,
		"EMBEDDABLE_CHARTS":           true,
		"EMBEDDED_SUPERSET":           false,
		"ENABLE_TEMPLATE_PROCESSING":  false,
		"ENABLE_VIEWERS":              false,
		"ESTIMATE_QUERY_COST":         false,
		"GLOBAL_ASYNC_QUERIES":        false,
		"GLOBAL_TASK_FRAMEWORK":       false,
		"RLS_IN_SQLLAB":               false,
		"SSH_TUNNELING":               false,
		"SQLLAB_BACKEND_PERSISTENCE":  true,
		"SQLLAB_FORCE_RUN_ASYNC":      false,
		"TAGGING_SYSTEM":              true,
		"THUMBNAILS":                  false,
		"VERSION_HISTORY":             true,
		"DASHBOARD_CROSS_FILTERS":     true,
		"DASHBOARD_NATIVE_FILTERS":    true,
		"DASHBOARD_RBAC":              true,
		"ENABLE_ADVANCED_DATA_TYPES":  false,
		"ENABLE_EXTENSIONS":           false,
		"SEMANTIC_LAYERS":             false,
		"LISTVIEWS_DEFAULT_CARD_VIEW": false,
		"SCHEDULED_QUERIES":           false,
		"AI_AGENT":                    false, // Agent master switch
		"AI_SKILL_DOMAIN":             false, // domain analysis skills (dupont/trend/...)
		"AI_TOOLS_WRITE":              true,  // write tools enabled
	},
}

// Load reads configuration from the environment and an optional YAML file
// (path from SUPERSET_CONFIG_PATH). Environment variables take precedence.
func Load() (*Config, error) {
	v := viper.New()
	for k, val := range defaults {
		v.SetDefault(k, val)
	}
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Optional YAML config file.
	if path := v.GetString("SUPERSET_CONFIG_PATH"); path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	if cfg.WebserverPort == 0 {
		cfg.WebserverPort = 8088
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "INFO"
	}
	return &cfg, nil
}

// ConfigFilePath returns the YAML config file path the server was started with
// (SUPERSET_CONFIG_PATH). It is empty when the server runs purely from
// environment variables, in which case runtime edits cannot be persisted.
func ConfigFilePath() string {
	return os.Getenv("SUPERSET_CONFIG_PATH")
}

// PersistDatabaseURI writes SQLALCHEMY_DATABASE_URI back to the YAML config
// file so the change survives a restart. Viper rewrites the whole file, so
// comments are dropped; callers should warn the operator. The new URI only
// takes effect after the server is restarted.
func PersistDatabaseURI(path, uri string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("SUPERSET_CONFIG_PATH is not set, cannot persist configuration")
	}
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	v.Set("SQLALCHEMY_DATABASE_URI", uri)
	if err := v.WriteConfig(); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// FeatureEnabled reports whether a feature flag is on.
func (c *Config) FeatureEnabled(name string) bool {
	if c.FeatureFlags == nil {
		return false
	}
	return c.FeatureFlags[name]
}
