// Package notifications delivers alert/report messages over email and Slack,
// mirroring superset/reports/notifications/*. Every channel is best effort and
// reports its outcome per recipient so the executor can record partial
// failures in report_execution_log.
package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"superset/internal/config"
	"superset/internal/models"
	"superset/internal/pkg/mail"
)

// Errors returned when a channel is not configured.
var (
	ErrSlackNotConfigured = errors.New("notifications: Slack is not configured")
	ErrEmailNotConfigured = mail.ErrSMTPNotConfigured
)

// Notification is the channel independent payload.
type Notification struct {
	Subject    string
	Text       string
	HTML       string
	Screenshot []byte
	CSV        []byte
	CSVName    string
}

// Recipient is a single normalised delivery target.
type Recipient struct {
	Type   string // "email" or "slack"
	Target string // address, channel or webhook URL
	CC     []string
	BCC    []string
}

// Channel delivers one notification to one recipient.
type Channel interface {
	Name() string
	Send(ctx context.Context, n Notification, r Recipient) error
}

// Result is the per recipient delivery outcome.
type Result struct {
	Recipient Recipient
	Channel   string
	Success   bool
	DryRun    bool
	Error     string
}

// Failed reports whether the delivery failed.
func (r Result) Failed() bool { return !r.Success && !r.DryRun }

// Dispatcher fans a notification out to every recipient.
type Dispatcher struct {
	log   *zap.Logger
	cfg   *config.Config
	email *EmailChannel
	slack *SlackChannel

	// DryRun logs deliveries instead of performing them
	// (ALERT_REPORTS_NOTIFICATION_DRY_RUN).
	DryRun bool
}

// NewDispatcher builds the channel set from configuration.
func NewDispatcher(cfg *config.Config, log *zap.Logger) *Dispatcher {
	if log == nil {
		log = zap.NewNop()
	}
	d := &Dispatcher{
		log:   log,
		cfg:   cfg,
		email: NewEmailChannel(cfg),
		slack: NewSlackChannel(cfg),
	}
	if cfg != nil {
		d.DryRun = cfg.AlertReportsNotificationDryRun
	}
	return d
}

// Email exposes the email channel (used by tests and health checks).
func (d *Dispatcher) Email() *EmailChannel { return d.email }

// Slack exposes the Slack channel.
func (d *Dispatcher) Slack() *SlackChannel { return d.slack }

// Send delivers n to every recipient. Delivery never aborts the batch: each
// outcome is collected.
func (d *Dispatcher) Send(ctx context.Context, n Notification, recipients []models.ReportRecipient) []Result {
	results := make([]Result, 0, len(recipients))
	for _, raw := range recipients {
		rc := ParseRecipient(raw)
		res := Result{Recipient: rc, Channel: rc.Type}
		if strings.TrimSpace(rc.Target) == "" {
			res.Error = "recipient target is empty"
			results = append(results, res)
			continue
		}
		var ch Channel
		switch strings.ToLower(rc.Type) {
		case "email":
			ch = d.email
		case "slack":
			ch = d.slack
		default:
			res.Error = fmt.Sprintf("unsupported recipient type %q", rc.Type)
			results = append(results, res)
			continue
		}
		res.Channel = ch.Name()

		if d.DryRun {
			d.log.Info("notification dry-run",
				zap.String("channel", ch.Name()),
				zap.String("target", rc.Target),
				zap.String("subject", n.Subject),
				zap.Int("screenshot_bytes", len(n.Screenshot)),
				zap.Int("csv_bytes", len(n.CSV)))
			res.Success = true
			res.DryRun = true
			results = append(results, res)
			continue
		}

		if err := ch.Send(ctx, n, rc); err != nil {
			res.Error = err.Error()
			d.log.Warn("notification failed",
				zap.String("channel", ch.Name()),
				zap.String("target", rc.Target),
				zap.Error(err))
			results = append(results, res)
			continue
		}
		res.Success = true
		results = append(results, res)
	}
	return results
}

// Failures renders the failed results as a single error message, or "".
func Failures(results []Result) string {
	msgs := make([]string, 0, len(results))
	for _, r := range results {
		if r.Failed() {
			msgs = append(msgs, fmt.Sprintf("%s:%s: %s", r.Channel, r.Recipient.Target, r.Error))
		}
	}
	return strings.Join(msgs, "; ")
}

// ParseRecipient normalises a stored report_recipient row.
func ParseRecipient(rc models.ReportRecipient) Recipient {
	out := Recipient{Type: strings.ToLower(strings.TrimSpace(rc.Type))}
	if out.Type == "" {
		out.Type = "email"
	}
	var cfg map[string]any
	if len(rc.RecipientConfigJSON) > 0 {
		_ = json.Unmarshal(rc.RecipientConfigJSON, &cfg)
	}
	out.Target = firstNonEmpty(cfg,
		"target", "recipient", "recipient_value", "email", "address", "value", "channel", "webhook_url", "url")
	out.CC = stringList(cfg["cc"])
	out.BCC = stringList(cfg["bcc"])
	return out
}

// firstNonEmpty returns the first non-empty string value among keys.
func firstNonEmpty(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// stringList accepts a string (comma separated) or a list of strings.
func stringList(v any) []string {
	switch t := v.(type) {
	case string:
		out := make([]string, 0, 2)
		for _, part := range strings.Split(t, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return t
	}
	return nil
}
