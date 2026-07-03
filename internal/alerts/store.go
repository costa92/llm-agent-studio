// Package alerts owns the run-failure email alert surface: the per-org
// alert settings (org_alert_settings) and the Notifier that turns a terminal
// run failure into at most one email per run, rate-limited per org.
package alerts

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// Store is the org_alert_settings CRUD surface.
type Store struct {
	db *gorm.DB
}

// NewStore builds a Store on the shared GORM handle.
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

// Get returns the org's alert settings. An org with no row yields the zero
// default (Enabled=false, Email="") — 未配置即静默，不是错误。
func (s *Store) Get(ctx context.Context, orgID string) (Settings, error) {
	var row orgAlertSettingsRow
	err := s.db.WithContext(ctx).Where("org_id = ?", orgID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Settings{OrgID: orgID}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("alerts: get settings: %w", err)
	}
	return Settings{OrgID: row.OrgID, Email: row.Email, Enabled: row.Enabled}, nil
}

// Upsert writes the org's alert settings (one row per org, org_id 主键)。
// 写路径遵循数据层铁律：INSERT ... ON CONFLICT 逐字透传，不走 gorm.Create。
func (s *Store) Upsert(ctx context.Context, orgID string, in UpsertInput) (Settings, error) {
	if err := s.db.WithContext(ctx).Exec(`
		INSERT INTO org_alert_settings (org_id, email, enabled, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (org_id) DO UPDATE SET
			email      = EXCLUDED.email,
			enabled    = EXCLUDED.enabled,
			updated_at = now()`,
		orgID, in.Email, in.Enabled).Error; err != nil {
		return Settings{}, fmt.Errorf("alerts: upsert settings: %w", err)
	}
	return s.Get(ctx, orgID)
}
