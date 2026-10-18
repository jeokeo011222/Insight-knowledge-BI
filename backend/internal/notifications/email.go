package notifications

import (
	"context"
	"strings"

	"superset/internal/config"
	"superset/internal/pkg/mail"
)

// EmailChannel delivers notifications through SMTP.
type EmailChannel struct {
	client *mail.Client
	from   string
}

// NewEmailChannel builds the SMTP channel.
func NewEmailChannel(cfg *config.Config) *EmailChannel {
	if cfg == nil {
		return &EmailChannel{client: mail.NewWithConfig(mail.Config{})}
	}
	c := mail.New(*cfg)
	return &EmailChannel{client: c, from: c.From()}
}

// Name implements Channel.
func (c *EmailChannel) Name() string { return "email" }

// Configured reports whether SMTP is usable.
func (c *EmailChannel) Configured() bool { return c.client != nil && c.client.Configured() }

// Send implements Channel. The screenshot and CSV ride along as attachments,
// mirroring superset's EmailNotification.
func (c *EmailChannel) Send(ctx context.Context, n Notification, r Recipient) error {
	if c.client == nil || !c.client.Configured() {
		return ErrEmailNotConfigured
	}
	to := append([]string{r.Target}, r.CC...)
	to = append(to, r.BCC...)

	msg := mail.Message{
		To:       to,
		Subject:  n.Subject,
		HTMLBody: n.HTML,
		TextBody: n.Text,
	}
	if len(n.Screenshot) > 0 {
		msg.Attachments = append(msg.Attachments, mail.Attachment{
			Filename:    "screenshot.png",
			ContentType: "image/png",
			Data:        n.Screenshot,
		})
	}
	if len(n.CSV) > 0 {
		name := n.CSVName
		if strings.TrimSpace(name) == "" {
			name = "report.csv"
		}
		msg.Attachments = append(msg.Attachments, mail.Attachment{
			Filename:    name,
			ContentType: "text/csv; charset=UTF-8",
			Data:        n.CSV,
		})
	}
	return c.client.Send(ctx, msg)
}
