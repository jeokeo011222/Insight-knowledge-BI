package connectors

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ConnectionKind groups engines by the shape of their connection form. The
// frontend renders a different set of inputs per kind, and BuildURI knows how
// to assemble each kind into a SQLAlchemy URI.
type ConnectionKind string

const (
	// KindHost is the common relational shape: host + port + database.
	KindHost ConnectionKind = "host"
	// KindCatalog is the Presto/Trino shape: host + port + catalog + schema,
	// where catalog and schema are path segments rather than query options.
	KindCatalog ConnectionKind = "catalog"
	// KindFile is the embedded shape: a single filesystem path (SQLite, DuckDB).
	KindFile ConnectionKind = "file"
	// KindURI is the escape hatch for engines whose DSN carries protocol
	// specific parts (accounts, regions, warehouses) that cannot be derived
	// from host/port alone.
	KindURI ConnectionKind = "uri"
)

// Field widget types understood by the connection form.
const (
	TypeText     = "string"
	TypePassword = "password"
	TypeInt      = "int"
	TypeSelect   = "select"
)

// Field is one input of the "create database" connection form. LabelKey is the
// i18n key the frontend prefers; Label is the English fallback used when the
// key is missing.
type Field struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	LabelKey    string `json:"label_key"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
	HelpKey     string `json:"help_key,omitempty"`
	Group       string `json:"group"`
}

// ConnectionForm is everything the UI needs to render the connection step for
// one engine.
type ConnectionForm struct {
	Kind    ConnectionKind `json:"kind"`
	Driver  string         `json:"driver"`
	Example string         `json:"example,omitempty"`
	Fields  []Field        `json:"fields"`
}

// uriOnlyEngines need a hand-written SQLAlchemy URI because their DSN embeds
// credentials, accounts or transport details that host/port cannot express.
var uriOnlyEngines = map[string]struct{}{
	"arc": {}, "ascend": {}, "awsathena": {}, "bigquery": {}, "couchbase": {},
	"d1": {}, "databricks": {}, "datafusion": {}, "datastore": {}, "dremio": {},
	"drill": {}, "druid": {}, "dynamodb": {}, "elasticsearch": {}, "firebolt": {},
	"gsheets": {}, "iotdb": {}, "kustosql": {}, "mongodb": {}, "odps": {},
	"ocient": {}, "phoenix": {}, "pinot": {}, "snowflake": {}, "solr": {}, "yql": {},
}

// catalogEngines put catalog and schema in the URI path.
var catalogEngines = map[string]struct{}{"presto": {}, "trino": {}}

// noSchemaEngines treat the database name as the schema, so a separate schema
// input would be redundant (and wrong) for them.
var noSchemaEngines = map[string]struct{}{
	"mysql": {}, "mariadb": {}, "starrocks": {}, "pydoris": {},
	"singlestoredb": {}, "oceanbase": {}, "databend": {},
}

// databasesNamedSpecially overrides the "Database" label where the engine uses
// a different concept for the same path segment.
var databasesNamedSpecially = map[string]struct {
	labelKey    string
	label       string
	placeholder string
	helpKey     string
	help        string
	defaultVal  string
}{
	"oracle": {
		labelKey: "database.field.service_name", label: "Service name",
		placeholder: "ORCLPDB1",
		helpKey:     "database.field.service_name_help",
		help:        "Oracle service name (or SID). It becomes the path of the URI.",
	},
	"presto": {
		labelKey: "database.field.catalog", label: "Catalog", placeholder: "hive",
	},
	"trino": {
		labelKey: "database.field.catalog", label: "Catalog", placeholder: "hive",
	},
	"kylin": {
		labelKey: "database.field.project", label: "Project", placeholder: "learn_kylin",
	},
	"clickhouse": {
		labelKey: "database.field.database", label: "Database",
		placeholder: "default", defaultVal: "default",
	},
}

// Kind reports which form shape an engine uses.
func Kind(engine string) ConnectionKind {
	e := NormalizeEngine(engine)
	if _, ok := uriOnlyEngines[e]; ok {
		return KindURI
	}
	if _, ok := catalogEngines[e]; ok {
		return KindCatalog
	}
	spec := Get(e)
	if spec == nil {
		return KindHost
	}
	if spec.DefaultPort() == 0 {
		return KindFile
	}
	return KindHost
}

// Form returns the connection form description for an engine.
func Form(engine string) ConnectionForm {
	e := NormalizeEngine(engine)
	spec := Get(e)
	kind := Kind(e)
	out := ConnectionForm{Kind: kind}
	if spec != nil {
		out.Driver = spec.Driver()
	}

	switch kind {
	case KindFile:
		example := "/data/example.db"
		if e == "duckdb" {
			// The common DuckDB warehouse is a file next to the app that wrote
			// it, so the Windows example is the useful one to show.
			example = "D:/data/example.duckdb"
		}
		out.Example = e + ":///data/example.db"
		out.Fields = []Field{{
			Name: "path", Label: "Database file", LabelKey: "database.field.file_path",
			Type: TypeText, Required: true, Group: "connection",
			Placeholder: example,
			Help:        "Absolute path to the database file, for example D:/data/stock.duckdb. A pasted duckdb:/// URI loses its prefix automatically.",
			HelpKey:     "database.field.file_path_help",
		}}
	case KindURI:
		out.Example = e + "://"
		out.Fields = []Field{{
			Name: "sqlalchemy_uri", Label: "SQLAlchemy URI", LabelKey: "database.field.uri",
			Type: TypeText, Required: true, Group: "connection",
			Help:    "This engine needs a complete SQLAlchemy URI because its connection string carries account or transport details.",
			HelpKey: "database.field.uri_only_help",
		}}
	case KindCatalog:
		port := 8080
		if spec != nil && spec.DefaultPort() > 0 {
			port = spec.DefaultPort()
		}
		out.Example = e + "://user:password@host:" + strconv.Itoa(port) + "/catalog/schema"
		out.Fields = []Field{
			hostField(),
			{Name: "port", Label: "Port", LabelKey: "database.field.port", Type: TypeInt, Group: "connection", Default: port},
			{Name: "database", Label: "Catalog", LabelKey: "database.field.catalog", Type: TypeText, Required: true, Group: "connection", Placeholder: "hive"},
			{Name: "schema", Label: "Schema", LabelKey: "database.field.schema", Type: TypeText, Required: true, Group: "connection", Placeholder: "default"},
			userField(),
			passwordField(),
		}
	default:
		port := 0
		if spec != nil {
			port = spec.DefaultPort()
		}
		out.Example = e + "://user:password@host:" + strconv.Itoa(port) + "/database"
		out.Fields = []Field{hostField()}
		portField := Field{
			Name: "port", Label: "Port", LabelKey: "database.field.port",
			Type: TypeInt, Group: "connection",
		}
		if port > 0 {
			portField.Default = port
		}
		out.Fields = append(out.Fields, portField)

		dbField := Field{
			Name: "database", Label: "Database", LabelKey: "database.field.database",
			Type: TypeText, Required: true, Group: "connection", Placeholder: "database",
		}
		if special, ok := databasesNamedSpecially[e]; ok {
			if special.labelKey != "" {
				dbField.LabelKey = special.labelKey
			}
			if special.label != "" {
				dbField.Label = special.label
			}
			if special.placeholder != "" {
				dbField.Placeholder = special.placeholder
			}
			if special.helpKey != "" {
				dbField.HelpKey = special.helpKey
			}
			if special.help != "" {
				dbField.Help = special.help
			}
			if special.defaultVal != "" {
				dbField.Default = special.defaultVal
			}
		}
		out.Fields = append(out.Fields, dbField, userField(), passwordField())

		if _, skip := noSchemaEngines[e]; !skip {
			schemaField := Field{
				Name: "schema", Label: "Schema", LabelKey: "database.field.schema",
				Type: TypeText, Group: "advanced",
				Help:    "Optional. The schema that owns the tables; datasets can override it.",
				HelpKey: "database.field.schema_help",
			}
			if spec != nil {
				if def := spec.DefaultSchema(""); def != "" {
					schemaField.Default = def
				}
			}
			out.Fields = append(out.Fields, schemaField)
		}
	}

	for i := range out.Fields {
		if out.Fields[i].Group == "" {
			out.Fields[i].Group = "connection"
		}
	}
	return out
}

func hostField() Field {
	return Field{
		Name: "host", Label: "Host", LabelKey: "database.field.host",
		Type: TypeText, Required: true, Group: "connection",
		Placeholder: "127.0.0.1",
	}
}

func userField() Field {
	return Field{
		Name: "username", Label: "Username", LabelKey: "database.field.username",
		Type: TypeText, Group: "auth",
	}
}

func passwordField() Field {
	return Field{
		Name: "password", Label: "Password", LabelKey: "database.field.password",
		Type: TypePassword, Group: "auth",
	}
}

// BuildOptions carries the structured connection form values.
type BuildOptions struct {
	Engine   string
	Host     string
	Port     int
	Database string
	Schema   string
	Username string
	Password string
	Query    string
	// Path is the filesystem path for file-backed engines.
	Path string
}

// ErrURIRequired is returned when an engine can only be configured through a
// complete SQLAlchemy URI.
var ErrURIRequired = errors.New("this engine requires a full SQLAlchemy URI")

// ErrPathRequired / ErrHostRequired / ErrEngineRequired let the API layer
// translate "what is missing" into the user's language. Reporting them as a
// generic "SQLAlchemy URI is required" hid which input the user still had to
// fill in.
var (
	ErrEngineRequired = errors.New("engine is required")
	ErrPathRequired   = errors.New("database file path is required")
	ErrHostRequired   = errors.New("host is required")
)

// BuildURI assembles a SQLAlchemy URI from structured form values. Credentials
// and database names are escaped, so passwords containing "@", "/" or ":" no
// longer corrupt the URI.
func BuildURI(o BuildOptions) (string, error) {
	engine := NormalizeEngine(o.Engine)
	if engine == "" {
		return "", ErrEngineRequired
	}
	spec := Get(engine)
	if spec == nil {
		return "", fmt.Errorf("unknown database engine %q", o.Engine)
	}
	kind := Kind(engine)

	if kind == KindURI {
		return "", ErrURIRequired
	}

	if kind == KindFile {
		path := strings.TrimSpace(o.Path)
		if path == "" {
			path = strings.TrimSpace(o.Database)
		}
		if path == "" {
			return "", ErrPathRequired
		}
		if path == ":memory:" {
			return engine + "://", nil
		}
		return engine + ":///" + normaliseFilePath(path), nil
	}

	host := strings.TrimSpace(o.Host)
	if host == "" {
		return "", ErrHostRequired
	}

	port := o.Port
	if port <= 0 {
		port = spec.DefaultPort()
	}
	// A host that already carries a port wins over the port input.
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") && port > 0 {
		if _, _, err := net.SplitHostPort(host); err == nil {
			port = 0
		}
	}

	u := &url.URL{Scheme: engine}
	if port > 0 {
		u.Host = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	} else {
		u.Host = host
	}

	if name := strings.TrimSpace(o.Username); name != "" {
		if pass := o.Password; pass != "" {
			u.User = url.UserPassword(name, pass)
		} else {
			u.User = url.User(name)
		}
	}

	database := strings.Trim(strings.TrimSpace(o.Database), "/")
	schema := strings.Trim(strings.TrimSpace(o.Schema), "/")
	switch kind {
	case KindCatalog:
		if database == "" {
			return "", fmt.Errorf("catalog is required")
		}
		u.Path = "/" + database
		if schema != "" {
			u.Path += "/" + schema
		}
		if u.RawQuery = strings.TrimPrefix(strings.TrimSpace(o.Query), "?"); u.RawQuery == "" {
			u.RawQuery = ""
		}
	default:
		if database != "" {
			u.Path = "/" + database
		}
		if q := strings.TrimPrefix(strings.TrimSpace(o.Query), "?"); q != "" {
			u.RawQuery = q
		}
	}
	return u.String(), nil
}

// normaliseFilePath converts a local filesystem path into the part that follows
// the authority separator of a SQLAlchemy file URI.
func normaliseFilePath(path string) string {
	return strings.ReplaceAll(stripFileScheme(path), "\\", "/")
}

// stripFileScheme turns whatever a user pasted into the "database file" input
// into a bare filesystem path. People paste a complete URI
// (`duckdb:///D:/data/stock.duckdb`) or a quoted path out of a config file,
// because every other Superset screen talks in SQLAlchemy URIs. Prefixing an
// already-prefixed value produced `duckdb:///duckdb:///D:/...`, and the driver
// then looked for a file literally named `duckdb:` — the UI showed a path that
// looked correct while the connection reported the file as missing.
func stripFileScheme(raw string) string {
	path := strings.TrimSpace(raw)
	if len(path) >= 2 {
		if quote := path[0]; (quote == '"' || quote == '\'') && path[len(path)-1] == quote {
			path = strings.TrimSpace(path[1 : len(path)-1])
		}
	}
	// Loop, because the pasted value can carry the scheme more than once
	// ("duckdb:///duckdb:///D:/x") and the nesting is not something a single
	// TrimPrefix can undo.
	for {
		// The slash left over from "scheme:///" hides a second scheme behind
		// it ("/duckdb:///..."), so drop it before looking for one.
		if strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") {
			rest := path[1:]
			if idx := strings.Index(rest, "://"); idx > 0 && isSchemeName(rest[:idx]) {
				path = rest
				continue
			}
		}
		idx := strings.Index(path, "://")
		if idx <= 0 || !isSchemeName(path[:idx]) {
			return path
		}
		path = path[idx+3:]
		switch {
		case strings.HasPrefix(path, "//"):
			// "duckdb:////data/app.db" is the absolute POSIX form: keep one slash.
			path = path[1:]
		case len(path) >= 3 && path[0] == '/' && isDriveLetter(path[1]) && path[2] == ':':
			// "duckdb:///D:/data/app.db": the leading slash is not part of the
			// drive path, otherwise os.Stat looks for "\D:".
			path = path[1:]
		}
	}
}

