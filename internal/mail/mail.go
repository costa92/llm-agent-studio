// Package mail provides a client to send registration verification emails.
// It supports SMTP configuration from the database, falling back to env-configured values,
// and always logs and writes mock email files under `mails/` in the workspace for development.
package mail

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net/smtp"
	"os"
	"path/filepath"
	"time"

	"github.com/costa92/llm-agent-studio/internal/mailconfig"
)

type MailConfigResolver interface {
	ResolveGlobal(ctx context.Context) (mailconfig.ResolvedMailConfig, error)
}

type EnvConfig struct {
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string
}

type Client struct {
	resolver MailConfigResolver
	env      EnvConfig
	logger   *slog.Logger
	mailDir  string // mock mail output folder
}

func New(resolver MailConfigResolver, env EnvConfig, logger *slog.Logger, workspaceRoot string) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	// Mock mails will be written to `mails/` directory in the workspace root
	mailDir := filepath.Join(workspaceRoot, "mails")
	return &Client{
		resolver: resolver,
		env:      env,
		logger:   logger,
		mailDir:  mailDir,
	}
}

func (c *Client) SendVerificationCode(ctx context.Context, email, code string) error {
	subject := "AI Studio Verification Code"
	body := fmt.Sprintf("Your email verification code is: %s\n\nIt is valid for 15 minutes.", code)
	c.logger.Info("mail: sending verification email", "email", email, "code", code)
	return c.Send(ctx, email, subject, body)
}

// Send delivers a generic plain-text email: it resolves SMTP settings (DB
// preferred, env fallback), always writes the dev mock file, and sends via
// SMTP when enabled/configured. Used by the verification-code path above and
// the run-failure alert notifier.
func (c *Client) Send(ctx context.Context, email, subject, body string) error {
	// 1. Resolve configuration (DB preferred, fall back to Env)
	host := c.env.SMTPHost
	port := c.env.SMTPPort
	user := c.env.SMTPUser
	pass := c.env.SMTPPass
	from := c.env.SMTPFrom
	enabled := true

	if c.resolver != nil {
		if db, err := c.resolver.ResolveGlobal(ctx); err == nil {
			if db.Enabled && db.SMTPHost != "" {
				host = db.SMTPHost
				port = db.SMTPPort
				user = db.SMTPUser
				pass = db.SMTPPass
				from = db.SMTPFrom
				enabled = db.Enabled
				c.logger.Info("mail: using database SMTP settings", "host", host, "from", from)
			}
		}
	}

	// Q-encode the subject so non-ASCII (中文告警标题) survives the SMTP header;
	// pure-ASCII subjects pass through unchanged.
	msg := []byte("To: " + email + "\r\n" +
		"From: " + from + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		body)

	// 2. Always log and write to local file for development audit
	if err := c.writeMockMail(email, subject, body); err != nil {
		c.logger.Error("mail: failed to write mock email file", "err", err)
	}

	// 3. If enabled and SMTP host is configured, send the real email
	if enabled && host != "" {
		addr := fmt.Sprintf("%s:%d", host, port)
		var auth smtp.Auth
		if user != "" || pass != "" {
			auth = smtp.PlainAuth("", user, pass, host)
		}
		
		// Run SMTP sending in a non-blocking goroutine so registration is fast,
		// but check for immediate errors in a testable way or handle timeout.
		err := smtp.SendMail(addr, auth, from, []string{email}, msg)
		if err != nil {
			c.logger.Error("mail: failed to send SMTP mail", "addr", addr, "from", from, "to", email, "err", err)
			return fmt.Errorf("mail: SMTP send error: %w", err)
		}
		c.logger.Info("mail: SMTP mail sent successfully", "to", email)
	} else {
		c.logger.Info("mail: SMTP sending disabled or unconfigured, mock email written", "email", email)
	}

	return nil
}

func (c *Client) writeMockMail(email, subject, body string) error {
	if err := os.MkdirAll(c.mailDir, 0755); err != nil {
		return err
	}
	filename := fmt.Sprintf("%d_%s.txt", time.Now().UnixNano(), email)
	path := filepath.Join(c.mailDir, filename)
	content := fmt.Sprintf("Date: %s\nTo: %s\nSubject: %s\n\n%s", time.Now().Format(time.RFC1123Z), email, subject, body)
	return os.WriteFile(path, []byte(content), 0644)
}
