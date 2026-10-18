package connectors

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// OracleSpec mirrors superset/db_engine_specs/oracle.py.
type OracleSpec struct{ BaseSpec }

// TimeGrainExpr renders TRUNC based truncation.
func (s *OracleSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return oracleTimeGrain(col, grain, tz)
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *OracleSpec) EpochToDTTM(col string) string {
	return fmt.Sprintf("TO_DATE('1970-01-01','YYYY-MM-DD') + NUMTODSINTERVAL(%s,'SECOND')", col)
}

// ConvertToDateTimeExpr casts text to a timestamp.
func (s *OracleSpec) ConvertToDateTimeExpr(col string) string {
	return fmt.Sprintf("CAST(%s AS TIMESTAMP)", col)
}

// LimitClause renders the 12c+ row limiting clause.
func (s *OracleSpec) LimitClause(n int) string { return fetchFirstLimit(n) }

// DSN converts a SQLAlchemy URI into a go-ora URL. The path component is the
// service name, matching oracledb://user:pass@host:port/service.
func (s *OracleSpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("invalid oracle URI: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("oracle URI must include a host")
	}
	port := u.Port()
	if port == "" {
		port = fmt.Sprintf("%d", s.DefaultPort())
	}
	service := strings.TrimPrefix(u.Path, "/")
	q := u.Query()
	if service == "" {
		service = q.Get("service_name")
	}
	if service == "" {
		return "", "", fmt.Errorf("oracle URI must include a service name, e.g. oracle://user:pass@host:1521/ORCLPDB1")
	}
	out := &url.URL{
		Scheme:   "oracle",
		Host:     net.JoinHostPort(u.Hostname(), port),
		Path:     "/" + strings.TrimPrefix(service, "/"),
		RawQuery: q.Encode(),
	}
	if u.User != nil {
		out.User = u.User
	}
	return "oracle", out.String(), nil
}

// Open returns a go-ora pool.
func (s *OracleSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("oracle", dsn) }

// SchemasQuery lists database users, which act as schemas.
func (s *OracleSpec) SchemasQuery() string {
	return `SELECT username AS schema_name FROM all_users ORDER BY username`
}

// TablesQuery lists tables owned by a schema.
func (s *OracleSpec) TablesQuery() string {
	return `SELECT table_name, 'TABLE' AS table_type FROM all_tables
	        WHERE owner = '%s' ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *OracleSpec) ColumnsQuery() string {
	return `SELECT column_name, data_type, nullable AS is_nullable, data_default AS column_default
	        FROM all_tab_columns
	        WHERE owner = '%s' AND table_name = '%s'
	        ORDER BY column_id`
}

func init() {
	Register(&OracleSpec{BaseSpec{
		EngineValue:      "oracle",
		EngineNameValue:  "Oracle",
		DriverValue:      "cx_oracle",
		PortValue:        1521,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    false,
		FileUpload:       true,
		MaxNameLen:       128,
		DefaultSchemaVal: "",
		Info:             map[string]any{"supports_dynamic_catalog": false},
	}})
}
