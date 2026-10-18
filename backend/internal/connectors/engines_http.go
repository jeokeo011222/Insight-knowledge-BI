package connectors

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
)

// HTTPSpec implements engines whose only query interface is a JSON-over-HTTP
// SQL endpoint. Queries run through the tiny driver in httpsqldriver.go.
type HTTPSpec struct {
	BaseSpec
	dialect     httpSQLDialect
	defaultPath string
	grain       timeGrainFunc
	epochFn     func(string) string
	schemas     string
	tables      string
	columns     string
	// extraBody carries static request fields (Kylin's project).
	extraBody map[string]any
}

// TimeGrainExpr delegates to the dialect.
func (s *HTTPSpec) TimeGrainExpr(col, grain, tz string) (string, error) {
	return s.grain(col, grain, tz)
}

// EpochToDTTM converts epoch seconds to a timestamp.
func (s *HTTPSpec) EpochToDTTM(col string) string {
	if s.epochFn != nil {
		return s.epochFn(col)
	}
	return fmt.Sprintf("CAST(%s AS TIMESTAMP)", col)
}

// DSN converts a SQLAlchemy URI into an httpsql driver DSN.
func (s *HTTPSpec) DSN(uri string) (string, string, error) {
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
	q := u.Query()
	if isTruthy(q.Get("ssl")) || isTruthy(q.Get("https")) || port == "443" {
		scheme = "https"
	}
	base := scheme + "://" + net.JoinHostPort(u.Hostname(), port)
	path := u.Path
	if path == "" || path == "/" {
		path = s.defaultPath
	}
	cfg := httpSQLConfig{
		Dialect:  s.dialect,
		Endpoint: base + path,
		BaseURL:  base,
	}
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}
	if token := firstNonEmpty(q.Get("bearer"), q.Get("token"), q.Get("access_token")); token != "" {
		cfg.Bearer = token
	}
	if len(s.extraBody) > 0 {
		cfg.Extra = map[string]any{}
		for k, v := range s.extraBody {
			cfg.Extra[k] = v
		}
	}
	if project := q.Get("project"); project != "" {
		if cfg.Extra == nil {
			cfg.Extra = map[string]any{}
		}
		cfg.Extra["project"] = project
	}
	dsn, err := encodeHTTPDSN(cfg)
	if err != nil {
		return "", "", err
	}
	return httpSQLDriverName, dsn, nil
}

// Open returns an httpsql pool.
func (s *HTTPSpec) Open(dsn string) (*sql.DB, error) { return sql.Open(httpSQLDriverName, dsn) }

// SchemasQuery lists schemas when the engine exposes a catalogue.
func (s *HTTPSpec) SchemasQuery() string {
	if s.schemas != "" {
		return s.schemas
	}
	return emptySchemasQuery
}

// TablesQuery lists tables when the engine exposes a catalogue.
func (s *HTTPSpec) TablesQuery() string {
	if s.tables != "" {
		return s.tables
	}
	return emptyTablesQuery
}

// ColumnsQuery lists columns when the engine exposes a catalogue.
func (s *HTTPSpec) ColumnsQuery() string {
	if s.columns != "" {
		return s.columns
	}
	return emptyColumnsQuery
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func init() {
	// Apache Druid exposes SQL over POST /druid/v2/sql and keeps a partial
	// INFORMATION_SCHEMA.
	Register(&HTTPSpec{
		BaseSpec: BaseSpec{
			EngineValue:      "druid",
			EngineNameValue:  "Apache Druid",
			DriverValue:      "pydruid",
			PortValue:        9088,
			QuoteChar:        `"`,
			HasJoins:         true,
			HasSubqueries:    true,
			AliasInSelect:    false,
			FileUpload:       false,
			MaxNameLen:       255,
			DefaultSchemaVal: "druid",
			Info:             map[string]any{"supports_dynamic_catalog": false},
		},
		dialect:     dialectDruid,
		defaultPath: "/druid/v2/sql",
		grain:       druidTimeGrain,
		epochFn:     func(col string) string { return fmt.Sprintf("MILLIS_TO_TIMESTAMP(%s * 1000)", col) },
		schemas: `SELECT SCHEMA_NAME AS schema_name FROM INFORMATION_SCHEMA.SCHEMATA
		          ORDER BY SCHEMA_NAME`,
		tables: `SELECT TABLE_NAME AS table_name, TABLE_TYPE AS table_type
		         FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = '%s'
		         ORDER BY TABLE_NAME`,
		columns: `SELECT COLUMN_NAME AS column_name, DATA_TYPE AS data_type,
		                 IS_NULLABLE AS is_nullable, COLUMN_DEFAULT AS column_default
		          FROM INFORMATION_SCHEMA.COLUMNS
		          WHERE TABLE_SCHEMA = '%s' AND TABLE_NAME = '%s'
		          ORDER BY ORDINAL_POSITION`,
	})

	// Apache Pinot exposes SQL over POST /query/sql.
	Register(&HTTPSpec{
		BaseSpec: BaseSpec{
			EngineValue:      "pinot",
			EngineNameValue:  "Apache Pinot",
			DriverValue:      "pinotdb",
			PortValue:        8099,
			QuoteChar:        `"`,
			HasJoins:         true,
			HasSubqueries:    true,
			AliasInSelect:    false,
			FileUpload:       false,
			MaxNameLen:       255,
			DefaultSchemaVal: "default",
			Info:             map[string]any{"supports_dynamic_catalog": false},
		},
		dialect:     dialectPinot,
		defaultPath: "/query/sql",
		grain:       pinotTimeGrain,
		epochFn: func(col string) string {
			return fmt.Sprintf("DATETIMECONVERT(%s, '1:SECONDS:EPOCH', '1:SECONDS:EPOCH', '1:SECONDS')", col)
		},
	})

	// Elasticsearch exposes SQL over POST /_sql?format=json.
	Register(&HTTPSpec{
		BaseSpec: BaseSpec{
			EngineValue:      "elasticsearch",
			EngineNameValue:  "Elasticsearch",
			DriverValue:      "elasticsearch",
			PortValue:        9243,
			QuoteChar:        `"`,
			HasJoins:         false,
			HasSubqueries:    false,
			AliasInSelect:    false,
			FileUpload:       false,
			MaxNameLen:       255,
			DefaultSchemaVal: "",
			Info:             map[string]any{"supports_dynamic_catalog": false},
		},
		dialect:     dialectElasticsearch,
		defaultPath: "/_sql",
		grain:       elasticsearchTimeGrain,
	})

	// Apache Kylin exposes SQL over POST /kylin/api/query and needs the
	// project name in the request body.
	Register(&HTTPSpec{
		BaseSpec: BaseSpec{
			EngineValue:      "kylin",
			EngineNameValue:  "Apache Kylin",
			DriverValue:      "kylin",
			PortValue:        7070,
			QuoteChar:        `"`,
			HasJoins:         true,
			HasSubqueries:    true,
			AliasInSelect:    false,
			FileUpload:       false,
			MaxNameLen:       255,
			DefaultSchemaVal: "default",
			Info:             map[string]any{"supports_dynamic_catalog": false},
		},
		dialect:     dialectKylin,
		defaultPath: "/kylin/api/query",
		grain:       kylinTimeGrain,
	})
}
