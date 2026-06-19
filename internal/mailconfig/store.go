// Package mailconfig owns mail_configs CRUD: global SMTP configuration.
// SMTP password is encrypted using secretbox.Box.
package mailconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"gorm.io/gorm"

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
	db  *gorm.DB
	box *secretbox.Box
}

func New(db *gorm.DB, box *secretbox.Box) *Store {
	return &Store{db: db, box: box}
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
	if err := s.db.WithContext(ctx).Exec(`
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
		id, in.SMTPHost, in.SMTPPort, in.SMTPUser, passEnc, in.SMTPFrom, in.Enabled, replacePass).Error; err != nil {
		return fmt.Errorf("mailconfig: upsert: %w", err)
	}
	return nil
}

func (s *Store) GetGlobal(ctx context.Context) (MailConfig, error) {
	var row mailConfigRow
	err := s.db.WithContext(ctx).Where("scope = ?", "global").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return MailConfig{}, ErrNotFound
	}
	if err != nil {
		return MailConfig{}, err
	}
	c := MailConfig{
		ID: row.ID, Scope: row.Scope, SMTPHost: row.SMTPHost, SMTPPort: row.SMTPPort,
		SMTPUser: row.SMTPUser, SMTPFrom: row.SMTPFrom, Enabled: row.Enabled,
	}
	c.HasSecret = len(row.SMTPPassEnc) > 0
	if len(row.SMTPPassEnc) > 0 && s.box != nil && s.box.Enabled() {
		dec, err := s.box.Decrypt(row.SMTPPassEnc)
		if err == nil {
			c.SMTPPass = string(dec)
		}
	}
	return c, nil
}

func (s *Store) ResolveGlobal(ctx context.Context) (ResolvedMailConfig, error) {
	var row mailConfigRow
	err := s.db.WithContext(ctx).Where("scope = ?", "global").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ResolvedMailConfig{}, ErrNotFound
	}
	if err != nil {
		return ResolvedMailConfig{}, err
	}
	var passDec string
	if len(row.SMTPPassEnc) > 0 {
		if s.box == nil || !s.box.Enabled() {
			return ResolvedMailConfig{}, ErrEncUnavailable
		}
		dec, err := s.box.Decrypt(row.SMTPPassEnc)
		if err != nil {
			return ResolvedMailConfig{}, fmt.Errorf("mailconfig: decrypt password: %w", err)
		}
		passDec = string(dec)
	}
	return ResolvedMailConfig{
		SMTPHost: row.SMTPHost, SMTPPort: row.SMTPPort, SMTPUser: row.SMTPUser,
		SMTPPass: passDec, SMTPFrom: row.SMTPFrom, Enabled: row.Enabled,
	}, nil
}
