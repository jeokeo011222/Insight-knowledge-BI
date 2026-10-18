package connectors

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresSpec mirrors superset/db_engine_specs/postgres.py.
type PostgresSpec struct{ BaseSpec }

// NewPostgresSpec builds the PostgreSQL engine spec.
func NewPostgresSpec() *PostgresSpec {
	return &PostgresSpec{BaseSpec{
		EngineValue:      "postgresql",
		EngineNameValue:  "PostgreSQL",
		DriverValue:      "psycopg2",
		PortValue:        5432,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       true,
		MaxNameLen:       63,
		DefaultSchemaVal: "public",
	}}
}

// TimeGrainExpr renders DATE_TRUNC with optional timezone conversion.
func (s *PostgresSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	unit, err := GrainToDateTrunc(grain)
	if err != nil {
		return "", err
	}
	if tz != "" && !strings.EqualFold(tz, "UTC") {
		return fmt.Sprintf("DATE_TRUNC('%s', %s AT TIME ZONE 'UTC' AT TIME ZONE '%s')", unit, col, tz), nil
	}
	return fmt.Sprintf("DATE_TRUNC('%s', %s)", unit, col), nil
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *PostgresSpec) EpochToDTTM(col string) string { return fmt.Sprintf("TO_TIMESTAMP(%s)", col) }

// ConvertToDateTimeExpr casts text to timestamp.
func (s *PostgresSpec) ConvertToDateTimeExpr(col string) string {
	return fmt.Sprintf("CAST(%s AS TIMESTAMP)", col)
}

// DSN converts a SQLAlchemy URI into a pgx DSN.
func (s *PostgresSpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", err
	}
	u.Scheme = "postgresql"
	if u.Query().Get("sslmode") == "" {
		q := u.Query()
		q.Set("sslmode", "disable")
		u.RawQuery = q.Encode()
	}
	return "pgx", u.String(), nil
}

// Open returns a pgx-backed pool.
func (s *PostgresSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("pgx", dsn) }

// SchemasQuery lists user schemas.
func (s *PostgresSpec) SchemasQuery() string {
	return `SELECT schema_name FROM information_schema.schemata
	        WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast')
	        ORDER BY schema_name`
}

// TablesQuery lists tables and views in a schema.
func (s *PostgresSpec) TablesQuery() string {
	return `SELECT table_name, table_type FROM information_schema.tables
	        WHERE table_schema = '%s' AND table_name NOT LIKE 'pg_%%'
	        ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *PostgresSpec) ColumnsQuery() string {
	return `SELECT column_name, data_type, is_nullable, column_default
	        FROM information_schema.columns
	        WHERE table_catalog = current_database() AND table_schema = '%s' AND table_name = '%s'
	        ORDER BY ordinal_position`
}

// MySQLSpec mirrors superset/db_engine_specs/mysql.py.
type MySQLSpec struct{ BaseSpec }

// NewMySQLSpec builds the MySQL engine spec.
func NewMySQLSpec() *MySQLSpec {
	return &MySQLSpec{BaseSpec{
		EngineValue:      "mysql",
		EngineNameValue:  "MySQL",
		DriverValue:      "mysqldb",
		PortValue:        3306,
		QuoteChar:        "`",
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       true,
		MaxNameLen:       64,
		DefaultSchemaVal: "",
	}}
}

// TimeGrainExpr renders the MySQL DATE_FORMAT based truncation.
func (s *MySQLSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return mysqlTimeGrain(col, grain, tz)
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *MySQLSpec) EpochToDTTM(col string) string {
	return fmt.Sprintf("FROM_UNIXTIME(%s)", col)
}

// ConvertToDateTimeExpr casts text to datetime.
func (s *MySQLSpec) ConvertToDateTimeExpr(col string) string {
	return fmt.Sprintf("STR_TO_DATE(%s, '%%Y-%%m-%%d %%H:%%i:%%s')", col)
}

