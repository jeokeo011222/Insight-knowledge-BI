package connectors

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
)

// PGSpec implements engines that speak the PostgreSQL wire protocol. pgx
// talks to all of them, so the only differences are the default port, the
// default SSL mode and the dialect metadata.
type PGSpec struct {
	BaseSpec
	sslMode      string
	epochFn      func(string) string
	schemasQuery string
}

// TimeGrainExpr renders DATE_TRUNC (with the multi-minute variants that
// PostgresEngineSpec defines).
func (s *PGSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return pgTimeGrain(col, grain, tz, s.Engine())
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *PGSpec) EpochToDTTM(col string) string {
	if s.epochFn != nil {
		return s.epochFn(col)
	}
	return fmt.Sprintf("TO_TIMESTAMP(%s)", col)
}

// DSN converts a SQLAlchemy URI into a pgx connection string.
func (s *PGSpec) DSN(uri string) (string, string, error) {
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
	u.Scheme = "postgresql"
	u.Host = net.JoinHostPort(u.Hostname(), port)
	q := u.Query()
	if q.Get("sslmode") == "" && s.sslMode != "" {
		q.Set("sslmode", s.sslMode)
	}
	u.RawQuery = q.Encode()
	return "pgx", u.String(), nil
}

// Open returns a pgx-backed pool.
func (s *PGSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("pgx", dsn) }

// SchemasQuery lists user schemas.
func (s *PGSpec) SchemasQuery() string {
	if s.schemasQuery != "" {
		return s.schemasQuery
	}
	return `SELECT schema_name FROM information_schema.schemata
	        WHERE schema_name NOT IN ('pg_catalog','information_schema','pg_toast')
	        ORDER BY schema_name`
}

// TablesQuery lists tables and views in a schema.
func (s *PGSpec) TablesQuery() string {
	return `SELECT table_name, table_type FROM information_schema.tables
	        WHERE table_schema = '%s' AND table_name NOT LIKE 'pg_%%'
	        ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *PGSpec) ColumnsQuery() string {
	return `SELECT column_name, data_type, is_nullable, column_default
	        FROM information_schema.columns
	        WHERE table_schema = '%s' AND table_name = '%s'
	        ORDER BY ordinal_position`
}

func newPGSpec(engine, displayName, driver string, port int, sslMode, defaultSchema string) *PGSpec {
	return &PGSpec{
		BaseSpec: BaseSpec{
			EngineValue:      engine,
			EngineNameValue:  displayName,
			DriverValue:      driver,
			PortValue:        port,
			QuoteChar:        `"`,
			HasJoins:         true,
			HasSubqueries:    true,
			AliasInSelect:    true,
			FileUpload:       true,
			MaxNameLen:       63,
			DefaultSchemaVal: defaultSchema,
			Info:             map[string]any{"supports_dynamic_catalog": false},
		},
		sslMode: sslMode,
	}
}

func init() {
	// Amazon Redshift is a Postgres fork on port 5439 and requires TLS by
	// default; its epoch conversion uses interval arithmetic.
	redshift := newPGSpec("redshift", "Amazon Redshift", "psycopg2", 5439, "prefer", "public")
	redshift.MaxNameLen = 127
	redshift.epochFn = func(col string) string {
		return fmt.Sprintf("(TIMESTAMP 'epoch' + %s * INTERVAL '1 second')", col)
	}
	redshift.Info = map[string]any{"supports_dynamic_catalog": true}
	Register(redshift)

	greenplum := newPGSpec("greenplum", "Greenplum", "psycopg2", 5432, "disable", "public")
	greenplum.epochFn = func(col string) string {
		return fmt.Sprintf("TO_TIMESTAMP(%s)", col)
	}
	Register(greenplum)

	timescale := newPGSpec("timescaledb", "TimescaleDB", "psycopg2", 5432, "disable", "public")
	timescale.Info = map[string]any{"supports_dynamic_catalog": true}
	Register(timescale)

	yugabyte := newPGSpec("yugabytedb", "YugabyteDB", "psycopg2", 5433, "disable", "public")
	Register(yugabyte)

	hologres := newPGSpec("hologres", "Hologres", "psycopg2", 80, "disable", "public")
	hologres.MaxNameLen = 63
	Register(hologres)

	denodo := newPGSpec("denodo", "Denodo", "psycopg2", 9996, "prefer", "public")
	denodo.FileUpload = false
	Register(denodo)

	risingwave := newPGSpec("risingwave", "RisingWave", "psycopg2", 4566, "disable", "public")
	risingwave.FileUpload = false
	risingwave.epochFn = func(col string) string {
		return fmt.Sprintf("TO_TIMESTAMP(%s)", col)
	}
	Register(risingwave)

	// CrateDB exposes the Postgres wire protocol on 5432 alongside its HTTP
	// API (4200). This build connects over the wire protocol, so 5432 is the
	// port that actually works here.
	crate := newPGSpec("crate", "CrateDB", "crate", 5432, "disable", "doc")
	crate.FileUpload = false
	crate.schemasQuery = `SELECT schema_name FROM information_schema.schemata
	        WHERE schema_name NOT IN ('information_schema','pg_catalog','sys','blob')
	        ORDER BY schema_name`
	Register(crate)

	// CockroachDB is wire compatible with Postgres (default port 26257).
	cockroach := newPGSpec("cockroachdb", "CockroachDB", "psycopg2", 26257, "disable", "public")
	cockroach.epochFn = func(col string) string {
		return fmt.Sprintf("TO_TIMESTAMP(%s)", col)
	}
	Register(cockroach)
}
