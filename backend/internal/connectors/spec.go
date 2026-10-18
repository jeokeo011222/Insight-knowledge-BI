// Package connectors implements the database engine abstraction that mirrors
// superset/db_engine_specs: a registry of EngineSpec implementations plus the
// connection manager that materialises *sql.DB handles from SQLAlchemy URIs.
package connectors

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// EngineSpec describes the dialect-specific behaviour of a backend.
// The interface intentionally mirrors BaseEngineSpec: every method has a
// working default so a new engine only overrides what differs.
type EngineSpec interface {
	// Engine returns the SQLAlchemy engine name (e.g. "postgresql").
	Engine() string
	// EngineName is the human readable name shown in the UI.
	EngineName() string
	// Driver returns the Python DBAPI driver name (informational).
	Driver() string
	// DefaultPort returns the default TCP port.
	DefaultPort() int
	// SupportsFileUpload reports whether CSV upload is allowed.
	SupportsFileUpload() bool
	// QuoteIdentifier quotes a single identifier.
	QuoteIdentifier(name string) string
	// TimeGrainExpr renders the SQL expression truncating col to a grain.
	TimeGrainExpr(col, grain, tz string) (string, error)
	// TimeGrainFromDS renders a numeric time-grain expression for the column
	// (used when a temporal column is stored as epoch seconds).
	EpochToDTTM(col string) string
	// ConvertToDateTimeExpr converts a text/numeric column into a timestamp.
	ConvertToDateTimeExpr(col string) string
	// LimitClause renders "LIMIT n" for a SELECT.
	LimitClause(n int) string
	// SelectStar returns "SELECT * FROM <table>" with the right quoting.
	SelectStar(table string) string
	// ColumnTypeMap maps a generic type to a dialect type.
	ColumnTypeMap(generic string) string
	// WhereOperators returns the supported filter operators.
	WhereOperators() map[string]string
	// SupportsJoins reports whether the engine supports joins.
	SupportsJoins() bool
	// SupportsSubqueries reports whether derived tables are supported.
	SupportsSubqueries() bool
	// AllowsAliasInSelect reports whether SELECT aliases may be reused in
	// GROUP BY / ORDER BY.
	AllowsAliasInSelect() bool
	// CTASPrefix builds the CREATE TABLE AS statement prefix.
	CTASPrefix(schema, table string) string
	// CVASPrefix builds the CREATE VIEW AS statement prefix.
	CVASPrefix(schema, view string) string
	// DMLStatement reports whether a SQL statement mutates data.
	DMLStatement(sql string) bool
	// IsReadOnlyQuery reports whether the statement is safe to run in SQL Lab.
	IsReadOnlyQuery(sql string) bool
	// DefaultSchema returns the schema used when none is given.
	DefaultSchema(dbName string) string
	// MaxColumnNameLength returns the identifier length limit.
	MaxColumnNameLength() int
	// Open builds a *sql.DB from a DSN.
	Open(dsn string) (*sql.DB, error)
	// DSN converts a SQLAlchemy URI into a Go driver DSN.
	DSN(uri string) (driver, dsn string, err error)
	// SchemasQuery returns the SQL listing schemas.
	SchemasQuery() string
	// TablesQuery returns the SQL listing tables for a schema.
	TablesQuery() string
	// ColumnsQuery returns the SQL listing columns for a table.
	ColumnsQuery() string
}

// BaseSpec provides the default implementations shared by all engines.
type BaseSpec struct {
	// EngineValue is the registry identifier (e.g. "postgresql"). When empty
	// it falls back to the lower-cased display name for backward
	// compatibility with the original five specs.
	EngineValue      string
	EngineNameValue  string
	DriverValue      string
	PortValue        int
	QuoteChar        string
	QuoteStart       string
	QuoteEnd         string
	HasJoins         bool
	HasSubqueries    bool
	AliasInSelect    bool
	FileUpload       bool
	MaxNameLen       int
	DefaultSchemaVal string
	Info             map[string]any
}

