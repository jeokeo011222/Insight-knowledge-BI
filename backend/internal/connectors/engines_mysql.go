package connectors

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// MySQLFamilySpec implements engines that speak the MySQL client/server
// protocol. go-sql-driver/mysql drives all of them.
type MySQLFamilySpec struct {
	BaseSpec
	// tlsByDefault forces TLS in the DSN (Databend Cloud listens on 443).
	tlsByDefault bool
	grain        timeGrainFunc
	schemasQuery string
	tablesQuery  string
	columnsQuery string
}

// TimeGrainExpr renders the MySQL DATE_FORMAT expressions.
func (s *MySQLFamilySpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	if s.grain != nil {
		return s.grain(col, grain, tz)
	}
	return mysqlTimeGrain(col, grain, tz)
}

// EpochToDTTM converts epoch seconds to a datetime.
func (s *MySQLFamilySpec) EpochToDTTM(col string) string {
	return fmt.Sprintf("FROM_UNIXTIME(%s)", col)
}

// ConvertToDateTimeExpr parses a string into a datetime.
func (s *MySQLFamilySpec) ConvertToDateTimeExpr(col string) string {
	return fmt.Sprintf("STR_TO_DATE(%s, '%%Y-%%m-%%d %%H:%%i:%%s')", col)
}

// DSN converts a SQLAlchemy URI into a go-sql-driver DSN.
func (s *MySQLFamilySpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("invalid %s URI: %w", s.Engine(), err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("%s URI must include a host", s.Engine())
	}
	port := u.Port()
	if port == "" {
		port = fmt.Sprintf("%d", s.DefaultPort())
	}
	cfg := mysql.NewConfig()
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
	}
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(u.Hostname(), port)
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Timeout = 15 * time.Second
	cfg.ReadTimeout = 0
	cfg.Params = map[string]string{"charset": "utf8mb4"}

	q := u.Query()
	if s.tlsByDefault || isTruthy(q.Get("ssl")) || isTruthy(q.Get("secure")) || isTruthy(q.Get("tls")) {
		cfg.TLSConfig = "true"
	}
	if charset := q.Get("charset"); charset != "" {
		cfg.Params["charset"] = charset
	}
	return "mysql", cfg.FormatDSN(), nil
}

// Open returns a MySQL pool.
func (s *MySQLFamilySpec) Open(dsn string) (*sql.DB, error) { return sql.Open("mysql", dsn) }

// SchemasQuery lists databases, which MySQL exposes as schemas.
func (s *MySQLFamilySpec) SchemasQuery() string {
	if s.schemasQuery != "" {
		return s.schemasQuery
	}
	return `SELECT schema_name FROM information_schema.schemata
	        WHERE schema_name NOT IN ('information_schema','mysql','performance_schema','sys')
	        ORDER BY schema_name`
}

// TablesQuery lists tables in a schema.
func (s *MySQLFamilySpec) TablesQuery() string {
	if s.tablesQuery != "" {
		return s.tablesQuery
	}
	return `SELECT table_name, table_type FROM information_schema.tables
	        WHERE table_schema = '%s' ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *MySQLFamilySpec) ColumnsQuery() string {
	if s.columnsQuery != "" {
		return s.columnsQuery
	}
	return `SELECT column_name, data_type, is_nullable, column_default
	        FROM information_schema.columns
	        WHERE table_schema = '%s' AND table_name = '%s'
	        ORDER BY ordinal_position`
}

// isTruthy interprets the common boolean spellings used in SQLAlchemy URIs.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "require":
		return true
	}
	return false
}

func newMySQLFamily(engine, displayName, driver string, port int, maxName int, upload bool) *MySQLFamilySpec {
	return &MySQLFamilySpec{
		BaseSpec: BaseSpec{
			EngineValue:      engine,
			EngineNameValue:  displayName,
			DriverValue:      driver,
			PortValue:        port,
			QuoteChar:        "`",
			HasJoins:         true,
			HasSubqueries:    true,
			AliasInSelect:    true,
			FileUpload:       upload,
			MaxNameLen:       maxName,
			DefaultSchemaVal: "",
			Info:             map[string]any{"supports_dynamic_catalog": false},
		},
	}
}

func init() {
	Register(newMySQLFamily("mariadb", "MariaDB", "mysqldb", 3306, 64, true))

	starrocks := newMySQLFamily("starrocks", "StarRocks", "starrocks", 9030, 255, true)
	Register(starrocks)

	doris := newMySQLFamily("pydoris", "Apache Doris", "mysqldb", 9030, 255, true)
	Register(doris)

	singlestore := newMySQLFamily("singlestoredb", "SingleStore", "singlestoredb", 3306, 255, true)
	Register(singlestore)

	oceanbase := newMySQLFamily("oceanbase", "OceanBase", "oceanbase", 2881, 255, false)
	Register(oceanbase)

	// Databend Cloud speaks the MySQL protocol over TLS on port 443 and uses
	// DATE_TRUNC rather than DATE_FORMAT.
	databend := newMySQLFamily("databend", "Databend", "databend", 443, 255, false)
	databend.tlsByDefault = true
	databend.grain = func(col, grain, tz string) (string, error) {
		return dateTruncTimeGrain(col, grain, tz, "databend")
	}
	Register(databend)
}
