package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/redis/go-redis/v9"

	"superset/internal/config"
	"superset/internal/pkg/cache"
	apperrors "superset/internal/pkg/errors"
)

// LLM gateway errors.
var (
	ErrLLMDisabled    = errors.New("ai assistant is disabled")
	ErrLLMUnavailable = errors.New("ai model is temporarily unavailable")
	ErrLLMNoConfig    = errors.New("ai llm base url is not configured")
)

// Rate limiter defaults (fixed window, per minute).
const (
	rlUserPerMin   = 30
	rlGlobalPerMin = 300
)

// ChatMessage is one OpenAI-compatible message.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds a tool call's name and JSON-encoded arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatTool is a tool declaration sent to the model.
type ChatTool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition describes a callable function for function-calling.
type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ChatRequest is the gateway-level chat request.
type ChatRequest struct {
	Model    string
	Messages []ChatMessage
	Tools    []ChatTool
	// Temperature overrides the deployment default for this call. Nil falls
	// back to cfg.AITemperature: the value is always transmitted, so a pinned
	// 0 is not swallowed by `omitempty` and output stays reproducible.
	Temperature *float64
	// MaxTokens overrides cfg.AIMaxTokens for this call (0 = provider default).
	MaxTokens int
	// Seed overrides cfg.AISeed for this call (0 = provider default).
	Seed   int
	UserID int
	// CacheKey, when set, enables result caching. It should be a stable hash
	// of the prompt + context + toolset.
	CacheKey string
}

// ChatResponse is the gateway-level chat response.
type ChatResponse struct {
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	TokenUsage int        `json:"token_usage"`
	Model      string     `json:"model"`
	Cached     bool       `json:"cached,omitempty"`

	// Effective sampling parameters, echoed so the audit trail records what was
	// actually sent rather than what was hoped for.
	Temperature float64 `json:"-"`
	MaxTokens   int     `json:"-"`
	Seed        int     `json:"-"`
	LatencyMS   int     `json:"-"`
}

// LLMGateway wraps an OpenAI-compatible chat completions endpoint with
// caching, rate limiting, retries and a circuit breaker: the guardrails the
// design requires for every model call.
type LLMGateway struct {
	cfg *config.Config
	// settings carries the runtime-editable LLM knobs; read it on every call so
	// changes made from the settings page take effect without a restart.
	settings *settingsStore
	log      *zap.Logger
	cache    *cache.Client
	client   *http.Client
	brk      *breaker
}

// NewLLMGateway builds a gateway from configuration.
func NewLLMGateway(cfg *config.Config, settings *settingsStore, log *zap.Logger, c *cache.Client) *LLMGateway {
	return &LLMGateway{
		cfg:      cfg,
		settings: settings,
		log:      log,
		cache:    c,
		// No client-level timeout: each call carries its own deadline so the
		// configured timeout can be changed at runtime.
		client: &http.Client{},
		brk:    newBreaker(),
	}
}

// timeout returns the per-call deadline derived from a provider's settings.
func (g *LLMGateway) timeout(pc *ProviderConfig) time.Duration {
	if pc != nil && pc.TimeoutSec > 0 {
		return time.Duration(pc.TimeoutSec) * time.Second
	}
	return 60 * time.Second
}

// Chat performs one model call against the given provider. Both ollama and
// openai_compatible providers speak the OpenAI /chat/completions contract, so
// only the base URL differs. The global master switch still gates every call.
func (g *LLMGateway) Chat(ctx context.Context, pc *ProviderConfig, req *ChatRequest) (*ChatResponse, error) {
	if !g.settings.Load().Enabled {
		return nil, ErrLLMDisabled
	}
	if pc == nil || strings.TrimSpace(pc.BaseURL) == "" {
		return nil, ErrLLMNoConfig
	}
	model := req.Model
	if model == "" {
		model = pc.Model
	}
	smp := g.sampling(pc, req)

	// Request cache: identical prompt + context + toolset reuses the result.
	// Sampling parameters are part of the key, otherwise a re-run at a
	// different temperature would silently serve the old answer.
	cacheSeed := fmt.Sprintf("%s|t=%.3f|mt=%d|seed=%d", req.CacheKey, smp.Temperature, smp.MaxTokens, smp.Seed)
	if g.cache != nil && req.CacheKey != "" {
		var cached ChatResponse
		if ok, err := g.cache.Get(ctx, cacheKey(cacheSeed), &cached); err != nil {
			g.log.Debug("llm cache lookup failed", zap.Error(err))
		} else if ok {
			cached.Cached = true
			cached.Temperature, cached.MaxTokens, cached.Seed = smp.Temperature, smp.MaxTokens, smp.Seed
			return &cached, nil
		}
	}

	if !g.brk.allow() {
		return nil, ErrLLMUnavailable
	}
	if err := g.allow(ctx, req.UserID); err != nil {
		return nil, err
	}

	payload := g.payload(model, req, smp)
	started := time.Now()
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err := g.do(ctx, pc, payload)
		if err == nil {
			g.brk.success()
			resp.Temperature, resp.MaxTokens, resp.Seed = smp.Temperature, smp.MaxTokens, smp.Seed
			resp.LatencyMS = int(time.Since(started).Milliseconds())
			if g.cache != nil && req.CacheKey != "" {
				ttl := time.Duration(g.cfg.AICacheTTLSec) * time.Second
				if ttl <= 0 {
					ttl = time.Hour
				}
				_ = g.cache.Set(ctx, cacheKey(cacheSeed), resp, ttl)
			}
			return resp, nil
		}
		lastErr = err
		// Do not retry client errors (bad request / model not found).
		var hs *httpStatusError
		if errors.As(err, &hs) && hs.code < 500 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
		}
	}
	g.brk.failure()
	g.log.Warn("llm call failed", zap.Error(lastErr))
	return nil, fmt.Errorf("%w: %v", ErrLLMUnavailable, lastErr)
}