// Engine returns the lower-case engine identifier.
func (b *BaseSpec) Engine() string {
	if b.EngineValue != "" {
		return strings.ToLower(b.EngineValue)
	}
	return strings.ToLower(b.EngineNameValue)
}

// EngineName returns the display name.
func (b *BaseSpec) EngineName() string { return b.EngineNameValue }

// Driver returns the default driver name.
func (b *BaseSpec) Driver() string { return b.DriverValue }

// DefaultPort returns the default port.
func (b *BaseSpec) DefaultPort() int { return b.PortValue }

// SupportsFileUpload reports CSV upload support.
func (b *BaseSpec) SupportsFileUpload() bool { return b.FileUpload }

// QuoteIdentifier quotes an identifier with the engine quote characters.
// Engines with asymmetric quoting (Microsoft SQL Server) set QuoteStart and
// QuoteEnd explicitly; the closing delimiter is escaped by doubling.
func (b *BaseSpec) QuoteIdentifier(name string) string {
	start, end := b.QuoteStart, b.QuoteEnd
	if start == "" {
		start = b.QuoteChar
	}
	if start == "" {
		start = `"`
	}
	if end == "" {
		end = start
	}
	parts := strings.Split(name, ".")
	for i, p := range parts {
		if p == "*" {
			continue
		}
		if strings.HasPrefix(p, start) && strings.HasSuffix(p, end) {
			continue
		}
		parts[i] = start + strings.ReplaceAll(p, end, end+end) + end
	}
	return strings.Join(parts, ".")
}

// TimeGrainExpr default implementation uses DATE_TRUNC.
func (b *BaseSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	unit, err := GrainToDateTrunc(grain)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DATE_TRUNC('%s', %s)", unit, col), nil
}

// EpochToDTTM default implementation.
func (b *BaseSpec) EpochToDTTM(col string) string {
	return fmt.Sprintf("TO_TIMESTAMP(%s)", col)
}

// ConvertToDateTimeExpr default implementation.
func (b *BaseSpec) ConvertToDateTimeExpr(col string) string {
	return fmt.Sprintf("CAST(%s AS TIMESTAMP)", col)
}

// LimitClause renders a LIMIT clause.
func (b *BaseSpec) LimitClause(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("LIMIT %d", n)
}

// SelectStar renders a SELECT * statement.
func (b *BaseSpec) SelectStar(table string) string {
	return "SELECT * FROM " + b.QuoteIdentifier(table)
}

// ColumnTypeMap maps generic types; engines override for specifics.
func (b *BaseSpec) ColumnTypeMap(generic string) string {
	switch strings.ToUpper(generic) {
	case "STRING", "VARCHAR", "TEXT":
		return "VARCHAR"
	case "INT", "INTEGER", "BIGINT":
		return "INTEGER"
	case "FLOAT", "DOUBLE", "NUMERIC", "DECIMAL":
		return "FLOAT"
	case "BOOLEAN", "BOOL":
		return "BOOLEAN"
	case "DATE":
		return "DATE"
	case "DATETIME", "TIMESTAMP":
		return "TIMESTAMP"
	}
	return generic
}

// WhereOperators returns the operator map for filters.
func (b *BaseSpec) WhereOperators() map[string]string {
	return map[string]string{
		"==": "=", "!=": "!=", ">": ">", ">=": ">=", "<": "<", "<=": "<=",
		"IN": "IN", "NOT IN": "NOT IN", "LIKE": "LIKE", "ILIKE": "ILIKE",
		"IS NULL": "IS NULL", "IS NOT NULL": "IS NOT NULL",
		"REGEX": "~", "TEMPORAL_RANGE": "=",
	}
}

// SupportsJoins reports join support.
func (b *BaseSpec) SupportsJoins() bool { return b.HasJoins }

// SupportsSubqueries reports derived-table support.
func (b *BaseSpec) SupportsSubqueries() bool { return b.HasSubqueries }

