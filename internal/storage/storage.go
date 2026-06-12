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

// m2Migrations are the studio M2 tables (spec §6: assets/generations/
// model_configs). All idempotent; do NOT alter M1 tables destructively.
var m2Migrations = []string{
	`CREATE TABLE IF NOT EXISTS assets (
		id              TEXT PRIMARY KEY,
		project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		shot_id         TEXT NOT NULL DEFAULT '',
		todo_id         TEXT NOT NULL DEFAULT '',
		type            TEXT NOT NULL DEFAULT 'image',
		blob_key        TEXT NOT NULL DEFAULT '',
		url             TEXT NOT NULL DEFAULT '',
		prompt          TEXT NOT NULL DEFAULT '',
		style           TEXT NOT NULL DEFAULT '',
		provider        TEXT NOT NULL DEFAULT '',
		model           TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'generating',
		version         INT  NOT NULL DEFAULT 1,
		parent_asset_id TEXT NOT NULL DEFAULT '',
		tags            TEXT[] NOT NULL DEFAULT '{}',
		created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS assets_project_idx ON assets (project_id)`,
	`CREATE INDEX IF NOT EXISTS assets_status_idx ON assets (status)`,
	`CREATE INDEX IF NOT EXISTS assets_tags_gin ON assets USING GIN (tags)`,
	`CREATE TABLE IF NOT EXISTS generations (
		id            TEXT PRIMARY KEY,
		project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		asset_id      TEXT NOT NULL DEFAULT '',
		todo_id       TEXT NOT NULL DEFAULT '',
		kind          TEXT NOT NULL DEFAULT 'image',
		provider      TEXT NOT NULL DEFAULT '',
		model         TEXT NOT NULL DEFAULT '',
		prompt        TEXT NOT NULL DEFAULT '',
		tokens        INT  NOT NULL DEFAULT 0,
		image_count   INT  NOT NULL DEFAULT 0,
		video_seconds INT  NOT NULL DEFAULT 0,
		cost_micros   BIGINT NOT NULL DEFAULT 0,
		latency_ms    INT  NOT NULL DEFAULT 0,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS generations_project_idx ON generations (project_id, created_at)`,
	`CREATE TABLE IF NOT EXISTS model_configs (
		id          TEXT PRIMARY KEY,
		org_id      TEXT NOT NULL,
		kind        TEXT NOT NULL DEFAULT 'image',
		provider    TEXT NOT NULL,
		model       TEXT NOT NULL,
		enabled     BOOLEAN NOT NULL DEFAULT true,
		is_default  BOOLEAN NOT NULL DEFAULT false,
		params_json JSONB,
		created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS model_configs_org_idx ON model_configs (org_id)`,
}

// m3Migrations are the studio M3 surfaces (spec §15 M3): the pricing table
// (provider+model unit prices feeding cost_micros — seeded, ops-tunable via
// SQL, no admin CRUD by decision) and the assets prescreen columns written by
// the ReviewAgent (advisory; HITL stays the hard gate). All idempotent.
var m3Migrations = []string{
	`ALTER TABLE assets ADD COLUMN IF NOT EXISTS prescreen_score INT NOT NULL DEFAULT -1`,
	`ALTER TABLE assets ADD COLUMN IF NOT EXISTS prescreen_flags TEXT[] NOT NULL DEFAULT '{}'`,
	`ALTER TABLE assets ADD COLUMN IF NOT EXISTS prescreen_note TEXT NOT NULL DEFAULT ''`,
	`CREATE TABLE IF NOT EXISTS pricing (
		provider             TEXT NOT NULL,
		model                TEXT NOT NULL,
		kind                 TEXT NOT NULL DEFAULT 'image',
		micros_per_image     BIGINT NOT NULL DEFAULT 0,
		micros_per_1k_tokens BIGINT NOT NULL DEFAULT 0,
		PRIMARY KEY (provider, model)
	)`,
	`INSERT INTO pricing (provider, model, kind, micros_per_image, micros_per_1k_tokens) VALUES
		('openai',     'gpt-image-1',             'image', 40000, 10000),
		('openai',     'dall-e-3',                'image', 40000, 0),
		('google',     'imagen-3.0-generate-002', 'image', 30000, 0),
		('minimax',    'image-01',                'image', 20000, 0),
		('volcengine', 'doubao-seedream-3-0-t2i', 'image', 20000, 0)
	 ON CONFLICT (provider, model) DO NOTHING`,
}

