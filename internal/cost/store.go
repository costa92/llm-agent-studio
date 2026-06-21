// Package cost owns the generations usage ledger (spec §6 generations) + cost
// aggregation (spec §9 成本中心). Every provider call writes one row (via the
// worker after a generate); the org/project cost views aggregate them.
package cost

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Generation is one generations row (a single provider call).
type Generation struct {
	ProjectID    string
	AssetID      string
	TodoID       string
	Kind         string
	Provider     string
	Model        string
	Prompt       string
	Tokens       int
	ImageCount   int
	VideoSeconds int // MediaSeconds: 媒体时长(秒), video=帧时长 audio=音频时长 (Q3 复用同列)
	CostMicros   int64
	LatencyMS    int
}

// Aggregate is a cost rollup (spec §9 stats cards).
type Aggregate struct {
	Generations int   `json:"generations"`
	Tokens      int   `json:"tokens"`
	ImageCount  int   `json:"imageCount"`
	CostMicros  int64 `json:"costMicros"`
}

// Store persists + aggregates the generations ledger.
type Store struct{ db *gorm.DB }

// New builds a Store.
func New(db *gorm.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Record appends one generations row.
func (s *Store) Record(ctx context.Context, g Generation) error {
	kind := g.Kind
	if kind == "" {
		kind = "image"
	}
	err := s.db.WithContext(ctx).Exec(
		`INSERT INTO generations
		 (id, project_id, asset_id, todo_id, kind, provider, model, prompt, tokens, image_count, video_seconds, cost_micros, latency_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		newID(), g.ProjectID, g.AssetID, g.TodoID, kind, g.Provider, g.Model, g.Prompt,
		g.Tokens, g.ImageCount, g.VideoSeconds, g.CostMicros, g.LatencyMS).Error
	if err != nil {
		return fmt.Errorf("cost: record: %w", err)
	}
	return nil
}

func scanAgg(row interface{ Scan(...any) error }) (Aggregate, error) {
	var a Aggregate
	err := row.Scan(&a.Generations, &a.Tokens, &a.ImageCount, &a.CostMicros)
	return a, err
}

// ByProject aggregates the ledger for one project.
func (s *Store) ByProject(ctx context.Context, projectID string) (Aggregate, error) {
	a, err := scanAgg(s.db.WithContext(ctx).Raw(`
		SELECT count(*), COALESCE(sum(tokens),0), COALESCE(sum(image_count),0), COALESCE(sum(cost_micros),0)
		FROM generations WHERE project_id=$1`, projectID).Row())
	if err != nil {
		return Aggregate{}, fmt.Errorf("cost: by project: %w", err)
	}
	return a, nil
}

// ByOrg aggregates the ledger for all projects in an org (via the project join).
func (s *Store) ByOrg(ctx context.Context, orgID string) (Aggregate, error) {
	a, err := scanAgg(s.db.WithContext(ctx).Raw(`
		SELECT count(*), COALESCE(sum(g.tokens),0), COALESCE(sum(g.image_count),0), COALESCE(sum(g.cost_micros),0)
		FROM generations g JOIN projects p ON g.project_id=p.id WHERE p.org_id=$1`, orgID).Row())
	if err != nil {
		return Aggregate{}, fmt.Errorf("cost: by org: %w", err)
	}
	return a, nil
}

// Price is one pricing row: unit prices for a provider+model (spec §15 M3
// 成本中心; seeded by the m3 migration, ops-tunable via SQL).
type Price struct {
	MicrosPerImage    int64
	MicrosPer1kTokens int64
	MicrosPerSecond   int64 // M4 按秒计费 (video 帧 / audio 时长)
}

// PriceFor looks up the unit price. ok=false (no error) when the provider+model
// has no pricing row — the caller records cost_micros=0 (unpriced).
func (s *Store) PriceFor(ctx context.Context, provider, model string) (Price, bool, error) {
	var p Price
	err := s.db.WithContext(ctx).Raw(
		`SELECT micros_per_image, micros_per_1k_tokens, micros_per_second FROM pricing WHERE provider=$1 AND model=$2`,
		provider, model).Row().Scan(&p.MicrosPerImage, &p.MicrosPer1kTokens, &p.MicrosPerSecond)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Price{}, false, nil
		}
		return Price{}, false, fmt.Errorf("cost: price for %s/%s: %w", provider, model, err)
	}
	return p, true, nil
}

// ComputeCostMicros prices one generation: per-image + per-1k-token + per-second.
func ComputeCostMicros(p Price, imageCount, tokens, seconds int) int64 {
	return int64(imageCount)*p.MicrosPerImage +
		int64(tokens)*p.MicrosPer1kTokens/1000 +
		int64(seconds)*p.MicrosPerSecond
}

// RecordPriced is Record with the pricing table applied: it fills CostMicros
// from the provider+model unit price (0 when unpriced). The single costing
// chokepoint — the worker calls this after every generation (M2 carry: M2
// recorded cost_micros=0 always).
func (s *Store) RecordPriced(ctx context.Context, g Generation) error {
	if g.CostMicros == 0 {
		if p, ok, err := s.PriceFor(ctx, g.Provider, g.Model); err != nil {
			return err
		} else if ok {
			g.CostMicros = ComputeCostMicros(p, g.ImageCount, g.Tokens, g.VideoSeconds)
		}
	}
	return s.Record(ctx, g)
}

// rangeBounds normalizes optional time bounds: zero from = epoch, zero to =
// far future (now + 24h covers clock skew; rows can't be in the future).
func rangeBounds(from, to time.Time) (time.Time, time.Time) {
	if from.IsZero() {
		from = time.Unix(0, 0)
	}
	if to.IsZero() {
		to = time.Now().Add(24 * time.Hour)
	}
	return from, to
}

// ByOrgBetween aggregates the org ledger within [from, to). Zero bounds are
// open (spec §9 成本中心: 按时间聚合).
func (s *Store) ByOrgBetween(ctx context.Context, orgID string, from, to time.Time) (Aggregate, error) {
	from, to = rangeBounds(from, to)
	a, err := scanAgg(s.db.WithContext(ctx).Raw(`
		SELECT count(*), COALESCE(sum(g.tokens),0), COALESCE(sum(g.image_count),0), COALESCE(sum(g.cost_micros),0)
		FROM generations g JOIN projects p ON g.project_id=p.id
		WHERE p.org_id=$1 AND g.created_at >= $2 AND g.created_at < $3`, orgID, from, to).Row())
	if err != nil {
		return Aggregate{}, fmt.Errorf("cost: by org between: %w", err)
	}
	return a, nil
}

// ByProjectBetween aggregates one project's ledger within [from, to).
func (s *Store) ByProjectBetween(ctx context.Context, projectID string, from, to time.Time) (Aggregate, error) {
	from, to = rangeBounds(from, to)
	a, err := scanAgg(s.db.WithContext(ctx).Raw(`
		SELECT count(*), COALESCE(sum(tokens),0), COALESCE(sum(image_count),0), COALESCE(sum(cost_micros),0)
		FROM generations WHERE project_id=$1 AND created_at >= $2 AND created_at < $3`, projectID, from, to).Row())
	if err != nil {
		return Aggregate{}, fmt.Errorf("cost: by project between: %w", err)
	}
	return a, nil
}

// ProjectAggregate is a per-project rollup row (UI 按项目成本条).
type ProjectAggregate struct {
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	Aggregate
}

// PerProjectByOrg rolls the org ledger up per project, most expensive first.
func (s *Store) PerProjectByOrg(ctx context.Context, orgID string, from, to time.Time) ([]ProjectAggregate, error) {
	from, to = rangeBounds(from, to)
	rows, err := s.db.WithContext(ctx).Raw(`
		SELECT p.id, p.name, count(*), COALESCE(sum(g.tokens),0), COALESCE(sum(g.image_count),0), COALESCE(sum(g.cost_micros),0)
		FROM generations g JOIN projects p ON g.project_id=p.id
		WHERE p.org_id=$1 AND g.created_at >= $2 AND g.created_at < $3
		GROUP BY p.id, p.name
		ORDER BY COALESCE(sum(g.cost_micros),0) DESC`, orgID, from, to).Rows()
	if err != nil {
		return nil, fmt.Errorf("cost: per project: %w", err)
	}
	defer rows.Close()
	out := make([]ProjectAggregate, 0)
	for rows.Next() {
		var pa ProjectAggregate
		if err := rows.Scan(&pa.ProjectID, &pa.ProjectName, &pa.Generations, &pa.Tokens, &pa.ImageCount, &pa.CostMicros); err != nil {
			return nil, err
		}
		out = append(out, pa)
	}
	return out, rows.Err()
}

// LedgerEntry is one generations row for the usage-detail table (UI 用量明细表).
type LedgerEntry struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	ProjectName string    `json:"projectName"`
	Kind        string    `json:"kind"`
	Provider    string    `json:"provider"`
	Model       string    `json:"model"`
	Tokens      int       `json:"tokens"`
	ImageCount  int       `json:"imageCount"`
	CostMicros  int64     `json:"costMicros"`
	LatencyMS   int       `json:"latencyMs"`
	CreatedAt   time.Time `json:"createdAt"`
}

// RecentByOrg returns the org's most recent ledger entries, newest first.
func (s *Store) RecentByOrg(ctx context.Context, orgID string, limit int) ([]LedgerEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.WithContext(ctx).Raw(`
		SELECT g.id, g.project_id, p.name, g.kind, g.provider, g.model, g.tokens, g.image_count, g.cost_micros, g.latency_ms, g.created_at
		FROM generations g JOIN projects p ON g.project_id=p.id
		WHERE p.org_id=$1 ORDER BY g.created_at DESC, g.id DESC LIMIT $2`, orgID, limit).Rows()
	if err != nil {
		return nil, fmt.Errorf("cost: recent: %w", err)
	}
	defer rows.Close()
	out := make([]LedgerEntry, 0)
	for rows.Next() {
		var e LedgerEntry
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.ProjectName, &e.Kind, &e.Provider, &e.Model,
			&e.Tokens, &e.ImageCount, &e.CostMicros, &e.LatencyMS, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountByOrgSince counts the org's generations since a timestamp (rolling-24h
// quota check, spec §12 配额).
func (s *Store) CountByOrgSince(ctx context.Context, orgID string, since time.Time) (int, error) {
	var n int
	if err := s.db.WithContext(ctx).Raw(`
		SELECT count(*) FROM generations g JOIN projects p ON g.project_id=p.id
		WHERE p.org_id=$1 AND g.created_at >= $2`, orgID, since).Row().Scan(&n); err != nil {
		return 0, fmt.Errorf("cost: count since: %w", err)
	}
	return n, nil
}

// UpsertSubmittedGeneration pre-registers an async generations row at submit
// time (estimated cost). On a crash-retry the (asset_id, todo_id) conflict
// returns the existing row id and does NOT double-insert (B3 DB-enforced dedup
// via generations_asset_todo_uniq). This is the SAME mechanism as §9.3 — one
// ledger row per generation, written at submit so CountByOrgSince counts
// in-flight work (I2 quota) and poll-done updates it in place (I5 idempotent).
func (s *Store) UpsertSubmittedGeneration(ctx context.Context, g Generation) (string, error) {
	kind := g.Kind
	if kind == "" {
		kind = "video"
	}
	var id string
	err := s.db.WithContext(ctx).Raw(`
		INSERT INTO generations
		 (id, project_id, asset_id, todo_id, kind, provider, model, prompt, tokens, image_count, video_seconds, cost_micros, latency_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (asset_id, todo_id) WHERE asset_id <> '' AND todo_id <> ''
		  DO UPDATE SET id = generations.id
		RETURNING id`,
		newID(), g.ProjectID, g.AssetID, g.TodoID, kind, g.Provider, g.Model, g.Prompt,
		g.Tokens, g.ImageCount, g.VideoSeconds, g.CostMicros, g.LatencyMS).Row().Scan(&id)
	if err != nil {
		return "", fmt.Errorf("cost: upsert submitted generation: %w", err)
	}
	return id, nil
}

// UpdateGenerationByAssetTodo backfills the real seconds/cost on the pre-
// registered async row at poll-done (idempotent: re-running with the same values
// is a no-op). Locates the row by (asset_id, todo_id) — no in-memory id passing.
func (s *Store) UpdateGenerationByAssetTodo(ctx context.Context, assetID, todoID string, seconds int, costMicros int64) error {
	if err := s.db.WithContext(ctx).Exec(
		`UPDATE generations SET video_seconds=$3, cost_micros=$4 WHERE asset_id=$1 AND todo_id=$2`,
		assetID, todoID, seconds, costMicros).Error; err != nil {
		return fmt.Errorf("cost: update generation by asset/todo: %w", err)
	}
	return nil
}
