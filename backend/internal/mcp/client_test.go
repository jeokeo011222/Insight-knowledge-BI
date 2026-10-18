package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeAPI is a stand-in for the REST server. The bridge is an HTTP client, so
// the interesting failures are all about transport: wrong path, missing
// Authorization header, an error body nobody read, a result that was not
// unwrapped.
type fakeAPI struct {
	server  *httptest.Server
	gotAuth []string
	gotBody []string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/security/login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != "admin" || body["password"] != "admin" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"bad credentials"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tok-123"}`))
	})
	mux.HandleFunc("/api/v1/dataset/", func(w http.ResponseWriter, r *http.Request) {
		f.gotAuth = append(f.gotAuth, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"count":1,"result":[{"id":7,"table_name":"birth_names","schema":"public","database":{"id":1,"database_name":"examples"},"kind":"physical","description":"出生"}]}`))
	})
	mux.HandleFunc("/api/v1/dataset/7/query", func(w http.ResponseWriter, r *http.Request) {
		f.gotAuth = append(f.gotAuth, r.Header.Get("Authorization"))
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		blob, _ := json.Marshal(body)
		f.gotBody = append(f.gotBody, string(blob))
		if strings.Contains(strings.ToLower(body["sql"].(string)), "delete") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"SQL_GUARD: 只允许只读查询"}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":{"rows":[{"n":1}],"rowcount":1}}`))
	})
	mux.HandleFunc("/api/v1/dataset/7", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/dataset/7":
			_, _ = w.Write([]byte(`{"result":{"id":7,"table_name":"birth_names","schema":"public","database":{"id":1,"database_name":"examples"},"kind":"physical","description":"出生","main_dttm_col":"ds","columns":[
				{"column_name":"gender","type":"VARCHAR","agent_visible":true,"masked":false,"pii_level":"none"},
				{"column_name":"ssn","type":"VARCHAR","agent_visible":false,"masked":true,"pii_level":"high"}],
				"metrics":[{"metric_name":"count","expression":"COUNT(*)","metric_type":"count","description":"行数"}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("/api/v1/dataset/7/semantic/models", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":[{"id":3,"name":"birth_names","status":"published","version":2,"primary_key":"id","manifest_hash":"abc123"}]}`))
	})
	mux.HandleFunc("/api/v1/semantic/model/3", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"publishable":true,"relationships":[],"issues":[
			{"severity":"warning","code":"NO_RELATIONSHIP","field":"","message":"没有关系"},
			{"severity":"error","code":"UNKNOWN_COLUMN","field":"ghost","message":"列不存在"}],
			"manifest":{"fields":[
				{"name":"gender","kind":"column","expression":"gender","type":"VARCHAR"},
				{"name":"secret","kind":"column","expression":"secret","hidden":true}]}}}`))
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func TestLoginThenCallUsesBearerToken(t *testing.T) {
	f := newFakeAPI(t)
	c := NewClient(f.server.URL, "")
	if err := c.Login(context.Background(), "admin", "admin"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if c.Token != "tok-123" {
		t.Fatalf("token not captured: %q", c.Token)
	}
	if _, err := c.Get(context.Background(), "/dataset/", url.Values{"q": {"birth"}}); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(f.gotAuth) == 0 || f.gotAuth[0] != "Bearer tok-123" {
		t.Fatalf("bearer header missing: %v", f.gotAuth)
	}
}

func TestLoginFailureSurfacesMessage(t *testing.T) {
	f := newFakeAPI(t)
	c := NewClient(f.server.URL, "")
	err := c.Login(context.Background(), "admin", "wrong")
	if err == nil || !strings.Contains(err.Error(), "bad credentials") {
		t.Fatalf("error text must be surfaced, got %v", err)
	}
}

// An unauthenticated call must fail locally with an actionable message rather
// than sending a request the server will reject with a bare 401.
func TestCallWithoutTokenExplainsItself(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "")
	_, err := c.Get(context.Background(), "/dataset/", nil)
	if err == nil || !strings.Contains(err.Error(), "SUPERSET_MCP_TOKEN") {
		t.Fatalf("expected a configuration hint, got %v", err)
	}
}

func TestRunSQLForwardsBodyAndSurfacesGuardError(t *testing.T) {
	f := newFakeAPI(t)
	c := NewClient(f.server.URL, "tok")
	tool := runSQLTool(c)

	out, err := tool.Handler(context.Background(), json.RawMessage(`{"dataset_id":7,"sql":"SELECT 1","limit":50}`))
	if err != nil {
		t.Fatalf("run_sql: %v", err)
	}
	res, _ := out.(json.RawMessage)
	if !strings.Contains(string(res), `"rowcount":1`) {
		t.Fatalf("result envelope not unwrapped: %s", res)
	}
	if len(f.gotBody) != 1 || !strings.Contains(f.gotBody[0], `"limit":50`) {
		t.Fatalf("body not forwarded: %v", f.gotBody)
	}

	_, err = tool.Handler(context.Background(), json.RawMessage(`{"dataset_id":7,"sql":"DELETE FROM t"}`))
	if err == nil || !strings.Contains(err.Error(), "只允许只读查询") {
		t.Fatalf("guard rejection must reach the caller, got %v", err)
	}
}

func TestListDatasetsShape(t *testing.T) {
	f := newFakeAPI(t)
	tool := listDatasetsTool(NewClient(f.server.URL, "tok"))
	out, err := tool.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("list_datasets: %v", err)
	}
	payload := out.(map[string]any)
	if payload["count"] != 1 {
		t.Fatalf("count not carried: %+v", payload)
	}
	rows := payload["datasets"].([]map[string]any)
	if len(rows) != 1 || rows[0]["table_name"] != "birth_names" || rows[0]["database"] != "examples" {
		t.Fatalf("dataset row malformed: %+v", rows)
	}
}

// describe_dataset has to hide what the guard hides. A description that lists a
// column the SQL path refuses is a trap: the caller writes SQL that cannot run.
func TestDescribeDatasetHidesAndFilters(t *testing.T) {
	f := newFakeAPI(t)
	tool := describeDatasetTool(NewClient(f.server.URL, "tok"))
	out, err := tool.Handler(context.Background(), json.RawMessage(`{"table_name":"birth_names"}`))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	payload := out.(map[string]any)
	if ds := payload["dataset"].(map[string]any); ds["id"] != float64(7) {
		t.Fatalf("dataset not resolved by table name: %+v", ds)
	}
	cols := payload["columns"].([]map[string]any)
	for _, c := range cols {
		if c["name"] == "ssn" {
			t.Fatalf("agent-hidden column leaked into the description: %+v", cols)
		}
	}
	if len(cols) != 1 || cols[0]["name"] != "gender" {
		t.Fatalf("expected only the visible column, got %+v", cols)
	}
	models := payload["semantic_models"].([]map[string]any)
	if len(models) != 1 || models[0]["name"] != "birth_names" || models[0]["publishable"] != true {
		t.Fatalf("semantic model detail not attached: %+v", models)
	}
	fields := models[0]["fields"].([]map[string]any)
	if len(fields) != 1 || fields[0]["name"] != "gender" {
		t.Fatalf("hidden field should be dropped: %+v", fields)
	}
	issues := models[0]["issues"].([]map[string]any)
	if len(issues) != 2 || issues[1]["code"] != "UNKNOWN_COLUMN" {
		t.Fatalf("issues not carried (a caller must know the model is broken): %+v", issues)
	}
}