// sampling is the resolved set of sampling parameters for one call.
type sampling struct {
	Temperature float64
	MaxTokens   int
	Seed        int
}

// sampling resolves the effective parameters: per-call override → deployment
// config → provider default. A nil Temperature is NOT "unset to zero"; the
// deployment value is always transmitted so behaviour is reproducible.
func (g *LLMGateway) sampling(pc *ProviderConfig, req *ChatRequest) sampling {
	s := sampling{Temperature: g.cfg.AITemperature, MaxTokens: g.cfg.AIMaxTokens, Seed: g.cfg.AISeed}
	if req.Temperature != nil {
		s.Temperature = *req.Temperature
	}
	if req.MaxTokens > 0 {
		s.MaxTokens = req.MaxTokens
	}
	if req.Seed != 0 {
		s.Seed = req.Seed
	}
	switch {
	case s.Temperature < 0:
		s.Temperature = 0
	case s.Temperature > 2:
		s.Temperature = 2
	}
	if s.MaxTokens < 0 {
		s.MaxTokens = 0
	}
	// Seed is an OpenAI-compatible extension; ollama's /v1 shim does not accept
	// it, so only send it where it is part of the contract.
	if pc != nil && pc.ProviderType == "ollama" {
		s.Seed = 0
	}
	return s
}

type chatPayload struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Tools       []ChatTool    `json:"tools,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Seed        *int          `json:"seed,omitempty"`
	Stream      bool          `json:"stream"`
}

func (g *LLMGateway) payload(model string, req *ChatRequest, smp sampling) *chatPayload {
	temp := smp.Temperature
	out := &chatPayload{
		Model:       model,
		Messages:    req.Messages,
		Tools:       req.Tools,
		Temperature: &temp,
		MaxTokens:   smp.MaxTokens,
		Stream:      false,
	}
	if smp.Seed != 0 {
		seed := smp.Seed
		out.Seed = &seed
	}
	return out
}

func (g *LLMGateway) do(ctx context.Context, pc *ProviderConfig, payload *chatPayload) (*ChatResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, g.timeout(pc))
	defer cancel()

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(pc.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if pc.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+pc.APIKey)
	}

	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 400 {
		return nil, &httpStatusError{code: resp.StatusCode, body: string(data)}
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode llm response: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("llm returned no choices")
	}
	msg := out.Choices[0].Message
	return &ChatResponse{
		Content:    msg.Content,
		ToolCalls:  msg.ToolCalls,
		TokenUsage: out.Usage.TotalTokens,
		Model:      out.Model,
	}, nil
}

// allow enforces the redis-backed fixed-window rate limit (per user + global).
// Without Redis the limiter degrades to unlimited.
func (g *LLMGateway) allow(ctx context.Context, userID int) error {
	if g.cache == nil {
		return nil
	}
	rdb := g.cache.Redis()
	minute := time.Now().Unix() / 60
	if userID > 0 {
		if exceeded, err := incrWithin(ctx, rdb, fmt.Sprintf("ai:rl:u:%d:%d", userID, minute), rlUserPerMin); err == nil && exceeded {
			return apperrors.New(apperrors.UnavailableError, "AI rate limit exceeded, please retry later", http.StatusTooManyRequests)
		}
	}
	if exceeded, err := incrWithin(ctx, rdb, fmt.Sprintf("ai:rl:g:%d", minute), rlGlobalPerMin); err == nil && exceeded {
		return apperrors.New(apperrors.UnavailableError, "AI rate limit exceeded, please retry later", http.StatusTooManyRequests)
	}
	return nil
}

func cacheKey(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return "ai:llm:" + hex.EncodeToString(sum[:])
}

// incrWithin increments a fixed-window counter and reports whether it exceeded
// the limit. Errors are non-fatal (the caller degrades to unlimited).
func incrWithin(ctx context.Context, rdb *redis.Client, key string, limit int) (bool, error) {
	n, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if n == 1 {
		rdb.Expire(ctx, key, 2*time.Minute)
	}
	return n > int64(limit), nil
}

// httpStatusError carries a non-2xx status from the LLM endpoint.
type httpStatusError struct {
	code int
	body string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("llm http %d: %s", e.code, truncate(e.body, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// breaker is a minimal consecutive-failure circuit breaker.
type breaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

func newBreaker() *breaker { return &breaker{} }

const (
	breakerThreshold = 5
	breakerCooldown  = 30 * time.Second
)

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !time.Now().Before(b.openUntil)
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openUntil = time.Time{}
}

func (b *breaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= breakerThreshold {
		b.openUntil = time.Now().Add(breakerCooldown)
		b.failures = 0
	}
}