// DSN converts a SQLAlchemy URI into a go-sql-driver DSN.
func (s *MySQLSpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", err
	}
	pass, _ := u.User.Password()
	port := u.Port()
	if port == "" {
		port = "3306"
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)%s?parseTime=true&loc=UTC&charset=utf8mb4",
		u.User.Username(), pass, u.Hostname(), port, u.Path)
	return "mysql", dsn, nil
}

// Open returns a MySQL pool.
func (s *MySQLSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("mysql", dsn) }

// SchemasQuery lists databases as schemas.
func (s *MySQLSpec) SchemasQuery() string {
	return `SELECT schema_name FROM information_schema.schemata
	        WHERE schema_name NOT IN ('information_schema','mysql','performance_schema','sys')
	        ORDER BY schema_name`
}

// TablesQuery lists tables in a schema.
func (s *MySQLSpec) TablesQuery() string {
	return `SELECT table_name, table_type FROM information_schema.tables
	        WHERE table_catalog = current_database() AND table_schema = '%s'
	        ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *MySQLSpec) ColumnsQuery() string {
	return `SELECT column_name, data_type, is_nullable, column_default
	        FROM information_schema.columns
	        WHERE table_catalog = current_database() AND table_schema = '%s' AND table_name = '%s'
	        ORDER BY ordinal_position`
}

// SQLiteSpec mirrors superset/db_engine_specs/sqlite.py.
type SQLiteSpec struct{ BaseSpec }

// NewSQLiteSpec builds the SQLite engine spec.
func NewSQLiteSpec() *SQLiteSpec {
	return &SQLiteSpec{BaseSpec{
		EngineValue:      "sqlite",
		EngineNameValue:  "SQLite",
		DriverValue:      "sqlite3",
		PortValue:        0,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       true,
		MaxNameLen:       63,
		DefaultSchemaVal: "main",
	}}
}

// TimeGrainExpr renders STRFTIME based truncation.
func (s *SQLiteSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return sqliteTimeGrain(col, grain, tz)
}

// EpochToDTTM converts epoch seconds to a datetime string.
func (s *SQLiteSpec) EpochToDTTM(col string) string {
	return fmt.Sprintf("DATETIME(%s, 'unixepoch')", col)
}

// ConvertToDateTimeExpr casts text to datetime.
func (s *SQLiteSpec) ConvertToDateTimeExpr(col string) string {
	return fmt.Sprintf("DATETIME(%s)", col)
}

// DSN resolves a sqlite URI to a filesystem path.
func (s *SQLiteSpec) DSN(uri string) (string, string, error) {
	path := strings.TrimPrefix(uri, "sqlite:///")
	path = strings.TrimPrefix(path, "sqlite://")
	if path == "" || path == ":memory:" {
		return "sqlite", "file::memory:?cache=shared", nil
	}
	return "sqlite", path, nil
}

// Open returns a modernc sqlite pool.
func (s *SQLiteSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("sqlite", dsn) }

// SchemasQuery returns the single sqlite schema.
func (s *SQLiteSpec) SchemasQuery() string { return `SELECT 'main' AS schema_name` }

// TablesQuery lists tables in a schema.
func (s *SQLiteSpec) TablesQuery() string {
	return `SELECT name AS table_name, 'table' AS table_type FROM sqlite_master
	        WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%%' ORDER BY name`
}

// ColumnsQuery lists columns via PRAGMA.
func (s *SQLiteSpec) ColumnsQuery() string { return `PRAGMA table_info('%s.%s')` }

// DuckDBSpec mirrors superset/db_engine_specs/duckdb.py.
type DuckDBSpec struct{ BaseSpec }

// NewDuckDBSpec builds the DuckDB engine spec.
func NewDuckDBSpec() *DuckDBSpec {
	return &DuckDBSpec{BaseSpec{
		EngineValue:      "duckdb",
		EngineNameValue:  "DuckDB",
		DriverValue:      "duckdb_engine",
		PortValue:        0,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       true,
		MaxNameLen:       63,
		DefaultSchemaVal: "main",
	}}
}

