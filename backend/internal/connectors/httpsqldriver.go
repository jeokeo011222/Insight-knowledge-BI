package connectors

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// This file implements a deliberately small database/sql driver for engines
// that only expose an HTTP JSON SQL endpoint (Druid, Pinot, Elasticsearch,
// Kylin). It is not a general purpose SQL client: it POSTs one statement and
// decodes the vendor specific result envelope. Anything it cannot parse is
// reported as an explicit error instead of panicking.

// httpSQLDialect enumerates the supported response envelopes.
type httpSQLDialect string

const (
	dialectDruid         httpSQLDialect = "druid"
	dialectPinot         httpSQLDialect = "pinot"
	dialectElasticsearch httpSQLDialect = "elasticsearch"
	dialectKylin         httpSQLDialect = "kylin"
)

// driverName is the database/sql driver name used by all HTTP engines.
const httpSQLDriverName = "httpsql"

// httpSQLConfig is the connection descriptor encoded as the DSN.
type httpSQLConfig struct {
	Dialect  httpSQLDialect `json:"dialect"`
	Endpoint string         `json:"endpoint"`
	BaseURL  string         `json:"base_url,omitempty"`
	User     string         `json:"user,omitempty"`
	Password string         `json:"password,omitempty"`
	Bearer   string         `json:"bearer,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
	Timeout  int            `json:"timeout_seconds,omitempty"`
}

// encodeHTTPDSN serialises the configuration into the driver DSN.
func encodeHTTPDSN(cfg httpSQLConfig) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("httpsql: cannot encode connection config: %w", err)
	}
	return string(b), nil
}

func init() {
	sql.Register(httpSQLDriverName, &httpSQLDriver{})
}

// httpSQLDriver implements driver.Driver.
type httpSQLDriver struct{}

// Open decodes the JSON DSN and returns a connection.
func (d *httpSQLDriver) Open(dsn string) (driver.Conn, error) {
	var cfg httpSQLConfig
	if err := json.Unmarshal([]byte(dsn), &cfg); err != nil {
		return nil, fmt.Errorf("httpsql: invalid DSN: %w", err)
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("httpsql: DSN is missing the endpoint")
	}
	if cfg.Dialect == "" {
		return nil, fmt.Errorf("httpsql: DSN is missing the dialect")
	}
	timeout := 60 * time.Second
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	return &httpSQLConn{cfg: cfg, client: &http.Client{Timeout: timeout}}, nil
}

// httpSQLConn implements driver.Conn, driver.QueryerContext and driver.Pinger.
type httpSQLConn struct {
	cfg    httpSQLConfig
	client *http.Client
}

// Prepare is not supported: the HTTP endpoints take a complete statement.
func (c *httpSQLConn) Prepare(query string) (driver.Stmt, error) {
	return nil, fmt.Errorf("httpsql: prepared statements are not supported by the %s endpoint", c.cfg.Dialect)
}

// Close releases the connection. The underlying client keeps no state.
func (c *httpSQLConn) Close() error { return nil }

// Begin is not supported.
func (c *httpSQLConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("httpsql: transactions are not supported by the %s endpoint", c.cfg.Dialect)
}

// Ping checks that the server answers HTTP at all.
func (c *httpSQLConn) Ping(ctx context.Context) error {
	target := c.cfg.BaseURL
	if target == "" {
		target = c.cfg.Endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("httpsql: cannot build ping request: %w", err)
	}
	c.applyAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("httpsql: %s is unreachable: %w", c.cfg.Dialect, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("httpsql: %s rejected the credentials (HTTP %d)", c.cfg.Dialect, resp.StatusCode)
	}
	return nil
}

// QueryContext executes one statement against the HTTP endpoint.
func (c *httpSQLConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) > 0 {
		return nil, fmt.Errorf("httpsql: bind parameters are not supported by the %s endpoint", c.cfg.Dialect)
	}
	cols, rows, err := c.query(ctx, query)
	if err != nil {
		return nil, err
	}
	return &httpSQLRows{cols: cols, rows: rows}, nil
}

// ExecContext reports that the endpoint is read-only for our purposes.
func (c *httpSQLConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return nil, fmt.Errorf("httpsql: the %s endpoint is query-only", c.cfg.Dialect)
}

func (c *httpSQLConn) applyAuth(req *http.Request) {
	switch {
	case c.cfg.Bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.cfg.Bearer)
	case c.cfg.User != "":
		req.SetBasicAuth(c.cfg.User, c.cfg.Password)
	}
}

func (c *httpSQLConn) query(ctx context.Context, statement string) ([]string, [][]driver.Value, error) {
	payload := map[string]any{}
	switch c.cfg.Dialect {
	case dialectDruid, dialectElasticsearch:
		payload["query"] = statement
	case dialectPinot:
		payload["sql"] = statement
	case dialectKylin:
		payload["sql"] = statement
		for k, v := range c.cfg.Extra {
			payload[k] = v
		}
	default:
		return nil, nil, fmt.Errorf("httpsql: unsupported dialect %q", c.cfg.Dialect)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("httpsql: cannot encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("httpsql: cannot build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	c.applyAuth(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("httpsql: %s request failed: %w", c.cfg.Dialect, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("httpsql: cannot read %s response: %w", c.cfg.Dialect, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("httpsql: %s returned HTTP %d: %s", c.cfg.Dialect, resp.StatusCode, truncateBody(raw))
	}
	return decodeHTTPResult(c.cfg.Dialect, raw)
}

// decodeHTTPResult dispatches on the vendor response envelope.
func decodeHTTPResult(dialect httpSQLDialect, raw []byte) ([]string, [][]driver.Value, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	switch dialect {
	case dialectDruid:
		return decodeDruid(dec, raw)
	case dialectPinot:
		return decodePinot(dec, raw)
	case dialectElasticsearch:
		return decodeElasticsearch(dec, raw)
	case dialectKylin:
		return decodeKylin(dec, raw)
	}
	return nil, nil, fmt.Errorf("httpsql: unsupported dialect %q", dialect)
}

// decodeDruid reads the [{"col": value}] array Druid returns.
func decodeDruid(dec *json.Decoder, raw []byte) ([]string, [][]driver.Value, error) {
	var records []map[string]any
	if err := dec.Decode(&records); err != nil {
		return nil, nil, fmt.Errorf("httpsql: cannot decode druid response: %w (body: %s)", err, truncateBody(raw))
	}
	if len(records) == 0 {
		return []string{}, [][]driver.Value{}, nil
	}
	// Druid returns JSON objects without an explicit column order; use a
	// stable (sorted) order so repeated queries agree.
	cols := make([]string, 0, len(records[0]))
	for k := range records[0] {
		cols = append(cols, k)
	}
	sortStrings(cols)
	rows := make([][]driver.Value, 0, len(records))
	for _, rec := range records {
		row := make([]driver.Value, len(cols))
		for i, c := range cols {
			row[i] = toDriverValue(rec[c])
		}
		rows = append(rows, row)
	}
	return cols, rows, nil
}

// decodePinot reads {"resultTable": {"dataSchema": ..., "rows": [...]}}.
func decodePinot(dec *json.Decoder, raw []byte) ([]string, [][]driver.Value, error) {
	var envelope struct {
		ResultTable *struct {
			DataSchema struct {
				ColumnNames []string `json:"columnNames"`
				ColumnTypes []string `json:"columnTypes"`
			} `json:"dataSchema"`
			Rows [][]any `json:"rows"`
		} `json:"resultTable"`
		Exceptions []struct {
			Message string `json:"message"`
		} `json:"exceptions"`
	}
	if err := dec.Decode(&envelope); err != nil {
		return nil, nil, fmt.Errorf("httpsql: cannot decode pinot response: %w (body: %s)", err, truncateBody(raw))
	}
	if len(envelope.Exceptions) > 0 {
		return nil, nil, fmt.Errorf("httpsql: pinot query failed: %s", envelope.Exceptions[0].Message)
	}
	if envelope.ResultTable == nil {
		return nil, nil, fmt.Errorf("httpsql: pinot response has no resultTable: %s", truncateBody(raw))
	}
	cols := envelope.ResultTable.DataSchema.ColumnNames
	rows := make([][]driver.Value, 0, len(envelope.ResultTable.Rows))
	for _, src := range envelope.ResultTable.Rows {
		row := make([]driver.Value, len(src))
		for i, v := range src {
			row[i] = toDriverValue(v)
		}
		rows = append(rows, row)
	}
	if cols == nil {
		cols = []string{}
	}
	return cols, rows, nil
}

// decodeElasticsearch reads {"columns": [...], "rows": [[...]]}.
func decodeElasticsearch(dec *json.Decoder, raw []byte) ([]string, [][]driver.Value, error) {
	var envelope struct {
		Columns *[]struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"columns"`
		Rows  *[][]any        `json:"rows"`
		Error json.RawMessage `json:"error"`
	}
	if err := dec.Decode(&envelope); err != nil {
		return nil, nil, fmt.Errorf("httpsql: cannot decode elasticsearch response: %w (body: %s)", err, truncateBody(raw))
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return nil, nil, fmt.Errorf("httpsql: elasticsearch query failed: %s", string(envelope.Error))
	}
	if envelope.Columns == nil {
		return nil, nil, fmt.Errorf("httpsql: elasticsearch response has no columns: %s", truncateBody(raw))
	}
	cols := make([]string, 0, len(*envelope.Columns))
	for _, c := range *envelope.Columns {
		cols = append(cols, c.Name)
	}
	rows := [][]driver.Value{}
	if envelope.Rows != nil {
		for _, src := range *envelope.Rows {
			row := make([]driver.Value, len(src))
			for i, v := range src {
				row[i] = toDriverValue(v)
			}
			rows = append(rows, row)
		}
	}
	return cols, rows, nil
}

