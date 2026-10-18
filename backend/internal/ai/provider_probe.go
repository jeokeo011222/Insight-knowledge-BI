package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProviderProbe is the outcome of a connectivity check against an
// OpenAI-compatible endpoint. It is intentionally richer than a boolean: an
// operator needs to know whether the failure was DNS, auth, a wrong path or a
// missing model, because each has a different fix.
type ProviderProbe struct {
	OK        bool     `json:"ok"`
	LatencyMS int64    `json:"latency_ms"`
	Method    string   `json:"method"` // models | chat
	Models    []string `json:"models,omitempty"`
	Model     string   `json:"model,omitempty"`
	Endpoint  string   `json:"endpoint"`
	Message   string   `json:"message"`
}

// probeMaxModels caps the catalogue echoed to the browser.
const probeMaxModels = 50

// ErrProbeBaseURL marks a malformed / non-http endpoint so the caller can map
// it to a 400 rather than a 502.
var ErrProbeBaseURL = errors.New("base url must start with http:// or https://")

// ProbeProvider verifies that an endpoint answers and that credentials work.
//
// Strategy: ask `GET {base}/models` first. It costs no tokens and returns the
// model catalogue the editor can show. Several vendors do not implement it
// (they answer 404/405), so the fallback is a single minimal
// `POST {base}/chat/completions` — the very call the agent will make at run
// time, which also proves the configured model actually exists.
func ProbeProvider(ctx context.Context, pc ProviderConfig) (ProviderProbe, error) {
	base := strings.TrimRight(strings.TrimSpace(pc.BaseURL), "/")
	if base == "" {
		return ProviderProbe{}, fmt.Errorf("%w: 接口地址为空", ErrProbeBaseURL)
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" {
		return ProviderProbe{}, fmt.Errorf("%w: %s", ErrProbeBaseURL, base)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ProviderProbe{}, fmt.Errorf("%w: %s", ErrProbeBaseURL, base)
	}

	timeout := time.Duration(pc.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := &http.Client{Timeout: timeout}
	start := time.Now()

	models, listErr := probeModels(ctx, client, base, pc.APIKey)
	if listErr == nil {
		model := strings.TrimSpace(pc.Model)
		matched := model != ""
		if model == "" && len(models) > 0 {
			model = models[0]
			matched = true
		}
		probe := ProviderProbe{
			OK:        true,
			LatencyMS: time.Since(start).Milliseconds(),
			Method:    "models",
			Models:    models,
			Model:     model,
			Endpoint:  base + "/models",
			Message:   fmt.Sprintf("连接成功，接口返回 %d 个可用模型", len(models)),
		}
		if model != "" && !matched {
			probe.Message = fmt.Sprintf("连接成功，但模型列表中未找到 %s", model)
		}
		return probe, nil
	}

	// Auth failures are terminal: retrying through chat/completions would only
	// produce a second 401 and a confusing message.
	var status *probeStatusError
	if errors.As(listErr, &status) && (status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden) {
		return ProviderProbe{OK: false, LatencyMS: time.Since(start).Milliseconds(), Method: "models", Endpoint: base + "/models",
			Message: fmt.Sprintf("鉴权失败（HTTP %d），请检查 API Key", status.Code)}, nil
	}

	model := strings.TrimSpace(pc.Model)
	if model == "" {
		return ProviderProbe{OK: false, LatencyMS: time.Since(start).Milliseconds(), Method: "models", Endpoint: base + "/models",
			Message: fmt.Sprintf("接口未提供模型列表（%s），请填写模型名称后再测试", probeErrText(listErr))}, nil
	}

	latency, chatErr := probeChat(ctx, client, base, pc.APIKey, model)
	if chatErr != nil {
		var chatStatus *probeStatusError
		if errors.As(chatErr, &chatStatus) {
			return ProviderProbe{OK: false, LatencyMS: latency, Method: "chat", Model: model, Endpoint: base + "/chat/completions",
				Message: fmt.Sprintf("调用模型失败（HTTP %d）：%s", chatStatus.Code, firstLine(chatStatus.Body))}, nil
		}
		return ProviderProbe{OK: false, LatencyMS: latency, Method: "chat", Model: model, Endpoint: base + "/chat/completions",
			Message: "无法连接接口：" + chatErr.Error()}, nil
	}

	return ProviderProbe{
		OK:        true,
		LatencyMS: latency,
		Method:    "chat",
		Model:     model,
		Endpoint:  base + "/chat/completions",
		Message:   fmt.Sprintf("连接成功，模型 %s 已完成一次最小调用", model),
	}, nil
}

// probeStatusError carries the HTTP status of a failed probe request.
type probeStatusError struct {
	Code int
	Body string
}

func (e *probeStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, firstLine(e.Body))
}

// probeModels reads the model catalogue. Both the OpenAI shape (data[].id) and
// the Ollama shape (models[].name) are accepted.
func probeModels(ctx context.Context, client *http.Client, base, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	applyBearer(req, apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, &probeStatusError{Code: resp.StatusCode, Body: string(body)}
	}

	var openai struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	var ollama struct {
		Models []struct {
			Name string `json:"name"`
			ID   string `json:"model"`
		} `json:"models"`
	}
	models := make([]string, 0, 8)
	if err := json.Unmarshal(body, &openai); err == nil {
		for _, item := range openai.Data {
			if item.ID != "" {
				models = append(models, item.ID)
			}
		}
	}
	if len(models) == 0 {
		if err := json.Unmarshal(body, &ollama); err == nil {
			for _, item := range ollama.Models {
				name := item.Name
				if name == "" {
					name = item.ID
				}
				if name != "" {
					models = append(models, name)
				}
			}
		}
	}
	if len(models) > probeMaxModels {
		models = models[:probeMaxModels]
	}
	return models, nil
}

// probeChat performs the smallest possible completion so the check exercises
// the same route the agent uses without burning meaningful tokens.
func probeChat(ctx context.Context, client *http.Client, base, apiKey, model string) (int64, error) {
	payload := map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
		"stream":     false,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	applyBearer(req, apiKey)
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return latency, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return latency, &probeStatusError{Code: resp.StatusCode, Body: string(body)}
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return latency, errors.New("接口返回空响应")
	}
	return latency, nil
}

func applyBearer(req *http.Request, apiKey string) {
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

// probeErrText renders a probe error without leaking a full HTML error page.
func probeErrText(err error) string {
	var status *probeStatusError
	if errors.As(err, &status) {
		return fmt.Sprintf("HTTP %d", status.Code)
	}
	return firstLine(err.Error())
}

// firstLine keeps error messages readable in a toast / alert.
func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexAny(text, "\r\n"); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 240 {
		text = text[:240] + "…"
	}
	return text
}