// TimeGrainExpr renders DATE_TRUNC (DuckDB supports it natively).
func (s *DuckDBSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	unit, err := GrainToDateTrunc(grain)
	if err != nil {
		return "", err
	}
	if tz != "" && !strings.EqualFold(tz, "UTC") {
		return fmt.Sprintf("DATE_TRUNC('%s', %s AT TIME ZONE 'UTC' AT TIME ZONE '%s')", unit, col, tz), nil
	}
	return fmt.Sprintf("DATE_TRUNC('%s', %s)", unit, col), nil
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *DuckDBSpec) EpochToDTTM(col string) string { return fmt.Sprintf("TO_TIMESTAMP(%s)", col) }

// DSN resolves a duckdb URI to the filesystem path the driver opens.
func (s *DuckDBSpec) DSN(uri string) (string, string, error) {
	path := stripFileScheme(uri)
	if path == "" || path == ":memory:" {
		return "duckdb", "", nil
	}
	// Open read-only: this instance is a query engine, the file belongs to the
	// application that writes it. Read-only also lets several of our pools hold
	// the same file at once, which read-write would refuse with a lock error.
	if strings.Contains(path, "?") {
		return "duckdb", path + "&access_mode=read_only", nil
	}
	return "duckdb", path + "?access_mode=read_only", nil
}

// Open returns a DuckDB pool. The file must already exist: DuckDB would
// otherwise create an empty database on a typo, and the connection would look
// healthy while pointing at no data at all.
func (s *DuckDBSpec) Open(dsn string) (*sql.DB, error) {
	// The DSN carries driver options after "?", so the existence check has to
	// run against the path alone.
	path := dsn
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if path != "" {
		if info, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrDBFileNotFound, path)
		} else if info.IsDir() {
			return nil, fmt.Errorf("%w: %s is a directory", ErrDBFileNotFound, path)
		}
	}
	return sql.Open("duckdb", dsn)
}

// SchemasQuery lists the schemas of the connected database. DuckDB also
// carries a "main" schema in its system and temp catalogs; without the catalog
// filter the same name came back three times.
func (s *DuckDBSpec) SchemasQuery() string {
	return `SELECT schema_name FROM information_schema.schemata
	        WHERE catalog_name = current_database()
	          AND schema_name NOT IN ('information_schema', 'pg_catalog')
	        ORDER BY schema_name`
}

