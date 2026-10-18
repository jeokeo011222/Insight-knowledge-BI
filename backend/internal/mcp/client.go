package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// client.go turns the MCP process into a thin HTTP client of our own REST API.
//
// This is the most important design decision in the package. The obvious
// alternative — open the metadata DB directly and re-run the queries inside the
// MCP process — would mean a second implementation of permission checks, RLS
// predicates, masking and the SQL guard, running as whoever started the
// process. One of the two would drift and the weaker one would be the back
// door. Going through the API means every call is authenticated as a real user
// and shares the exact code path the web UI uses.

// Client talks to the REST API.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient builds a client. baseURL is the server root (e.g.
// http://localhost:8088); /api/v1 is appended here so callers cannot forget it.
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/v1",
		Token:   strings.TrimSpace(token),
		HTTP:    &http.Client{Timeout: 120 * time.Second},
	}
}

// Login exchanges credentials for a bearer token. Used when the MCP client is
// configured with a username/password instead of a long-lived token.
func (c *Client) Login(ctx context.Context, username, password string) error {
	body := map[string]any{"username": username, "password": password, "provider": "db"}
	raw, err := c.do(ctx, http.MethodPost, "/security/login", nil, body, false, false)
	if err != nil {
		return err
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("login response is not JSON: %w", err)
	}
	if out.AccessToken == "" {
		return fmt.Errorf("login returned no access token")
	}
	c.Token = out.AccessToken
	return nil
}

// Get performs an authenticated GET and returns the `result` member (or the
// whole body when the endpoint is not enveloped).
func (c *Client) Get(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, path, query, nil, true, true)
}

// GetEnvelope returns the whole body, unwrapped. Paginated endpoints carry
// `count` next to `result`; a client that only ever sees `result` cannot report
// "showing 50 of 1 200" and will quietly mislead its caller.
func (c *Client) GetEnvelope(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, path, query, nil, true, false)
}

// Post performs an authenticated POST.
func (c *Client) Post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.do(ctx, http.MethodPost, path, nil, body, true, true)
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, auth, unwrap bool) (json.RawMessage, error) {
	target := c.BaseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		blob, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(blob)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		if c.Token == "" {
			return nil, fmt.Errorf("not authenticated: set SUPERSET_MCP_TOKEN or SUPERSET_MCP_USERNAME/PASSWORD")
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		// The API answers {"message": "..."} for every handled error. Surfacing
		// that text verbatim is what lets the calling model fix its request:
		// "UNKNOWN_COLUMN: 字段 \"gendor\" 不在数据集里" is actionable, "400" is not.
		var e struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := firstNonEmpty(e.Message, e.Error, strings.TrimSpace(string(raw)))
		return nil, fmt.Errorf("%s %s failed (%d): %s", method, path, resp.StatusCode, truncate(msg, 800))
	}
	if !auth || !unwrap {
		return raw, nil
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Result) > 0 {
		return envelope.Result, nil
	}
	return raw, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
