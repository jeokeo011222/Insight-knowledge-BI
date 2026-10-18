package connectors

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/exasol/exasol-driver-go"
)

// TrinoSpec implements Trino and Presto, which share the same HTTP protocol
// and are both served by trino-go-client.
type TrinoSpec struct {
	BaseSpec
	presto bool
}

// TimeGrainExpr renders date_trunc.
func (s *TrinoSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return dateTruncTimeGrain(col, grain, tz, s.Engine())
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *TrinoSpec) EpochToDTTM(col string) string { return fmt.Sprintf("from_unixtime(%s)", col) }

// DSN converts a SQLAlchemy URI into a trino-go-client DSN. The URI path is
// <catalog>/<schema>.
func (s *TrinoSpec) DSN(uri string) (string, string, error) {
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
	scheme := "http"
	if isTruthy(u.Query().Get("ssl")) || isTruthy(u.Query().Get("https")) || port == "443" {
		scheme = "https"
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	catalog, schema := "", ""
	if len(segments) > 0 {
		catalog = segments[0]
	}
	if len(segments) > 1 {
		schema = segments[1]
	}
	q := u.Query()
	if v := q.Get("catalog"); v != "" {
		catalog = v
	}
	if v := q.Get("schema"); v != "" {
		schema = v
	}
	q.Del("catalog")
	q.Del("schema")
	q.Del("ssl")
	q.Del("https")
	if catalog != "" {
		q.Set("catalog", catalog)
	}
	if schema != "" {
		q.Set("schema", schema)
	}
	out := &url.URL{
		Scheme:   scheme,
		Host:     net.JoinHostPort(u.Hostname(), port),
		RawQuery: q.Encode(),
	}
	if u.User != nil {
		out.User = u.User
	}
	return "trino", out.String(), nil
}

// Open returns a trino-go-client pool.
func (s *TrinoSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("trino", dsn) }

// SchemasQuery lists schemas in the current catalog.
func (s *TrinoSpec) SchemasQuery() string {
	return `SELECT schema_name FROM information_schema.schemata
	        WHERE schema_name NOT IN ('information_schema','pg_catalog','sys')
	        ORDER BY schema_name`
}

// TablesQuery lists tables and views in a schema.
func (s *TrinoSpec) TablesQuery() string {
	return `SELECT table_name, table_type FROM information_schema.tables
	        WHERE table_schema = '%s' ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *TrinoSpec) ColumnsQuery() string {
	return `SELECT column_name, data_type, is_nullable, column_default
	        FROM information_schema.columns
	        WHERE table_schema = '%s' AND table_name = '%s'
	        ORDER BY ordinal_position`
}

// VerticaSpec mirrors superset/db_engine_specs/vertica.py.
type VerticaSpec struct{ BaseSpec }

// TimeGrainExpr renders DATE_TRUNC.
func (s *VerticaSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return dateTruncTimeGrain(col, grain, tz, "vertica")
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *VerticaSpec) EpochToDTTM(col string) string { return fmt.Sprintf("TO_TIMESTAMP(%s)", col) }

// DSN converts a SQLAlchemy URI into a vertica-sql-go DSN.
func (s *VerticaSpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("invalid vertica URI: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("vertica URI must include a host")
	}
	port := u.Port()
	if port == "" {
		port = fmt.Sprintf("%d", s.DefaultPort())
	}
	out := &url.URL{
		Scheme:   "vertica",
		Host:     net.JoinHostPort(u.Hostname(), port),
		Path:     u.Path,
		RawQuery: u.Query().Encode(),
	}
	if u.User != nil {
		out.User = u.User
	}
	return "vertica", out.String(), nil
}

// Open returns a vertica-sql-go pool.
func (s *VerticaSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("vertica", dsn) }

// SchemasQuery lists schemas from the Vertica catalogue.
func (s *VerticaSpec) SchemasQuery() string {
	return `SELECT schema_name FROM v_catalog.schemata
	        WHERE schema_name NOT IN ('v_catalog','v_monitor','v_internal')
	        ORDER BY schema_name`
}

// TablesQuery lists tables and views in a schema.
func (s *VerticaSpec) TablesQuery() string {
	return `SELECT table_name, table_type FROM v_catalog.tables
	        WHERE table_schema = '%s' ORDER BY table_name`
}

// ColumnsQuery lists columns of a table.
func (s *VerticaSpec) ColumnsQuery() string {
	return `SELECT column_name, data_type, is_nullable, column_default
	        FROM v_catalog.columns
	        WHERE table_schema = '%s' AND table_name = '%s'
	        ORDER BY ordinal_position`
}

// ExasolSpec mirrors superset/db_engine_specs/exasol.py.
type ExasolSpec struct{ BaseSpec }

// TimeGrainExpr renders DATE_TRUNC.
func (s *ExasolSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return dateTruncTimeGrain(col, grain, tz, "exa")
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *ExasolSpec) EpochToDTTM(col string) string {
	return fmt.Sprintf("ADD_SECONDS(TIMESTAMP '1970-01-01 00:00:00', %s)", col)
}

// DSN converts a SQLAlchemy URI into an Exasol driver DSN.
func (s *ExasolSpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("invalid exa URI: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("exa URI must include a host")
	}
	port := u.Port()
	if port == "" {
		port = fmt.Sprintf("%d", s.DefaultPort())
	}
	var user, pass string
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	var portNum int
	fmt.Sscanf(port, "%d", &portNum)
	dsn := exasol.NewConfig(user, pass).Host(u.Hostname()).Port(portNum).String()
	return "exasol", dsn, nil
}

// Open returns an exasol-driver-go pool.
func (s *ExasolSpec) Open(dsn string) (*sql.DB, error) { return sql.Open("exasol", dsn) }

// SchemasQuery lists schemas.
func (s *ExasolSpec) SchemasQuery() string {
	return `SELECT SCHEMA_NAME AS schema_name FROM SYS.EXA_ALL_SCHEMAS
	        WHERE SCHEMA_NAME NOT IN ('SYS','EXA_STATISTICS') ORDER BY SCHEMA_NAME`
}

// TablesQuery lists tables in a schema.
func (s *ExasolSpec) TablesQuery() string {
	return `SELECT TABLE_NAME AS table_name, 'TABLE' AS table_type FROM SYS.EXA_ALL_TABLES
	        WHERE TABLE_SCHEMA = '%s' ORDER BY TABLE_NAME`
}

// ColumnsQuery lists columns of a table.
func (s *ExasolSpec) ColumnsQuery() string {
	return `SELECT COLUMN_NAME AS column_name, COLUMN_TYPE AS data_type,
	               IS_NULLABLE AS is_nullable, COLUMN_DEFAULT AS column_default
	        FROM SYS.EXA_ALL_COLUMNS
	        WHERE COLUMN_SCHEMA = '%s' AND COLUMN_TABLE = '%s'
	        ORDER BY COLUMN_ORDINAL_POSITION`
}

// HANASpec mirrors superset/db_engine_specs/hana.py.
type HANASpec struct{ BaseSpec }

// TimeGrainExpr renders the SAP HANA string/date conversions.
func (s *HANASpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	norm, err := NormalizeGrain(grain)
	if err != nil {
		return "", fmt.Errorf("hana: %w", err)
	}
	if norm == "" {
		return col, nil
	}
	switch norm {
	case "PT1S":
		return fmt.Sprintf("TO_TIMESTAMP(TO_CHAR(%s, 'YYYY-MM-DD HH24:MI:SS'))", col), nil
	case "PT1M":
		return fmt.Sprintf("TO_TIMESTAMP(TO_CHAR(%s, 'YYYY-MM-DD HH24:MI:00'))", col), nil
	case "PT1H":
		return fmt.Sprintf("TO_TIMESTAMP(TO_CHAR(%s, 'YYYY-MM-DD HH24:00:00'))", col), nil
	case "P1D":
		return fmt.Sprintf("TO_DATE(%s)", col), nil
	case "P1W":
		return fmt.Sprintf("ADD_DAYS(TO_DATE(%[1]s), 1 - DAYOFWEEK(%[1]s))", col), nil
	case "P1M":
		return fmt.Sprintf("TO_DATE(TO_CHAR(%s, 'YYYY-MM') || '-01')", col), nil
	case "P3M":
		return fmt.Sprintf("ADD_MONTHS(TO_DATE(TO_CHAR(%[1]s, 'YYYY-MM') || '-01'), -((MONTH(%[1]s) - 1) %% 3))", col), nil
	case "P1Y":
		return fmt.Sprintf("TO_DATE(TO_CHAR(%s, 'YYYY') || '-01-01')", col), nil
	}
	return "", fmt.Errorf("unsupported time grain %q for hana", grain)
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *HANASpec) EpochToDTTM(col string) string {
	return fmt.Sprintf("ADD_SECONDS(TO_TIMESTAMP('1970-01-01 00:00:00'), %s)", col)
}

// DSN converts a SQLAlchemy URI into a go-hdb DSN.
func (s *HANASpec) DSN(uri string) (string, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", "", fmt.Errorf("invalid hana URI: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("hana URI must include a host")
	}
	port := u.Port()
	if port == "" {
		port = fmt.Sprintf("%d", s.DefaultPort())
	}
	out := &url.URL{
		Scheme:   "hdb",
		Host:     net.JoinHostPort(u.Hostname(), port),
		RawQuery: u.Query().Encode(),
	}
	if u.User != nil {
		out.User = u.User
	}
	return "hdb", out.String(), nil
}

// Open returns a go-hdb pool.
func (s *HANASpec) Open(dsn string) (*sql.DB, error) { return sql.Open("hdb", dsn) }

// SchemasQuery lists schemas.
func (s *HANASpec) SchemasQuery() string {
	return `SELECT SCHEMA_NAME AS schema_name FROM SYS.SCHEMAS
	        WHERE SCHEMA_NAME NOT LIKE 'SYS%' AND SCHEMA_NAME != '_SYS_BIC'
	        ORDER BY SCHEMA_NAME`
}

// TablesQuery lists tables in a schema.
func (s *HANASpec) TablesQuery() string {
	return `SELECT TABLE_NAME AS table_name, TABLE_TYPE AS table_type FROM SYS.TABLES
	        WHERE SCHEMA_NAME = '%s' ORDER BY TABLE_NAME`
}

// ColumnsQuery lists columns of a table.
func (s *HANASpec) ColumnsQuery() string {
	return `SELECT COLUMN_NAME AS column_name, DATA_TYPE_NAME AS data_type,
	               IS_NULLABLE AS is_nullable, DEFAULT_VALUE AS column_default
	        FROM SYS.TABLE_COLUMNS
	        WHERE SCHEMA_NAME = '%s' AND TABLE_NAME = '%s'
	        ORDER BY POSITION`
}

func init() {
	Register(&TrinoSpec{BaseSpec: BaseSpec{
		EngineValue:      "trino",
		EngineNameValue:  "Trino",
		DriverValue:      "trino",
		PortValue:        8080,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       false,
		MaxNameLen:       255,
		DefaultSchemaVal: "",
		Info:             map[string]any{"supports_dynamic_catalog": true},
	}})

	Register(&TrinoSpec{BaseSpec: BaseSpec{
		EngineValue:      "presto",
		EngineNameValue:  "Presto",
		DriverValue:      "presto",
		PortValue:        8080,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       false,
		MaxNameLen:       255,
		DefaultSchemaVal: "",
		Info:             map[string]any{"supports_dynamic_catalog": true},
	}, presto: true})

	Register(&VerticaSpec{BaseSpec{
		EngineValue:      "vertica",
		EngineNameValue:  "Vertica",
		DriverValue:      "vertica_python",
		PortValue:        5433,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       true,
		MaxNameLen:       128,
		DefaultSchemaVal: "public",
		Info:             map[string]any{"supports_dynamic_catalog": false},
	}})

	Register(&ExasolSpec{BaseSpec{
		EngineValue:      "exa",
		EngineNameValue:  "Exasol",
		DriverValue:      "exasol",
		PortValue:        8563,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       true,
		MaxNameLen:       128,
		DefaultSchemaVal: "",
		Info:             map[string]any{"supports_dynamic_catalog": false},
	}})

	Register(&HANASpec{BaseSpec{
		EngineValue:      "hana",
		EngineNameValue:  "SAP HANA",
		DriverValue:      "hdbcli",
		PortValue:        30015,
		QuoteChar:        `"`,
		HasJoins:         true,
		HasSubqueries:    true,
		AliasInSelect:    true,
		FileUpload:       true,
		MaxNameLen:       127,
		DefaultSchemaVal: "",
		Info:             map[string]any{"supports_dynamic_catalog": false},
	}})

	// Snowflake, BigQuery and Athena require vendor SDKs (or OAuth
	// credentials) that are not bundled, so they are specification-only.
	Register(newSpecOnly(engineDef{
		engine: "snowflake", name: "Snowflake", driver: "snowflake", port: 443,
		quote: `"`, aliasInSelect: true, maxName: 255, defaultSchema: "PUBLIC",
		grain: snowflakeTimeGrain,
		schemas: `SELECT schema_name FROM information_schema.schemata
		          WHERE schema_name NOT IN ('INFORMATION_SCHEMA') ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
		epoch: func(col string) string { return fmt.Sprintf("TO_TIMESTAMP(%s)", col) },
		info:  map[string]any{"supports_dynamic_catalog": true},
	}))

	Register(newSpecOnly(engineDef{
		engine: "bigquery", name: "Google BigQuery", driver: "bigquery", port: 443,
		quote: "`", aliasInSelect: true, maxName: 300, defaultSchema: "",
		grain:   bigqueryTimeGrain,
		schemas: "SELECT schema_name FROM `region-us`.INFORMATION_SCHEMA.SCHEMATA ORDER BY schema_name",
		tables: "SELECT table_name, table_type FROM `region-us`.INFORMATION_SCHEMA.TABLES\n" +
			"WHERE table_schema = '%s' ORDER BY table_name",
		columns: "SELECT column_name, data_type, is_nullable, column_default\n" +
			"FROM `region-us`.INFORMATION_SCHEMA.COLUMNS\n" +
			"WHERE table_schema = '%s' AND table_name = '%s' ORDER BY ordinal_position",
		limit: sqlLimit,
		epoch: func(col string) string { return fmt.Sprintf("TIMESTAMP_SECONDS(%s)", col) },
		info:  map[string]any{"supports_dynamic_catalog": true},
	}))

	Register(newSpecOnly(engineDef{
		engine: "awsathena", name: "Amazon Athena", driver: "pyathena", port: 443,
		quote: `"`, aliasInSelect: true, maxName: 255, defaultSchema: "default",
		grain: func(col, grain, tz string) (string, error) {
			return dateTruncTimeGrain(col, grain, tz, "awsathena")
		},
		schemas: `SELECT schema_name FROM information_schema.schemata ORDER BY schema_name`,
		tables: `SELECT table_name, table_type FROM information_schema.tables
		         WHERE table_schema = '%s' ORDER BY table_name`,
		columns: `SELECT column_name, data_type, is_nullable, column_default
		          FROM information_schema.columns
		          WHERE table_schema = '%s' AND table_name = '%s'
		          ORDER BY ordinal_position`,
		limit: sqlLimit,
		epoch: func(col string) string { return fmt.Sprintf("from_unixtime(%s)", col) },
		info:  map[string]any{"supports_dynamic_catalog": false},
	}))
}