// TablesQuery lists tables and views in a schema.
func (s *DuckDBSpec) TablesQuery() string {
	return `SELECT table_name, table_type FROM information_schema.tables
	        WHERE table_catalog = current_database() AND table_schema = '%s'
	        ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *DuckDBSpec) ColumnsQuery() string {
	return `SELECT column_name, data_type, is_nullable, column_default
	        FROM information_schema.columns
	        WHERE table_catalog = current_database() AND table_schema = '%s' AND table_name = '%s'
	        ORDER BY ordinal_position`
}

// ClickHouseSpec mirrors superset/db_engine_specs/clickhouse.py.
type ClickHouseSpec struct{ BaseSpec }

// NewClickHouseSpec builds the ClickHouse engine spec.
func NewClickHouseSpec() *ClickHouseSpec {
	return &ClickHouseSpec{BaseSpec{
		EngineValue:      "clickhouse",
		EngineNameValue:  "ClickHouse (sqlalchemy)",
		DriverValue:      "connect",
		PortValue:        8123,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       false,
		MaxNameLen:       255,
		DefaultSchemaVal: "default",
	}}
}

// TimeGrainExpr renders toStartOf* functions.
func (s *ClickHouseSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", err
	}
	if tz != "" && !strings.EqualFold(tz, "UTC") {
		col = fmt.Sprintf("toTimeZone(%s, '%s')", col, tz)
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("toStartOfSecond(%s)", col), nil
	case "PT1M":
		return fmt.Sprintf("toStartOfMinute(%s)", col), nil
	case "PT5M":
		return fmt.Sprintf("toStartOfFiveMinute(%s)", col), nil
	case "PT10M":
		return fmt.Sprintf("toStartOfTenMinutes(%s)", col), nil
	case "PT15M":
		return fmt.Sprintf("toStartOfFifteenMinutes(%s)", col), nil
	case "PT30M":
		return fmt.Sprintf("toStartOfInterval(%s, INTERVAL 30 minute)", col), nil
	case "PT1H":
		return fmt.Sprintf("toStartOfHour(%s)", col), nil
	case "PT6H":
		return fmt.Sprintf("toStartOfInterval(%s, INTERVAL 6 hour)", col), nil
	case "P1D":
		return fmt.Sprintf("toStartOfDay(%s)", col), nil
	case "P1W":
		return fmt.Sprintf("toStartOfWeek(%s)", col), nil
	case "P1M":
		return fmt.Sprintf("toStartOfMonth(%s)", col), nil
	case "P3M":
		return fmt.Sprintf("toStartOfQuarter(%s)", col), nil
	case "P1Y":
		return fmt.Sprintf("toStartOfYear(%s)", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for clickhouse", grain)
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *ClickHouseSpec) EpochToDTTM(col string) string { return fmt.Sprintf("toDateTime(%s)", col) }

// DSN converts a SQLAlchemy URI into a clickhouse-go DSN.
func (s *ClickHouseSpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", err
	}
	port := u.Port()
	if port == "" {
		port = "8123"
	}
	pass, _ := u.User.Password()
	dsn := fmt.Sprintf("clickhouse://%s:%s@%s:%s%s",
		u.User.Username(), pass, u.Hostname(), port, u.Path)
	return "clickhouse", dsn, nil
}

// Open returns a ClickHouse pool.
func (s *ClickHouseSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("clickhouse", dsn) }

// SchemasQuery lists databases.
func (s *ClickHouseSpec) SchemasQuery() string {
	return `SELECT name AS schema_name FROM system.databases
	        WHERE name NOT IN ('system','INFORMATION_SCHEMA','information_schema') ORDER BY 1`
}

// TablesQuery lists tables.
func (s *ClickHouseSpec) TablesQuery() string {
	return `SELECT name AS table_name, engine AS table_type FROM system.tables WHERE database = '%s' ORDER BY 1`
}

// ColumnsQuery lists columns.
func (s *ClickHouseSpec) ColumnsQuery() string {
	return `SELECT name AS column_name, type AS data_type, '' AS is_nullable
	        FROM system.columns WHERE database = '%s' AND table = '%s' ORDER BY position`
}

// GenericSpec is the fallback for engines without a dedicated spec.
type GenericSpecImpl struct{ BaseSpec }

// GenericSpec returns the generic engine spec.
func GenericSpec() *GenericSpecImpl {
	return &GenericSpecImpl{BaseSpec{
		EngineNameValue:  "generic",
		DriverValue:      "",
		PortValue:        0,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    false,
		FileUpload:       false,
		MaxNameLen:       63,
		DefaultSchemaVal: "",
	}}
}

// DSN is not available for the generic spec.
func (s *GenericSpecImpl) DSN(uri string) (string, string, error) {
	return "", "", fmt.Errorf("engine is not supported by this build")
}

// Open is unavailable for the generic spec.
func (s *GenericSpecImpl) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("engine is not supported by this build")
}

// SchemasQuery returns an empty result set.
func (s *GenericSpecImpl) SchemasQuery() string { return `SELECT '' AS schema_name WHERE 1 = 0` }

// TablesQuery returns an empty result set.
func (s *GenericSpecImpl) TablesQuery() string { return `SELECT '' AS table_name WHERE 1 = 0` }

// ColumnsQuery returns an empty result set.
func (s *GenericSpecImpl) ColumnsQuery() string { return `SELECT '' AS column_name WHERE 1 = 0` }

// tzOffsetModifier converts an IANA-ish timezone into a sqlite modifier.
func tzOffsetModifier(tz string) string {
	if strings.HasPrefix(tz, "+") || strings.HasPrefix(tz, "-") {
		return tz + " hours"
	}
	return "localtime"
}

func init() {
	Register(NewPostgresSpec())
	Register(NewMySQLSpec())
	Register(NewSQLiteSpec())
	Register(NewDuckDBSpec())
	Register(NewClickHouseSpec())
}