// m4Migrations are the studio M4 surfaces (spec §6 二期异步引擎): the async poll
// budget column, the external-job handle + submit timestamp, the crash-idempotency
// partial unique indexes (B1 assets_todo_uniq / B3 generations_asset_todo_uniq),
// the per-second pricing dimension, and video/audio catalog seed prices. All
// idempotent, additive only — never alters M1-M3 columns destructively.
var m4Migrations = []string{
	`ALTER TABLE todos  ADD COLUMN IF NOT EXISTS poll_attempts INT NOT NULL DEFAULT 0`,
	`ALTER TABLE assets ADD COLUMN IF NOT EXISTS external_job_id TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE assets ADD COLUMN IF NOT EXISTS submitted_at TIMESTAMPTZ`,
	// Reverse lookup by external job (cancel/reconcile); partial avoids '' bloat.
	`CREATE INDEX IF NOT EXISTS assets_extjob_idx ON assets (external_job_id) WHERE external_job_id <> ''`,
	// B1: one asset row per todo (crash reclaim reuses the row, never re-creates).
	// Partial so the many regenerate/image rows with todo_id='' don't collide.
	`CREATE UNIQUE INDEX IF NOT EXISTS assets_todo_uniq ON assets (todo_id) WHERE todo_id <> ''`,
	// B3: async ledger dedup is a DB invariant (submit-insert / poll-update hit the
	// same row); partial excludes the empty keys image's sync path may carry.
	`CREATE UNIQUE INDEX IF NOT EXISTS generations_asset_todo_uniq ON generations (asset_id, todo_id)
	  WHERE asset_id <> '' AND todo_id <> ''`,
	// Per-second pricing dimension (video frame seconds / audio seconds, Q3).
	`ALTER TABLE pricing ADD COLUMN IF NOT EXISTS micros_per_second BIGINT NOT NULL DEFAULT 0`,
	// Video/audio prices: fake (live-verification ledger assertions) + real models
	// (placeholders, ops-tunable via SQL). Order-of-magnitude, NOT a quote.
	`INSERT INTO pricing (provider, model, kind, micros_per_second) VALUES
		('fake',       'fake-video-async', 'video', 500000),
		('fake',       'fake-audio-async', 'audio',  50000),
		('runway',     'gen-3',            'video', 500000),
		('kling',      'kling-v1',         'video', 280000),
		('google',     'veo-2',            'video', 500000),
		('openai',     'tts-1',            'audio',  15000)
	 ON CONFLICT (provider, model) DO NOTHING`,
}

// m5Migrations are the BYOK per-config credential columns on model_configs
// (base_url + 静态加密的 api_key_enc)。additive only — 不破坏既有 model_configs。
var m5Migrations = []string{
	`ALTER TABLE model_configs ADD COLUMN IF NOT EXISTS base_url TEXT`,
	`ALTER TABLE model_configs ADD COLUMN IF NOT EXISTS api_key_enc BYTEA`,
}

// Migrate applies the M1 + M2 + M3 + M4 + M5 migrations in order. Idempotent.
func (s *Storage) Migrate(ctx context.Context) error {
	all := append(append(append(append(append([]string{},
		m1Migrations...), m2Migrations...), m3Migrations...), m4Migrations...), m5Migrations...)
	for _, stmt := range all {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("storage: migrate: %w", err)
		}
	}
	return nil
}