// isSchemeName reports whether value looks like a URI scheme (RFC 3986) rather
// than a piece of a filesystem path.
func isSchemeName(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

func isDriveLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// CleanFileURI rewrites a file-engine URI that carries a second URI inside its
// path. Only the nested form is touched, so a well-formed
// "sqlite:////data/app.db" keeps its slash count and relative paths
// ("sqlite:///data/app.db") keep their meaning.
func CleanFileURI(uri string) string {
	trimmed := strings.TrimSpace(uri)
	sep := strings.Index(trimmed, "://")
	if sep <= 0 {
		return uri
	}
	engine := NormalizeEngine(trimmed[:sep])
	if Kind(engine) != KindFile {
		return uri
	}
	if !strings.Contains(trimmed[sep+3:], "://") {
		return uri
	}
	path := stripFileScheme(trimmed)
	if path == "" || path == ":memory:" {
		return engine + "://"
	}
	return engine + ":///" + path
}

// ParseFileURI returns the filesystem path a file-engine URI points at.
func ParseFileURI(uri string) string {
	return stripFileScheme(uri)
}

// Engine categories group the "supported databases" picker the same way
// Superset's own connect flow does. They describe what the system is *for*, not
// how its connection string is shaped — Kind already covers the latter — so an
// engine can be both "analytics" and KindHost.
const (
	CategoryRelational = "relational"
	CategoryAnalytics  = "analytics"
	CategoryCloud      = "cloud"
	CategoryFile       = "file"
	CategoryNoSQL      = "nosql"
	CategoryOther      = "other"
)

// engineCategories maps each registered engine to its picker group. Engines
// missing from the map fall back to CategoryOther, so a newly registered
// engine still shows up instead of disappearing from the picker.
var engineCategories = map[string]string{
	// Transactional / relational engines.
	"postgresql": CategoryRelational, "mysql": CategoryRelational, "mariadb": CategoryRelational,
	"oracle": CategoryRelational, "mssql": CategoryRelational, "db2": CategoryRelational,
	"ibmi": CategoryRelational, "sybase": CategoryRelational, "firebird": CategoryRelational,
	"cockroachdb": CategoryRelational, "greenplum": CategoryRelational, "redshift": CategoryRelational,
	"timescaledb": CategoryRelational, "yugabytedb": CategoryRelational, "exa": CategoryRelational,
	"monetdb": CategoryRelational, "netezza": CategoryRelational, "teradatasql": CategoryRelational,
	"vertica": CategoryRelational, "hana": CategoryRelational, "hologres": CategoryRelational,
	"denodo": CategoryRelational, "crate": CategoryRelational, "risingwave": CategoryRelational,
	"singlestoredb": CategoryRelational, "oceanbase": CategoryRelational,
	"superset": CategoryRelational, "yql": CategoryRelational,

	// OLAP / warehouse / analytics engines.
	"clickhouse": CategoryAnalytics, "hive": CategoryAnalytics, "impala": CategoryAnalytics,
	"presto": CategoryAnalytics, "trino": CategoryAnalytics, "spark": CategoryAnalytics,
	"druid": CategoryAnalytics, "pinot": CategoryAnalytics, "kylin": CategoryAnalytics,
	"pydoris": CategoryAnalytics, "starrocks": CategoryAnalytics, "databend": CategoryAnalytics,
	"drill": CategoryAnalytics, "dremio": CategoryAnalytics, "kustosql": CategoryAnalytics,
	"firebolt": CategoryAnalytics, "ascend": CategoryAnalytics, "ocient": CategoryAnalytics,
	"datafusion": CategoryAnalytics, "odps": CategoryAnalytics, "arc": CategoryAnalytics,

	// Cloud managed services.
	"bigquery": CategoryCloud, "snowflake": CategoryCloud, "databricks": CategoryCloud,
	"awsathena": CategoryCloud, "gsheets": CategoryCloud, "datastore": CategoryCloud,
	"dynamodb": CategoryCloud, "d1": CategoryCloud,

	// Embedded / file backed.
	"sqlite": CategoryFile, "duckdb": CategoryFile, "shillelagh": CategoryFile,

	// Document stores, search engines and time-series.
	"mongodb": CategoryNoSQL, "elasticsearch": CategoryNoSQL, "solr": CategoryNoSQL,
	"couchbase": CategoryNoSQL, "phoenix": CategoryNoSQL, "iotdb": CategoryNoSQL,
	"taosws": CategoryNoSQL,
}

// Category returns the picker group for an engine.
func Category(engine string) string {
	if c, ok := engineCategories[NormalizeEngine(engine)]; ok {
		return c
	}
	return CategoryOther
}

// Categories lists the picker groups in display order.
func Categories() []string {
	return []string{
		CategoryRelational, CategoryAnalytics, CategoryCloud,
		CategoryFile, CategoryNoSQL, CategoryOther,
	}
}
