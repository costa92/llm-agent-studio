// Package alerts owns the email alert surface: the per-org alert settings
// (org_alert_settings) plus two delivery paths — the event-driven Notifier that
// turns a terminal run failure into at most one email per run, and the periodic
// Evaluator that checks operational conditions (成本超阈 / 卡顿运行 / 审核积压)
// per org. Both are rate-limited/de-duped in-memory (单实例部署假设).
package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Store is the org_alert_settings CRUD surface + the read queries the Evaluator
// uses to compute operational alert conditions.
type Store struct {
	db *gorm.DB
}

// NewStore builds a Store on the shared GORM handle.
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

func rowToSettings(row orgAlertSettingsRow) Settings {
	return Settings{
		OrgID:                 row.OrgID,
		Email:                 row.Email,
		Enabled:               row.Enabled,
		BudgetEnabled:         row.BudgetEnabled,
		BudgetThresholdMicros: row.BudgetThresholdMicros,
		BudgetWindowHours:     row.BudgetWindowHours,
		StuckEnabled:          row.StuckEnabled,
		StuckThresholdMinutes: row.StuckThresholdMinutes,
		BacklogEnabled:        row.BacklogEnabled,
		BacklogThreshold:      row.BacklogThreshold,
	}
}

// Get returns the org's alert settings. An org with no row yields the zero
// default (所有开关=false, Email="") — 未配置即静默，不是错误。
func (s *Store) Get(ctx context.Context, orgID string) (Settings, error) {
	var row orgAlertSettingsRow
	err := s.db.WithContext(ctx).Where("org_id = ?", orgID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Settings{OrgID: orgID}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("alerts: get settings: %w", err)
	}
	return rowToSettings(row), nil
}

// Upsert writes the org's alert settings (one row per org, org_id 主键)。
// 写路径遵循数据层铁律：INSERT ... ON CONFLICT 逐字透传，不走 gorm.Create。
func (s *Store) Upsert(ctx context.Context, orgID string, in UpsertInput) (Settings, error) {
	if err := s.db.WithContext(ctx).Exec(`
		INSERT INTO org_alert_settings (
			org_id, email, enabled,
			budget_enabled, budget_threshold_micros, budget_window_hours,
			stuck_enabled, stuck_threshold_minutes,
			backlog_enabled, backlog_threshold,
			updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, now())
		ON CONFLICT (org_id) DO UPDATE SET
			email                   = EXCLUDED.email,
			enabled                 = EXCLUDED.enabled,
			budget_enabled          = EXCLUDED.budget_enabled,
			budget_threshold_micros = EXCLUDED.budget_threshold_micros,
			budget_window_hours     = EXCLUDED.budget_window_hours,
			stuck_enabled           = EXCLUDED.stuck_enabled,
			stuck_threshold_minutes = EXCLUDED.stuck_threshold_minutes,
			backlog_enabled         = EXCLUDED.backlog_enabled,
			backlog_threshold       = EXCLUDED.backlog_threshold,
			updated_at              = now()`,
		orgID, in.Email, in.Enabled,
		in.BudgetEnabled, in.BudgetThresholdMicros, in.BudgetWindowHours,
		in.StuckEnabled, in.StuckThresholdMinutes,
		in.BacklogEnabled, in.BacklogThreshold).Error; err != nil {
		return Settings{}, fmt.Errorf("alerts: upsert settings: %w", err)
	}
	return s.Get(ctx, orgID)
}

// ListOperational returns every org whose settings have at least one OPERATIONAL
// alert enabled AND a non-empty email — the Evaluator's per-tick work set. run
// 失败告警（enabled）不在此列（它走事件驱动的 Notifier，非周期评估）。
func (s *Store) ListOperational(ctx context.Context) ([]Settings, error) {
	var rows []orgAlertSettingsRow
	err := s.db.WithContext(ctx).
		Where("email <> '' AND (budget_enabled OR stuck_enabled OR backlog_enabled)").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("alerts: list operational: %w", err)
	}
	out := make([]Settings, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToSettings(r))
	}
	return out, nil
}

// StuckRun 是一次卡顿运行：某 plan 仍有未完结的 todo，但整条运行已连续
// StuckThresholdMinutes 无任何进展（其 todo 的 max(updated_at) 太旧）。
type StuckRun struct {
	ProjectID   string
	ProjectName string
	PlanID      string
	StuckMinutes int // 已卡顿分钟数（整数向下取整）
}

// StuckRuns lists the org's stuck runs: plans (未软删项目) with at least one
// non-terminal todo whose newest todo update is older than olderThan. 终态
// todo = done/failed/canceled；全部终态的运行已结束、不算卡顿。按卡顿时长倒序，
// 上限 limit（邮件只列样本 + 总数）。
func (s *Store) StuckRuns(ctx context.Context, orgID string, olderThan time.Duration, limit int) ([]StuckRun, error) {
	if limit <= 0 {
		limit = 20
	}
	mins := int(olderThan / time.Minute)
	rows, err := s.db.WithContext(ctx).Raw(`
		SELECT pl.project_id, pr.name, pl.id,
		       floor(EXTRACT(EPOCH FROM (now() - max(t.updated_at))) / 60)::int
		FROM plans pl
		JOIN projects pr ON pr.id = pl.project_id
		JOIN todos t ON t.plan_id = pl.id
		WHERE pr.org_id = $1 AND pr.deleted_at IS NULL
		GROUP BY pl.id, pl.project_id, pr.name
		HAVING count(*) FILTER (WHERE t.status NOT IN ('done','failed','canceled')) > 0
		   AND max(t.updated_at) < now() - make_interval(mins => $2)
		ORDER BY max(t.updated_at) ASC
		LIMIT $3`, orgID, mins, limit).Rows()
	if err != nil {
		return nil, fmt.Errorf("alerts: stuck runs: %w", err)
	}
	defer rows.Close()
	out := make([]StuckRun, 0)
	for rows.Next() {
		var r StuckRun
		if err := rows.Scan(&r.ProjectID, &r.ProjectName, &r.PlanID, &r.StuckMinutes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PendingAcceptanceCount counts the org's assets still awaiting human review
// (status='pending_acceptance'，未软删项目) — 审核积压告警的度量。
func (s *Store) PendingAcceptanceCount(ctx context.Context, orgID string) (int, error) {
	var n int
	if err := s.db.WithContext(ctx).Raw(`
		SELECT count(*) FROM assets a
		JOIN projects p ON p.id = a.project_id
		WHERE p.org_id = $1 AND p.deleted_at IS NULL AND a.status = 'pending_acceptance'`,
		orgID).Row().Scan(&n); err != nil {
		return 0, fmt.Errorf("alerts: pending acceptance count: %w", err)
	}
	return n, nil
}
