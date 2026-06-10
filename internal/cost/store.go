// Package cost owns the generations usage ledger (spec §6 generations) + cost
// aggregation (spec §9 成本中心). Every provider call writes one row (via the
// worker after a generate); the org/project cost views aggregate them.
package cost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
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
	VideoSeconds int
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
type Store struct{ pool *pgxpool.Pool }

// New builds a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

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
	_, err := s.pool.Exec(ctx,
		`INSERT INTO generations
		 (id, project_id, asset_id, todo_id, kind, provider, model, prompt, tokens, image_count, video_seconds, cost_micros, latency_ms)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		newID(), g.ProjectID, g.AssetID, g.TodoID, kind, g.Provider, g.Model, g.Prompt,
		g.Tokens, g.ImageCount, g.VideoSeconds, g.CostMicros, g.LatencyMS)
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
	a, err := scanAgg(s.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(tokens),0), COALESCE(sum(image_count),0), COALESCE(sum(cost_micros),0)
		FROM generations WHERE project_id=$1`, projectID))
	if err != nil {
		return Aggregate{}, fmt.Errorf("cost: by project: %w", err)
	}
	return a, nil
}

// ByOrg aggregates the ledger for all projects in an org (via the project join).
func (s *Store) ByOrg(ctx context.Context, orgID string) (Aggregate, error) {
	a, err := scanAgg(s.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(g.tokens),0), COALESCE(sum(g.image_count),0), COALESCE(sum(g.cost_micros),0)
		FROM generations g JOIN projects p ON g.project_id=p.id WHERE p.org_id=$1`, orgID))
	if err != nil {
		return Aggregate{}, fmt.Errorf("cost: by org: %w", err)
	}
	return a, nil
}
