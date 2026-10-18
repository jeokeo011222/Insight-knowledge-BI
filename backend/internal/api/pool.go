package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"superset/internal/connectors"
)

// poolHandle wraps a *sql.DB together with the dialect spec used to build SQL.
type poolHandle struct {
	db   *sql.DB
	spec connectors.EngineSpec
	uri  string
}

// openPool materialises a connection pool for a SQLAlchemy URI.
func openPool(uri string) (*poolHandle, error) {
	db, spec, err := connectors.Open(uri)
	if err != nil {
		return nil, err
	}
	return &poolHandle{db: db, spec: spec, uri: uri}, nil
}

// Close releases the pool.
func (p *poolHandle) Close() error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Close()
}

// DB exposes the underlying handle for query execution.
func (p *poolHandle) DB() *sql.DB { return p.db }

// Query runs a statement and returns rows as generic maps. Column names are
// used verbatim (lower-cased to keep the API stable across drivers).
func (p *poolHandle) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

// Exec runs a mutating statement.
func (p *poolHandle) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return p.db.ExecContext(ctx, query, args...)
}

// Ping verifies connectivity.
func (p *poolHandle) Ping(ctx context.Context) error { return p.db.PingContext(ctx) }

// scanRows converts sql.Rows into a slice of column-name keyed maps.
//
// Keys are lower-cased on purpose. Every dialect spec writes its metadata
// queries with lowercase aliases (`SELECT table_name ...`), but drivers echo
// back whatever the server calls the column: MySQL and Oracle report
// information_schema columns as SCHEMA_NAME / TABLE_NAME, DB2 as uppercase
// too. Reading row["table_name"] then silently yields nil, which surfaces as
// an empty schema list and a table picker full of "nil". Normalising here
// keeps every caller dialect-agnostic.
func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[strings.ToLower(c)] = normalizeValue(values[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// rowValue reads a column by name, tolerating case and driver quoting
// differences (e.g. `schema_name`, `SCHEMA_NAME`, `"Schema_Name"`).
//
// scanRows already lower-cases the keys, so this is a belt-and-braces helper
// for the call sites that pull metadata out of a row: a driver that returns an
// uncommon spelling still resolves instead of producing "<nil>" in the UI.
func rowValue(row map[string]any, names ...string) any {
	for _, name := range names {
		if v, ok := row[name]; ok {
			return v
		}
	}
	for key, v := range row {
		bare := strings.Trim(key, `"`)
		for _, name := range names {
			if strings.EqualFold(bare, name) {
				return v
			}
		}
	}
	return nil
}

// rowText is rowValue restricted to the "non-empty string" case, returning ""
// when the column is missing or null.
func rowText(row map[string]any, names ...string) string {
	switch v := rowValue(row, names...).(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// normalizeValue converts driver-specific types into JSON-friendly values.
func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return t
	}
}

// queryStrings runs a query whose first column is a string list.
func queryStrings(ctx context.Context, p *poolHandle, query string) ([]string, error) {
	rows, err := p.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		for _, v := range row {
			out = append(out, fmt.Sprint(v))
			break
		}
	}
	return out, nil
}
