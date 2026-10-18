package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"superset/internal/config"
)

// slackTimeout bounds every Slack HTTP call.
const slackTimeout = 15 * time.Second

// slackMaxErrorBody is how much of a failing response body is preserved.
const slackMaxErrorBody = 512

// SlackChannel posts notifications either through the Web API (when a bot
// token is configured) or through an incoming webhook URL.
type SlackChannel struct {
	token  string
	client *http.Client
}

// NewSlackChannel builds the Slack channel.
func NewSlackChannel(cfg *config.Config) *SlackChannel {
	token := ""
	if cfg != nil {
		token = strings.TrimSpace(cfg.SlackAPIToken)
	}
	return &SlackChannel{token: token, client: &http.Client{Timeout: slackTimeout}}
}

// Name implements Channel.
func (c *SlackChannel) Name() string { return "slack" }

// Configured reports whether a bot token exists.
func (c *SlackChannel) Configured() bool { return c.token != "" }

// Send implements Channel. With a token the recipient target is a channel id
// or name; without one it must be an incoming webhook URL.
func (c *SlackChannel) Send(ctx context.Context, n Notification, r Recipient) error {
	text := n.Text
	if strings.TrimSpace(text) == "" {
		text = n.Subject
	}
	blocks := slackBlocks(n)

	if c.token != "" {
		if strings.TrimSpace(r.Target) == "" {
			return fmt.Errorf("slack: channel is required when using SLACK_API_TOKEN")
		}
		payload := map[string]any{
			"channel": r.Target,
			"text":    text,
			"blocks":  blocks,
		}
		return c.post(ctx, "https://slack.com/api/chat.postMessage", payload, true)
	}

	target := strings.TrimSpace(r.Target)
	if target == "" {
		return ErrSlackNotConfigured
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		return ErrSlackNotConfigured
	}
	payload := map[string]any{"text": text, "blocks": blocks}
	return c.post(ctx, target, payload, false)
}

// slackBlocks renders the notification as Slack Block Kit sections.
func slackBlocks(n Notification) []map[string]any {
	blocks := make([]map[string]any, 0, 3)
	header := strings.TrimSpace(n.Subject)
	if header == "" {
		header = "Superset notification"
	}
	blocks = append(blocks, map[string]any{
		"type": "header",
		"text": map[string]any{"type": "plain_text", "text": truncate(header, 150)},
	})
	body := strings.TrimSpace(n.Text)
	if body == "" {
		body = strings.TrimSpace(n.HTML)
	}
	if body != "" {
		blocks = append(blocks, map[string]any{
			"type": "section",
			"text": map[string]any{"type": "mrkdwn", "text": truncate(body, 2900)},
		})
	}
	if len(n.CSV) > 0 || len(n.Screenshot) > 0 {
		parts := make([]string, 0, 2)
		if len(n.Screenshot) > 0 {
			parts = append(parts, fmt.Sprintf("_screenshot: %d bytes_", len(n.Screenshot)))
		}
		if len(n.CSV) > 0 {
			parts = append(parts, fmt.Sprintf("_csv: %s (%d bytes)_", n.CSVName, len(n.CSV)))
		}
		blocks = append(blocks, map[string]any{
			"type": "context",
			"elements": []map[string]any{
				{"type": "mrkdwn", "text": strings.Join(parts, " · ")},
			},
		})
	}
	return blocks
}

// post performs the JSON request and validates the Slack response envelope.
func (c *SlackChannel) post(ctx context.Context, url string, payload any, apiCall bool) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack: encode payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if apiCall {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack: post %s: %w", url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack: %s returned %d: %s", url, resp.StatusCode, truncate(string(raw), slackMaxErrorBody))
	}
	if apiCall {
		var envelope struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("slack: decode response: %w (body: %s)", err, truncate(string(raw), slackMaxErrorBody))
		}
		if !envelope.OK {
			return fmt.Errorf("slack: chat.postMessage failed: %s", truncate(envelope.Error, slackMaxErrorBody))
		}
	}
	return nil
}

// truncate clamps a string to at most n runes, appending an ellipsis.
func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n]) + "..."
}