// AllowsAliasInSelect reports alias reuse in GROUP BY / ORDER BY.
func (b *BaseSpec) AllowsAliasInSelect() bool { return b.AliasInSelect }

// CTASPrefix builds a CREATE TABLE AS prefix.
func (b *BaseSpec) CTASPrefix(schema, table string) string {
	if schema != "" {
		return "CREATE TABLE " + b.QuoteIdentifier(schema) + "." + b.QuoteIdentifier(table) + " AS "
	}
	return "CREATE TABLE " + b.QuoteIdentifier(table) + " AS "
}

// CVASPrefix builds a CREATE VIEW AS prefix.
func (b *BaseSpec) CVASPrefix(schema, view string) string {
	if schema != "" {
		return "CREATE VIEW " + b.QuoteIdentifier(schema) + "." + b.QuoteIdentifier(view) + " AS "
	}
	return "CREATE VIEW " + b.QuoteIdentifier(view) + " AS "
}

// DMLStatement reports whether the statement mutates data.
func (b *BaseSpec) DMLStatement(stmt string) bool {
	head := strings.ToUpper(strings.TrimSpace(stmt))
	for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "DROP", "ALTER", "TRUNCATE", "CREATE", "GRANT", "REVOKE"} {
		if strings.HasPrefix(head, kw) {
			return true
		}
	}
	return false
}

// IsReadOnlyQuery reports whether the statement only reads data.
func (b *BaseSpec) IsReadOnlyQuery(stmt string) bool {
	head := strings.ToUpper(strings.TrimSpace(stripSQLComments(stmt)))
	return strings.HasPrefix(head, "SELECT") || strings.HasPrefix(head, "WITH") || strings.HasPrefix(head, "SHOW") || strings.HasPrefix(head, "EXPLAIN")
}

// DefaultSchema returns the default schema.
func (b *BaseSpec) DefaultSchema(dbName string) string {
	if b.DefaultSchemaVal != "" {
		return b.DefaultSchemaVal
	}
	return dbName
}

// MaxColumnNameLength returns the identifier limit.
func (b *BaseSpec) MaxColumnNameLength() int {
	if b.MaxNameLen > 0 {
		return b.MaxNameLen
	}
	return 63
}

// EngineInformation exposes the feature flags advertised by /database/available.
func (b *BaseSpec) EngineInformation() map[string]any {
	if b.Info != nil {
		return b.Info
	}
	return map[string]any{"supports_dynamic_catalog": false}
}

// EngineInformationProvider is implemented by every spec through BaseSpec; the
// optional interface keeps third-party specs that only satisfy EngineSpec
// working.
type EngineInformationProvider interface {
	EngineInformation() map[string]any
}

// Information returns the feature flags for a spec, with a safe default.
func Information(spec EngineSpec) map[string]any {
	if p, ok := spec.(EngineInformationProvider); ok {
		if info := p.EngineInformation(); info != nil {
			return info
		}
	}
	return map[string]any{"supports_dynamic_catalog": false}
}

func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// registry holds every registered engine spec.
var (
	registryMu sync.RWMutex
	registry   = map[string]EngineSpec{}
)

// Register adds an engine spec to the registry. It panics on duplicates so
// that programming errors surface at init time.
func Register(spec EngineSpec) {
	registryMu.Lock()
	defer registryMu.Unlock()
	key := strings.ToLower(spec.Engine())
	if _, exists := registry[key]; exists {
		panic("connectors: duplicate engine spec " + key)
	}
	registry[key] = spec
}

// engineAliases maps SQLAlchemy scheme aliases onto the canonical engine name.
var engineAliases = map[string]string{
	"postgres":     "postgresql",
	"postgresql":   "postgresql",
	"mysql":        "mysql",
	"sqlite":       "sqlite",
	"duckdb":       "duckdb",
	"clickhouse":   "clickhouse",
	"clickhousedb": "clickhouse",
	"tdengine":     "taosws",
	"taosws":       "taosws",
	"exasol":       "exa",
	"exa":          "exa",
	"ydb":          "yql",
	"yql":          "yql",
	"kusto":        "kustosql",
	"kustosql":     "kustosql",
	"awsathena":    "awsathena",
	"athena":       "awsathena",
}

