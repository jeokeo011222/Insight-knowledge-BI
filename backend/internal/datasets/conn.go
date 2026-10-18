package datasets

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"superset/internal/connectors"
)

// connHandle wraps the source connection with the dialect spec needed to build
// SQL. It deliberately mirrors api.poolHandle: that helper lives in the HTTP
// package, and importing it would pull the whole API surface into the
// background worker.
type connHandle struct {
	db   *sql.DB
	spec connectors.EngineSpec
}

func openConn(uri string) (*connHandle, error) {
	db, spec, err := connectors.Open(uri)
	if err != nil {
		return nil, err
	}
	return &connHandle{db: db, spec: spec}, nil
}

func (h *connHandle) Close() error {
	if h == nil || h.db == nil {
		return nil
	}
	return h.db.Close()
}

// Query runs a metadata statement and returns rows keyed by lower-cased column
// name. Drivers echo the server's own casing (MySQL reports TABLE_NAME, DB2 too),
// so normalising here keeps every reader dialect-agnostic.
func (h *connHandle) Query(ctx context.Context, query string) ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	rows, err := h.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, 8)
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
			switch v := values[i].(type) {
			case []byte:
				row[strings.ToLower(c)] = string(v)
			default:
				row[strings.ToLower(c)] = v
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// rowValue reads a column by name, tolerating quoting and case differences.
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

// rowText is rowValue restricted to strings, returning "" for missing values.
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

// escapeSQL makes a literal safe inside a single-quoted string.
func escapeSQL(s string) string { return strings.ReplaceAll(s, "'", "''") }

// qualified builds the schema-qualified table reference used by the specs.
func qualified(schema, table string) string {
	if schema == "" {
		return table
	}
	return schema + "." + table
}

// isTemporalType mirrors the type heuristic used by the dataset API and the SQL
// Lab column annotator.
func isTemporalType(dtype string) bool {
	dtype = strings.ToLower(dtype)
	for _, marker := range []string{"timestamp", "datetime", "date", "time"} {
		if strings.Contains(dtype, marker) {
			return true
		}
	}
	return false
}

func newUUID() string { return uuid.NewString() }
