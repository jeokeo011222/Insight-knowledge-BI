package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProbeProviderModelsHappyPath: an OpenAI-compatible /models endpoint is
// enough on its own — no tokens are spent.
func TestProbeProviderModelsHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization header = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`))
	}))
	defer srv.Close()

	probe, err := ProbeProvider(context.Background(), ProviderConfig{
		BaseURL: srv.URL + "/v1", Model: "deepseek-chat", APIKey: "secret", TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("ProbeProvider: %v", err)
	}
	if !probe.OK || probe.Method != "models" {
		t.Fatalf("probe = %+v", probe)
	}
	if len(probe.Models) != 2 || probe.Model != "deepseek-chat" {
		t.Fatalf("models = %v model = %q", probe.Models, probe.Model)
	}
}

// TestProbeProviderFallsBackToChat: vendors without /models must still be
// verifiable through the call the agent actually makes.
func TestProbeProviderFallsBackToChat(t *testing.T) {
	var chatCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			http.NotFound(w, r)
		case "/v1/chat/completions":
			chatCalled = true
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	probe, err := ProbeProvider(context.Background(), ProviderConfig{
		BaseURL: srv.URL + "/v1", Model: "gpt-4o-mini", TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("ProbeProvider: %v", err)
	}
	if !chatCalled || !probe.OK || probe.Method != "chat" {
		t.Fatalf("chatCalled=%v probe=%+v", chatCalled, probe)
	}
}

// TestProbeProviderAuthFailure: a 401 must be reported as auth, not silently
// retried against another path.
func TestProbeProviderAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid api key"}`))
	}))
	defer srv.Close()

	probe, err := ProbeProvider(context.Background(), ProviderConfig{
		BaseURL: srv.URL, Model: "m", APIKey: "bad", TimeoutSec: 5,
	})
	if err != nil {
		t.Fatalf("ProbeProvider: %v", err)
	}
	if probe.OK || !strings.Contains(probe.Message, "鉴权失败") {
		t.Fatalf("probe = %+v", probe)
	}
}

// TestProbeProviderRequiresModelWhenNoCatalogue: without /models and without a
// model name there is nothing to call, and the message must say so.
func TestProbeProviderRequiresModelWhenNoCatalogue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	probe, err := ProbeProvider(context.Background(), ProviderConfig{BaseURL: srv.URL, TimeoutSec: 5})
	if err != nil {
		t.Fatalf("ProbeProvider: %v", err)
	}
	if probe.OK || !strings.Contains(probe.Message, "模型名称") {
		t.Fatalf("probe = %+v", probe)
	}
}

// TestProbeProviderRejectsBadScheme keeps a typo from turning into a DNS probe.
func TestProbeProviderRejectsBadScheme(t *testing.T) {
	_, err := ProbeProvider(context.Background(), ProviderConfig{BaseURL: "127.0.0.1:11434/v1"})
	if !errors.Is(err, ErrProbeBaseURL) {
		t.Fatalf("err = %v", err)
	}
}
