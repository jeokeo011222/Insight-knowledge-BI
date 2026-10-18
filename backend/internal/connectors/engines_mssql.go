package connectors

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// MSSQLSpec mirrors superset/db_engine_specs/mssql.py.
type MSSQLSpec struct{ BaseSpec }

// TimeGrainExpr renders the DATEADD/DATEDIFF expressions from the Python spec.
func (s *MSSQLSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return mssqlTimeGrain(col, grain, tz)
}

// EpochToDTTM converts epoch seconds to a datetime2 value.
func (s *MSSQLSpec) EpochToDTTM(col string) string {
	return fmt.Sprintf("DATEADD(SECOND, %s, '1970-01-01')", col)
}

// ConvertToDateTimeExpr casts text to datetime2.
func (s *MSSQLSpec) ConvertToDateTimeExpr(col string) string {
	return fmt.Sprintf("CAST(%s AS DATETIME2)", col)
}

// LimitClause renders the T-SQL OFFSET/FETCH form.
func (s *MSSQLSpec) LimitClause(n int) string { return offsetFetchLimit(n) }

// DSN converts a SQLAlchemy URI into a go-mssqldb URL.
func (s *MSSQLSpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("invalid mssql URI: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("mssql URI must include a host")
	}
	port := u.Port()
	if port == "" {
		port = fmt.Sprintf("%d", s.DefaultPort())
	}
	q := u.Query()
	if dbName := strings.TrimPrefix(u.Path, "/"); dbName != "" && q.Get("database") == "" {
		q.Set("database", dbName)
	}
	if q.Get("encrypt") == "" {
		q.Set("encrypt", "disable")
	}
	if q.Get("app name") == "" {
		q.Set("app name", "superset-go")
	}
	out := &url.URL{
		Scheme:   "sqlserver",
		Host:     net.JoinHostPort(u.Hostname(), port),
		RawQuery: q.Encode(),
	}
	if u.User != nil {
		out.User = u.User
	}
	return "sqlserver", out.String(), nil
}

// Open returns a go-mssqldb pool.
func (s *MSSQLSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("sqlserver", dsn) }

// SchemasQuery lists schemas in the current database.
func (s *MSSQLSpec) SchemasQuery() string {
	return `SELECT schema_name FROM INFORMATION_SCHEMA.SCHEMATA
	        WHERE schema_name NOT IN ('sys','INFORMATION_SCHEMA','db_owner','db_accessadmin',
	                                  'db_securityadmin','db_ddladmin','db_backupoperator',
	                                  'db_datareader','db_datawriter','db_denydatareader',
	                                  'db_denydatawriter','guest')
	        ORDER BY schema_name`
}

// TablesQuery lists tables and views in a schema.
func (s *MSSQLSpec) TablesQuery() string {
	return `SELECT table_name, table_type FROM INFORMATION_SCHEMA.TABLES
	        WHERE table_schema = '%s' ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *MSSQLSpec) ColumnsQuery() string {
	return `SELECT column_name, data_type, is_nullable, column_default
	        FROM INFORMATION_SCHEMA.COLUMNS
	        WHERE table_schema = '%s' AND table_name = '%s'
	        ORDER BY ordinal_position`
}

func init() {
	Register(&MSSQLSpec{BaseSpec{
		EngineValue:      "mssql",
		EngineNameValue:  "Microsoft SQL Server",
		DriverValue:      "pyodbc",
		PortValue:        1433,
		QuoteStart:       "[",
		QuoteEnd:         "]",
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    false,
		FileUpload:       true,
		MaxNameLen:       128,
		DefaultSchemaVal: "dbo",
		Info:             map[string]any{"supports_dynamic_catalog": true},
	}})
}
