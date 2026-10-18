// Package mail implements SMTP delivery with the standard library only.
// It mirrors the subset of superset.utils.core.send_email_smtp that the
// alert/report notifications need: plain, SSL (465) and STARTTLS (587)
// transports, AUTH PLAIN/LOGIN and MIME multipart messages with attachments.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"time"

	"superset/internal/config"
)

// ErrSMTPNotConfigured is returned by Send when no SMTP host is set. Upper
// layers degrade this into a dry-run log entry.
var ErrSMTPNotConfigured = errors.New("mail: SMTP is not configured")

// Attachment is a single MIME attachment.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is an outgoing email.
type Message struct {
	To          []string
	Subject     string
	HTMLBody    string
	TextBody    string
	Attachments []Attachment
}

// Config is the SMTP transport configuration.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	MailFrom string
	StartTLS bool
	SSL      bool
}

// Client sends messages through one SMTP configuration.
type Client struct {
	cfg Config
	// Timeout bounds the whole SMTP conversation when the context carries no
	// deadline of its own.
	Timeout time.Duration
}

// New builds a client from the application configuration.
func New(cfg config.Config) *Client {
	port := cfg.SMTPPort
	if port == 0 {
		port = 25
	}
	return &Client{
		cfg: Config{
			Host:     strings.TrimSpace(cfg.SMTPHost),
			Port:     port,
			User:     cfg.SMTPUser,
			Password: cfg.SMTPPassword,
			MailFrom: cfg.SMTPMailFrom,
			StartTLS: cfg.SMTPStartTLS,
			SSL:      cfg.SMTPSSL,
		},
		Timeout: 30 * time.Second,
	}
}

// NewWithConfig builds a client from an explicit transport configuration.
func NewWithConfig(cfg Config) *Client {
	if cfg.Port == 0 {
		cfg.Port = 25
	}
	cfg.Host = strings.TrimSpace(cfg.Host)
	return &Client{cfg: cfg, Timeout: 30 * time.Second}
}

// Configured reports whether a host is set.
func (c *Client) Configured() bool { return c.cfg.Host != "" }

// From returns the envelope sender.
func (c *Client) From() string {
	if c.cfg.MailFrom != "" {
		return c.cfg.MailFrom
	}
	return c.cfg.User
}

// Send delivers msg. It returns ErrSMTPNotConfigured when no host is set.
func (c *Client) Send(ctx context.Context, msg Message) error {
	if !c.Configured() {
		return ErrSMTPNotConfigured
	}
	to := ParseRecipients(msg.To)
	if len(to) == 0 {
		return errors.New("mail: no valid recipient addresses")
	}
	from := c.From()
	if from == "" {
		return errors.New("mail: sender address is not configured (SMTP_MAIL_FROM)")
	}
	raw := buildMessage(msg, from, to)

	addr := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	conn, err := c.dial(ctx, addr)
	if err != nil {
		return fmt.Errorf("mail: connect %s: %w", addr, err)
	}
	defer conn.Close()

	// Bound every read/write of the conversation.
	deadline := time.Now().Add(c.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	go func() {
		<-ctx.Done()
		_ = conn.SetDeadline(time.Now())
	}()

	client, err := smtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: handshake with %s: %w", addr, err)
	}
	defer client.Close()

	if err := c.startTLS(client); err != nil {
		return err
	}
	if err := c.authenticate(client); err != nil {
		return err
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mail: MAIL FROM <%s>: %w", from, err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("mail: RCPT TO <%s>: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return fmt.Errorf("mail: write message body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: finish message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("mail: QUIT: %w", err)
	}
	return nil
}

// dial opens the TCP or implicit-TLS connection.
func (c *Client) dial(ctx context.Context, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: c.Timeout}
	implicitTLS := c.cfg.SSL || c.cfg.Port == 465
	if !implicitTLS {
		return dialer.DialContext(ctx, "tcp", addr)
	}
	td := &tls.Dialer{
		NetDialer: dialer,
		Config:    &tls.Config{ServerName: c.cfg.Host, MinVersion: tls.VersionTLS12},
	}
	return td.DialContext(ctx, "tcp", addr)
}

// startTLS upgrades the plain connection when the server advertises it.
func (c *Client) startTLS(client *smtp.Client) error {
	if c.cfg.SSL || c.cfg.Port == 465 {
		return nil
	}
	ok, _ := client.Extension("STARTTLS")
	if !ok {
		if c.cfg.StartTLS || c.cfg.Port == 587 {
			// Servers on 587 that do not advertise STARTTLS cannot be used
			// safely; report it instead of silently sending credentials in
			// the clear.
			return fmt.Errorf("mail: server does not support STARTTLS")
		}
		return nil
	}
	if err := client.StartTLS(&tls.Config{ServerName: c.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
		return fmt.Errorf("mail: STARTTLS: %w", err)
	}
	return nil
}

// authenticate negotiates AUTH PLAIN or AUTH LOGIN when credentials exist and
// the server advertises one of them.
func (c *Client) authenticate(client *smtp.Client) error {
	if c.cfg.User == "" {
		return nil
	}
	ok, mechanisms := client.Extension("AUTH")
	if !ok {
		// A server without AUTH simply accepts unauthenticated relays.
		return nil
	}
	upper := strings.ToUpper(mechanisms)
	switch {
	case strings.Contains(upper, "PLAIN"):
		return client.Auth(&plainAuth{user: c.cfg.User, password: c.cfg.Password, host: c.cfg.Host})
	case strings.Contains(upper, "LOGIN"):
		return client.Auth(&loginAuth{user: c.cfg.User, password: c.cfg.Password})
	default:
		return fmt.Errorf("mail: server advertises no supported AUTH mechanism (%s)", mechanisms)
	}
}

// plainAuth implements AUTH PLAIN without net/smtp's hard requirement that the
// connection be on localhost when TLS is absent (Superset deployments often
// relay to an internal MTA).
type plainAuth struct {
	user, password, host string
}

func (a *plainAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	resp := []byte("\x00" + a.user + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("mail: unexpected AUTH PLAIN challenge")
	}
	return nil, nil
}

// loginAuth implements the legacy AUTH LOGIN challenge/response flow.
type loginAuth struct {
	user, password string
}

func (a *loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch {
	case strings.Contains(strings.ToLower(string(challenge)), "user"):
		return []byte(a.user), nil
	case strings.Contains(strings.ToLower(string(challenge)), "pass"):
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("mail: unexpected AUTH LOGIN challenge %q", string(challenge))
	}
}

// emailPattern is deliberately loose: it only rejects obvious typos.
var emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+\-']+@[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?)+$`)

// ParseRecipients validates and de-duplicates addresses. "Name <a@b.c>" is
// reduced to the bare address.
func ParseRecipients(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		addr := strings.TrimSpace(entry)
		if i := strings.LastIndex(addr, "<"); i >= 0 && strings.HasSuffix(addr, ">") {
			addr = strings.TrimSpace(addr[i+1 : len(addr)-1])
		}
		addr = strings.Trim(addr, "\"'")
		if addr == "" || !emailPattern.MatchString(addr) {
			continue
		}
		key := strings.ToLower(addr)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr)
	}
	return out
}

// ValidAddress reports whether s looks like a single email address.
func ValidAddress(s string) bool {
	return len(ParseRecipients([]string{s})) == 1
}
