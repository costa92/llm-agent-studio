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
	// M5.1 增量：per-project 规划模型 override。空串 = 走 org 默认；
	// 非空时 runHandler 经 modelrouter.ChatModelForNamed 查 model_configs
	// 拿对应 provider/model 的 key 来组装 chat model，planner 拿到后用这个。
	// ADD COLUMN IF NOT EXISTS 让旧库升上来也不报错。
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS planner_provider TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS planner_model TEXT NOT NULL DEFAULT ''`,
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

// m6Migrations 建 storage_configs (per-org / global 对象存储配置)。secret 半段静态
// 加密 (secret_enc BYTEA)，与 BYOK 同一把 box。两条 partial unique index 保证
// global 唯一 + 每 org 唯一。additive only。
var m6Migrations = []string{
	`CREATE TABLE IF NOT EXISTS storage_configs (
		id TEXT PRIMARY KEY,
		scope TEXT NOT NULL,
		org_id TEXT NOT NULL DEFAULT '',
		mode TEXT NOT NULL,
		endpoint TEXT NOT NULL DEFAULT '',
		region TEXT NOT NULL DEFAULT '',
		bucket TEXT NOT NULL DEFAULT '',
		access_key_id TEXT NOT NULL DEFAULT '',
		secret_enc BYTEA,
		use_ssl BOOLEAN NOT NULL DEFAULT true,
		public_prefix TEXT NOT NULL DEFAULT '',
		enabled BOOLEAN NOT NULL DEFAULT true,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS storage_configs_global_uniq ON storage_configs (scope) WHERE scope='global'`,
	`SELECT 1`,
}

// m7Migrations 建 mail_configs (global SMTP 邮件发送配置)。secret_enc BYTEA
// 存放加密后的 SMTP 密码，scope='global' 局部唯一索引保证系统仅一份全局设置。
var m7Migrations = []string{
	`CREATE TABLE IF NOT EXISTS mail_configs (
		id TEXT PRIMARY KEY,
		scope TEXT NOT NULL,
		smtp_host TEXT NOT NULL DEFAULT '',
		smtp_port INT NOT NULL DEFAULT 587,
		smtp_user TEXT NOT NULL DEFAULT '',
		smtp_pass_enc BYTEA,
		smtp_from TEXT NOT NULL DEFAULT '',
		enabled BOOLEAN NOT NULL DEFAULT true,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS mail_configs_global_uniq ON mail_configs (scope) WHERE scope='global'`,
}

// m8Migrations 建 prompts 表 (提示词管理)。
var m8Migrations = []string{
	`CREATE TABLE IF NOT EXISTS prompts (
		id TEXT PRIMARY KEY,
		org_id TEXT NOT NULL,
		name TEXT NOT NULL,
		content TEXT NOT NULL,
		style TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS prompts_org_idx ON prompts (org_id)`,
}

// m9Migrations are the studio M9 migrations (adding image_provider and image_model to projects).
var m9Migrations = []string{
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS image_provider TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS image_model TEXT NOT NULL DEFAULT ''`,
}

// m10Migrations are the studio M10 migrations (adding storage_mode and updating storage_configs unique constraints).
var m10Migrations = []string{
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS storage_mode TEXT NOT NULL DEFAULT ''`,
	`DROP INDEX IF EXISTS storage_configs_org_uniq`,
	`CREATE UNIQUE INDEX IF NOT EXISTS storage_configs_org_mode_uniq ON storage_configs (org_id, mode) WHERE scope='org'`,
}

// m11Migrations add custom workflow support.
var m11Migrations = []string{
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS custom_workflow_enabled BOOLEAN NOT NULL DEFAULT false`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS workflow_nodes JSONB`,
}

// m12Migrations promote custom workflows to a first-class 1:N model: a project
// can own MANY named workflows, and each workflow is an independent execution
// unit. A run (plans row) records WHICH workflow it came from via workflow_id
// (NULL = legacy/LLM-planner run). run_events also carries workflow_id so a
// workflow's run timeline can be isolated. The m11 single-embedded workflow
// (projects.custom_workflow_enabled + workflow_nodes) is backfilled into one
// workflows row per project; the legacy columns are kept (not dropped).
var m12Migrations = []string{
	`CREATE TABLE IF NOT EXISTS workflows (
		id          TEXT PRIMARY KEY,
		project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		name        TEXT NOT NULL,
		nodes       JSONB NOT NULL DEFAULT '[]',
		created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS workflows_project_idx ON workflows (project_id, created_at)`,
	`ALTER TABLE plans      ADD COLUMN IF NOT EXISTS workflow_id TEXT REFERENCES workflows(id) ON DELETE SET NULL`,
	`ALTER TABLE run_events ADD COLUMN IF NOT EXISTS workflow_id TEXT NOT NULL DEFAULT ''`,
	// Backfill the m11 embedded workflow into a first-class row. Guarded by
	// NOT EXISTS so re-running migrations never duplicates. md5(random()||clock)
	// yields a 32-hex id matching the app's newID() format.
	`INSERT INTO workflows (id, project_id, name, nodes)
	 SELECT md5(random()::text || clock_timestamp()::text), p.id, '默认工作流', p.workflow_nodes
	 FROM projects p
	 WHERE p.custom_workflow_enabled = true
	   AND p.workflow_nodes IS NOT NULL
	   AND p.workflow_nodes::text NOT IN ('', 'null', '[]')
	   AND NOT EXISTS (SELECT 1 FROM workflows w WHERE w.project_id = p.id)`,
}

// m13Migrations add per-kind typing + per-(org,kind) default to prompts.
var m13Migrations = []string{
	`ALTER TABLE prompts ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE prompts ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false`,
	`CREATE UNIQUE INDEX IF NOT EXISTS prompts_org_kind_default_uniq ON prompts (org_id, kind) WHERE is_default`,
}

// m14Migrations add the per-project cover image link (cover_asset_id → an assets
// row reused; ” = no cover). additive only.
var m14Migrations = []string{
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS cover_asset_id TEXT NOT NULL DEFAULT ''`,
}

// m15Migrations record per-asset storage backend identity so the serve path
// reads bytes from the backend they were WRITTEN to, not the project's CURRENT
// storage_mode (which breaks historical assets after a storage switch). The
// backfill is run-once / idempotent (guarded on storage_config_id=”).
var m15Migrations = []string{
	`ALTER TABLE assets ADD COLUMN IF NOT EXISTS storage_config_id TEXT NOT NULL DEFAULT ''`,
	// Backfill configured-backend rows FIRST: match each asset's project (org,mode)
	// to its enabled storage_configs row and stamp that config id. Correct ONLY for
	// orgs that have NOT switched mode since the asset was written — the only
	// recoverable case (we cannot know a switched org's historical backend).
	`UPDATE assets a SET storage_config_id = sc.id
	 FROM projects p, storage_configs sc
	 WHERE a.project_id = p.id AND sc.org_id = p.org_id
	   AND sc.mode = p.storage_mode AND sc.enabled = true
	   AND a.storage_config_id = ''`,
	// Builtin sweep SECOND: every remaining unstamped row (projects on builtin
	// default localfs / no matching config) gets the "builtin" sentinel. Order
	// matters — configured rows must be claimed before this catch-all.
	`UPDATE assets SET storage_config_id = 'builtin' WHERE storage_config_id = ''`,
}

// m16Migrations: org 存储多配置(去 org×mode 唯一约束 + name/is_default + projects 覆盖列)。
var m16Migrations = []string{
	`ALTER TABLE storage_configs ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE storage_configs ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT false`,
	`DROP INDEX IF EXISTS storage_configs_org_mode_uniq`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS storage_config_id TEXT NOT NULL DEFAULT ''`,
	`CREATE UNIQUE INDEX IF NOT EXISTS storage_configs_one_org_default
	 ON storage_configs (org_id) WHERE scope='org' AND is_default=true`,
	`UPDATE storage_configs sc SET is_default=true
	 WHERE sc.scope='org' AND sc.enabled=true
	   AND sc.id = (SELECT id FROM storage_configs x
	                WHERE x.scope='org' AND x.org_id=sc.org_id AND x.enabled=true
	                ORDER BY created_at ASC LIMIT 1)
	   AND NOT EXISTS (SELECT 1 FROM storage_configs y
	                   WHERE y.scope='org' AND y.org_id=sc.org_id AND y.is_default=true)`,
	`UPDATE storage_configs SET name=mode WHERE name=''`,
}

// m17Migrations: 儿童绘本——projects 加 kind('standard'/'picturebook') + picturebook_config(生成参数 JSON,见 project.PictureBookConfig)。
var m17Migrations = []string{
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'standard'`,
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS picturebook_config TEXT NOT NULL DEFAULT ''`,
}

// Migrate applies the M1 + M2 + M3 + M4 + M5 + M6 + M7 + M8 + M9 + M10 + M11 + M12 + M13 + M14 + M15 + M16 + M17 migrations in order. Idempotent.
func (s *Storage) Migrate(ctx context.Context) error {
	all := append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append([]string{},
		m1Migrations...), m2Migrations...), m3Migrations...), m4Migrations...), m5Migrations...), m6Migrations...), m7Migrations...), m8Migrations...), m9Migrations...), m10Migrations...), m11Migrations...), m12Migrations...), m13Migrations...), m14Migrations...), m15Migrations...), m16Migrations...), m17Migrations...)
	for _, stmt := range all {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("storage: migrate: %w", err)
		}
	}
	return nil
}
