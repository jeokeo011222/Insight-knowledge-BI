package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AuthConfig describes how a REST request authenticates against the remote
// system. Type is one of none / bearer / basic / api_key.
type AuthConfig struct {
	Type     string `json:"type,omitempty"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	KeyName  string `json:"key_name,omitempty"`
	KeyValue string `json:"key_value,omitempty"`
	// In selects where an api_key travels: "header" (default) or "query".
	In string `json:"in,omitempty"`
}

// RESTSource is the user supplied description of a remote JSON endpoint. It is
// persisted verbatim on the dataset so the data can be re-pulled later.
type RESTSource struct {
	URL        string            `json:"url"`
	Method     string            `json:"method,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Params     map[string]string `json:"params,omitempty"`
	Body       string            `json:"body,omitempty"`
	Auth       AuthConfig        `json:"auth,omitempty"`
	RecordPath string            `json:"record_path,omitempty"`
	MaxRows    int               `json:"max_rows,omitempty"`
	TimeoutSec int               `json:"timeout_sec,omitempty"`
}

const (
	defaultTimeoutSec = 30
	maxTimeoutSec     = 120
	defaultMaxRows    = 1000
	maxMaxRows        = 100000
	maxResponseBytes  = 64 << 20
)

// Fetch performs the configured request and converts the JSON payload into a
// typed Table. Only http/https URLs are allowed and the response size, timeout
// and row count are all bounded.
func Fetch(ctx context.Context, src RESTSource) (*Table, error) {
	endpoint, err := url.Parse(strings.TrimSpace(src.URL))
	if err != nil || endpoint.Host == "" {
		return nil, fmt.Errorf("a valid API URL is required")
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("the API URL must use http or https")
	}

	method := strings.ToUpper(strings.TrimSpace(src.Method))
	if method == "" {
		method = http.MethodGet
	}

	query := endpoint.Query()
	for k, v := range src.Params {
		query.Set(k, v)
	}
	if strings.EqualFold(src.Auth.Type, "api_key") && strings.EqualFold(src.Auth.In, "query") && src.Auth.KeyName != "" {
		query.Set(src.Auth.KeyName, src.Auth.KeyValue)
	}
	endpoint.RawQuery = query.Encode()

	var body io.Reader
	if strings.TrimSpace(src.Body) != "" {
		body = strings.NewReader(src.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("could not build the request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range src.Headers {
		req.Header.Set(k, v)
	}
	applyAuth(req, src.Auth)

	timeout := src.TimeoutSec
	if timeout <= 0 {
		timeout = defaultTimeoutSec
	}
	if timeout > maxTimeoutSec {
		timeout = maxTimeoutSec
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("the API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes))
	dec.UseNumber()
	var payload any
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("could not parse the API JSON response: %w", err)
	}

	records, err := extractRecords(payload, src.RecordPath)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("the API response contained no records")
	}
	return tableFromRecords(records, src.MaxRows), nil
}

// applyAuth injects credentials according to the configured scheme.
func applyAuth(req *http.Request, a AuthConfig) {
	switch strings.ToLower(strings.TrimSpace(a.Type)) {
	case "bearer", "token":
		if a.Token != "" {
			req.Header.Set("Authorization", "Bearer "+a.Token)
		}
	case "basic":
		if a.Username != "" || a.Password != "" {
			req.SetBasicAuth(a.Username, a.Password)
		}
	case "api_key":
		if a.KeyName != "" && !strings.EqualFold(a.In, "query") {
			req.Header.Set(a.KeyName, a.KeyValue)
		}
	}
}

// extractRecords walks the record path and returns the array of records. When
// no path is configured the payload itself is used: an array of objects, the
// single array-valued field of an envelope, or a lone object.
func extractRecords(payload any, path string) ([]map[string]any, error) {
	node := payload
	if p := strings.TrimSpace(path); p != "" {
		resolved, err := navigate(payload, p)
		if err != nil {
			return nil, err
		}
		node = resolved
	} else if obj, ok := payload.(map[string]any); ok {
		if arr := singleArrayField(obj); arr != nil {
			node = arr
		}
	}
	return recordsOf(node)
}

// singleArrayField returns the only array-valued field of an object payload, or
// nil when there is none or several. It lets common envelopes such as
// {"data": [...]} work without an explicit record path.
func singleArrayField(obj map[string]any) []any {
	var found []any
	for _, value := range obj {
		if arr, ok := value.([]any); ok {
			if found != nil {
				return nil // ambiguous: more than one array field
			}
			found = arr
		}
	}
	return found
}

// recordsOf normalises an arbitrary node into a list of record objects.
func recordsOf(node any) ([]map[string]any, error) {
	switch t := node.(type) {
	case []any:
		out := make([]map[string]any, 0, len(t))
		for _, item := range t {
			switch record := item.(type) {
			case map[string]any:
				out = append(out, record)
			default:
				out = append(out, map[string]any{"value": record})
			}
		}
		return out, nil
	case map[string]any:
		return []map[string]any{t}, nil
	case nil:
		return nil, fmt.Errorf("the configured record path was not found in the response")
	default:
		return []map[string]any{{"value": t}}, nil
	}
}

// navigate follows a dot separated path. A numeric segment indexes into arrays.
func navigate(node any, path string) (any, error) {
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			continue
		}
		switch current := node.(type) {
		case map[string]any:
			value, ok := current[segment]
			if !ok {
				return nil, fmt.Errorf("the record path %q was not found in the response", path)
			}
			node = value
		case []any:
			idx, err := strconv.Atoi(segment)
			if err != nil || idx < 0 || idx >= len(current) {
				return nil, fmt.Errorf("the record path %q was not found in the response", path)
			}
			node = current[idx]
		default:
			return nil, fmt.Errorf("the record path %q was not found in the response", path)
		}
	}
	return node, nil
}

// tableFromRecords flattens record objects into a typed Table, preserving the
// first-seen order of keys across all records.
func tableFromRecords(records []map[string]any, maxRows int) *Table {
	if maxRows <= 0 {
		maxRows = defaultMaxRows
	}
	if maxRows > maxMaxRows {
		maxRows = maxMaxRows
	}
	if len(records) > maxRows {
		records = records[:maxRows]
	}

	order := make([]string, 0, 16)
	seen := map[string]struct{}{}
	for _, record := range records {
		for key := range record {
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				order = append(order, key)
			}
		}
	}
	header := UniqueNames(order)

	raw := make([][]any, len(records))
	for i, record := range records {
		row := make([]any, len(order))
		for j, key := range order {
			row[j] = flattenValue(record[key])
		}
		raw[i] = row
	}
	return typedTable(header, raw)
}

// flattenValue renders nested objects/arrays as compact JSON so every cell is a
// scalar the SQLite writer can store.
func flattenValue(v any) any {
	switch v.(type) {
	case map[string]any, []any:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprint(v)
	default:
		return v
	}
}
