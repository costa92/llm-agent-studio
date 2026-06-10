// Package storage owns the pgxpool and the studio M1 table migrations.
// Unlike llm-agent-kb there is no pgvector and no rag store (M1 is text-only),
// so Open is simpler: just a pool + idempotent business migrations.
package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config configures Open.
type Config struct {
	PGURL string
}

// Storage holds the pgxpool.
type Storage struct {
	pool *pgxpool.Pool
}

// Open builds the pool. The caller owns Close.
func Open(ctx context.Context, cfg Config) (*Storage, error) {
	if cfg.PGURL == "" {
		return nil, fmt.Errorf("storage: PGURL is required")
	}
	pool, err := pgxpool.New(ctx, cfg.PGURL)
	if err != nil {
		return nil, fmt.Errorf("storage: new pool: %w", err)
	}
	return &Storage{pool: pool}, nil
}

// Pool returns the underlying pgxpool.
func (s *Storage) Pool() *pgxpool.Pool { return s.pool }

// Close releases the pool.
func (s *Storage) Close() { s.pool.Close() }

// m1Migrations are the studio M1 tables (spec §6). All idempotent.
var m1Migrations = []string{
	`CREATE TABLE IF NOT EXISTS projects (
		id              TEXT PRIMARY KEY,
		org_id          TEXT NOT NULL,
		name            TEXT NOT NULL,
		description     TEXT NOT NULL DEFAULT '',
		content_type    TEXT NOT NULL DEFAULT '',
		target_platform TEXT NOT NULL DEFAULT '',
		style           TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'draft',
		created_by      TEXT NOT NULL,
		created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS projects_org_idx ON projects (org_id)`,
	`CREATE TABLE IF NOT EXISTS plans (
		id            TEXT PRIMARY KEY,
		project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		status        TEXT NOT NULL DEFAULT 'created',
		raw_plan_json JSONB,
		valid         BOOLEAN NOT NULL DEFAULT false,
		fallback_used BOOLEAN NOT NULL DEFAULT false,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS plans_project_idx ON plans (project_id)`,
	`CREATE TABLE IF NOT EXISTS todos (
		id           TEXT PRIMARY KEY,
		project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		plan_id      TEXT NOT NULL,
		type         TEXT NOT NULL,
		status       TEXT NOT NULL DEFAULT 'pending',
		agent        TEXT NOT NULL DEFAULT '',
		skill        TEXT NOT NULL DEFAULT '',
		depends_on   TEXT[] NOT NULL DEFAULT '{}',
		input_json   JSONB,
		output_ref   TEXT NOT NULL DEFAULT '',
		error        TEXT NOT NULL DEFAULT '',
		attempts     INT  NOT NULL DEFAULT 0,
		locked_by    TEXT NOT NULL DEFAULT '',
		locked_until TIMESTAMPTZ,
		next_run_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
		created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS todos_project_idx ON todos (project_id)`,
	`CREATE INDEX IF NOT EXISTS todos_claim_idx ON todos (status, next_run_at)`,
	`CREATE TABLE IF NOT EXISTS scripts (
		id           TEXT PRIMARY KEY,
		project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		todo_id      TEXT NOT NULL,
		content_json JSONB NOT NULL,
		version      INT  NOT NULL DEFAULT 1,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS scripts_project_idx ON scripts (project_id)`,
	`CREATE TABLE IF NOT EXISTS shots (
		id         TEXT PRIMARY KEY,
		project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		script_id  TEXT NOT NULL,
		todo_id    TEXT NOT NULL,
		shot_no    INT  NOT NULL,
		camera     TEXT NOT NULL DEFAULT '',
		scene      TEXT NOT NULL DEFAULT '',
		action     TEXT NOT NULL DEFAULT '',
		prompt     TEXT NOT NULL DEFAULT '',
		duration   INT  NOT NULL DEFAULT 0,
		ordering   INT  NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS shots_project_idx ON shots (project_id)`,
	`CREATE TABLE IF NOT EXISTS run_events (
		project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		seq        BIGSERIAL PRIMARY KEY,
		kind       TEXT NOT NULL,
		todo_id    TEXT NOT NULL DEFAULT '',
		payload    JSONB,
		ts         TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS run_events_project_idx ON run_events (project_id, seq)`,
}

// Migrate applies the M1 migrations. Idempotent.
func (s *Storage) Migrate(ctx context.Context) error {
	for _, stmt := range m1Migrations {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("storage: migrate: %w", err)
		}
	}
	return nil
}