// NormalizeEngine resolves an alias to the canonical engine name.
func NormalizeEngine(engine string) string {
	e := strings.ToLower(strings.TrimSpace(engine))
	if i := strings.Index(e, "+"); i > 0 {
		e = e[:i]
	}
	if canonical, ok := engineAliases[e]; ok {
		return canonical
	}
	return e
}

// Get returns the spec for an engine name, or nil.
func Get(engine string) EngineSpec {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[NormalizeEngine(engine)]
}

// GetOrDefault returns the spec or the generic fallback.
func GetOrDefault(engine string) EngineSpec {
	if s := Get(engine); s != nil {
		return s
	}
	// Some callers hold a full SQLAlchemy URI rather than an engine name
	// (dataset sampling did). Resolve the scheme instead of silently falling
	// back to the generic spec: the generic one quotes identifiers with `"`,
	// which is a syntax error on MySQL and SQL Server — their data screens
	// would break with an unrelated "You have an error in your SQL syntax".
	if strings.Contains(engine, "://") {
		if parsed, err := ParseSQLAlchemyURI(engine); err == nil {
			if s := Get(parsed.Engine); s != nil {
				return s
			}
		}
	}
	return GenericSpec()
}

// EngineNames lists every registered engine name, sorted.
func EngineNames() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Specs returns all registered specs sorted by engine name.
func Specs() []EngineSpec {
	names := EngineNames()
	out := make([]EngineSpec, 0, len(names))
	for _, n := range names {
		out = append(out, registry[n])
	}
	return out
}

// ParseURI splits a SQLAlchemy URI into its components.
// Example: postgresql+psycopg2://user:pass@host:5432/dbname?sslmode=disable
type URI struct {
	Engine   string
	Driver   string
	Username string
	Password string
	Host     string
	Port     int
	Database string
	Query    url.Values
}

// ParseSQLAlchemyURI parses a SQLAlchemy database URI.
func ParseSQLAlchemyURI(raw string) (*URI, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid database URI: %w", err)
	}
	if u.Scheme == "" {
		return nil, fmt.Errorf("database URI must include a scheme, e.g. postgresql://user:pass@host/db")
	}
	scheme := u.Scheme
	engine, driver := scheme, ""
	if i := strings.Index(scheme, "+"); i > 0 {
		engine, driver = scheme[:i], scheme[i+1:]
	}
	out := &URI{Engine: NormalizeEngine(engine), Driver: driver, Query: u.Query()}
	if u.User != nil {
		out.Username = u.User.Username()
		out.Password, _ = u.User.Password()
	}
	out.Host = u.Hostname()
	if p := u.Port(); p != "" {
		fmt.Sscanf(p, "%d", &out.Port)
	}
	out.Database = strings.TrimPrefix(u.Path, "/")
	if spec := Get(engine); spec != nil && out.Port == 0 {
		out.Port = spec.DefaultPort()
	}
	return out, nil
}

// Open builds a connection pool for a SQLAlchemy URI.
func Open(uri string) (*sql.DB, EngineSpec, error) {
	parsed, err := ParseSQLAlchemyURI(uri)
	if err != nil {
		return nil, nil, err
	}
	spec := Get(parsed.Engine)
	if spec == nil {
		return nil, nil, fmt.Errorf("unsupported database engine %q", parsed.Engine)
	}
	driver, dsn, err := spec.DSN(uri)
	if err != nil {
		return nil, nil, err
	}
	db, err := spec.Open(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", driver, err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	return db, spec, nil
}

// Ping verifies that a URI is reachable.
func Ping(ctx context.Context, uri string) error {
	db, _, err := Open(uri)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}