// decodeKylin reads {"columnMetas": [...], "results": [...]}. Kylin returns
// results either as row arrays or as objects keyed by column name; both are
// accepted.
func decodeKylin(dec *json.Decoder, raw []byte) ([]string, [][]driver.Value, error) {
	var envelope struct {
		ColumnMetas *[]struct {
			Name string `json:"name"`
		} `json:"columnMetas"`
		Results   []json.RawMessage `json:"results"`
		Exception string            `json:"exception"`
	}
	if err := dec.Decode(&envelope); err != nil {
		return nil, nil, fmt.Errorf("httpsql: cannot decode kylin response: %w (body: %s)", err, truncateBody(raw))
	}
	if envelope.Exception != "" {
		return nil, nil, fmt.Errorf("httpsql: kylin query failed: %s", envelope.Exception)
	}
	if envelope.ColumnMetas == nil {
		return nil, nil, fmt.Errorf("httpsql: kylin response has no columnMetas: %s", truncateBody(raw))
	}
	cols := make([]string, 0, len(*envelope.ColumnMetas))
	for _, c := range *envelope.ColumnMetas {
		cols = append(cols, c.Name)
	}
	rows := make([][]driver.Value, 0, len(envelope.Results))
	for _, item := range envelope.Results {
		trimmed := bytes.TrimSpace(item)
		if len(trimmed) == 0 {
			continue
		}
		if trimmed[0] == '[' {
			var src []any
			if err := json.Unmarshal(trimmed, &src); err != nil {
				return nil, nil, fmt.Errorf("httpsql: cannot decode kylin row: %w", err)
			}
			row := make([]driver.Value, len(src))
			for i, v := range src {
				row[i] = toDriverValue(v)
			}
			rows = append(rows, row)
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return nil, nil, fmt.Errorf("httpsql: cannot decode kylin row: %w", err)
		}
		row := make([]driver.Value, len(cols))
		for i, c := range cols {
			row[i] = toDriverValue(obj[c])
		}
		rows = append(rows, row)
	}
	return cols, rows, nil
}

// toDriverValue converts a decoded JSON value into a database/sql value.
func toDriverValue(v any) driver.Value {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return t
	case bool:
		return t
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case float64:
		return t
	case int64:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// truncateBody shortens an error body for logging.
func truncateBody(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 512 {
		s = s[:512] + "..."
	}
	if s == "" {
		s = "<empty body>"
	}
	return s
}

// httpSQLRows implements driver.Rows over an in-memory result set.
type httpSQLRows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

// Columns returns the result column names.
func (r *httpSQLRows) Columns() []string { return r.cols }

// Close releases the result set.
func (r *httpSQLRows) Close() error { r.rows = nil; return nil }

// Next copies the next row into dest.
func (r *httpSQLRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.pos]
	r.pos++
	for i := range dest {
		if i < len(row) {
			dest[i] = row[i]
		} else {
			dest[i] = nil
		}
	}
	return nil
}

// sortStrings sorts in place; kept local to avoid pulling sort into the
// decoder call sites.
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
