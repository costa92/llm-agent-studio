// Package mailconfig owns mail_configs CRUD: global SMTP configuration.
// SMTP password is encrypted using secretbox.Box.
package mailconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
)

// ErrEncUnavailable represents that encryption box is not configured but a password is provided.
var ErrEncUnavailable = errors.New("mailconfig: SMTP password encryption requires JWT_SECRET")
var ErrNotFound = errors.New("mailconfig: config not found")

type MailConfig struct {
	ID        string `json:"id"`
	Scope     string `json:"scope"`
	SMTPHost  string `json:"smtpHost"`
	SMTPPort  int    `json:"smtpPort"`
	SMTPUser  string `json:"smtpUser"`
	SMTPPass  string `json:"smtpPass,omitempty"`
	SMTPFrom  string `json:"smtpFrom"`
	Enabled   bool   `json:"enabled"`
	HasSecret bool   `json:"hasSecret"`
}

type ResolvedMailConfig struct {
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string
	Enabled  bool
}

type UpsertInput struct {
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPFrom string
	SMTPPass string
	Enabled  bool
}

type Store struct {
	pool *pgxpool.Pool
	box  *secretbox.Box
}

func New(pool *pgxpool.Pool, box *secretbox.Box) *Store {
	return &Store{pool: pool, box: box}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) UpsertGlobal(ctx context.Context, in UpsertInput) error {
	var passEnc []byte
	replacePass := false
	if in.SMTPPass != "" {
		if s.box == nil || !s.box.Enabled() {
			return ErrEncUnavailable
		}
		enc, err := s.box.Encrypt([]byte(in.SMTPPass))
		if err != nil {
			return fmt.Errorf("mailconfig: encrypt password: %w", err)
		}
		passEnc = enc
		replacePass = true
	}

	id := newID()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mail_configs (id, scope, smtp_host, smtp_port, smtp_user, smtp_pass_enc, smtp_from, enabled, updated_at)
		VALUES ($1, 'global', $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (scope) WHERE scope='global' DO UPDATE SET
			smtp_host = EXCLUDED.smtp_host,
			smtp_port = EXCLUDED.smtp_port,
			smtp_user = EXCLUDED.smtp_user,
			smtp_pass_enc = CASE WHEN $8 THEN EXCLUDED.smtp_pass_enc ELSE mail_configs.smtp_pass_enc END,
			smtp_from = EXCLUDED.smtp_from,
			enabled = EXCLUDED.enabled,
			updated_at = now()`,
		id, in.SMTPHost, in.SMTPPort, in.SMTPUser, passEnc, in.SMTPFrom, in.Enabled, replacePass)
	if err != nil {
		return fmt.Errorf("mailconfig: upsert: %w", err)
	}
	return nil
}

func (s *Store) GetGlobal(ctx context.Context) (MailConfig, error) {
	var c MailConfig
	var passEnc []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, scope, smtp_host, smtp_port, smtp_user, smtp_from, enabled, smtp_pass_enc
		FROM mail_configs WHERE scope='global' LIMIT 1`).
		Scan(&c.ID, &c.Scope, &c.SMTPHost, &c.SMTPPort, &c.SMTPUser, &c.SMTPFrom, &c.Enabled, &passEnc)
	if errors.Is(err, pgx.ErrNoRows) {
		return MailConfig{}, ErrNotFound
	}
	if err != nil {
		return MailConfig{}, err
	}
	c.HasSecret = len(passEnc) > 0
	if len(passEnc) > 0 && s.box != nil && s.box.Enabled() {
		dec, err := s.box.Decrypt(passEnc)
		if err == nil {
			c.SMTPPass = string(dec)
		}
	}
	return c, nil
}

func (s *Store) ResolveGlobal(ctx context.Context) (ResolvedMailConfig, error) {
	var smtpHost, smtpUser, smtpFrom string
	var smtpPort int
	var enabled bool
	var passEnc []byte
	err := s.pool.QueryRow(ctx, `
		SELECT smtp_host, smtp_port, smtp_user, smtp_from, enabled, smtp_pass_enc
		FROM mail_configs WHERE scope='global' LIMIT 1`).
		Scan(&smtpHost, &smtpPort, &smtpUser, &smtpFrom, &enabled, &passEnc)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedMailConfig{}, ErrNotFound
	}
	if err != nil {
		return ResolvedMailConfig{}, err
	}

	var passDec string
	if len(passEnc) > 0 {
		if s.box == nil || !s.box.Enabled() {
			return ResolvedMailConfig{}, ErrEncUnavailable
		}
		dec, err := s.box.Decrypt(passEnc)
		if err != nil {
			return ResolvedMailConfig{}, fmt.Errorf("mailconfig: decrypt password: %w", err)
		}
		passDec = string(dec)
	}

	return ResolvedMailConfig{
		SMTPHost: smtpHost,
		SMTPPort: smtpPort,
		SMTPUser: smtpUser,
		SMTPPass: passDec,
		SMTPFrom: smtpFrom,
		Enabled:  enabled,
	}, nil
}
