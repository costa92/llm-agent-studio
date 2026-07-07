// Package storage owns the pgxpool and the studio M1 table migrations.
// Unlike llm-agent-kb there is no pgvector and no rag store (M1 is text-only),
// so Open is simpler: just a pool + idempotent business migrations.
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Config configures Open.
type Config struct {
	PGURL string
}

// Storage holds the pgxpool plus a coexisting GORM handle (same DSN).
type Storage struct {
	pool   *pgxpool.Pool
	gormDB *gorm.DB

	// testGoSteps, when non-nil, overrides the production Go-step registry
	// (goSteps) so tests can exercise the version-tracked migration runner
	// without depending on a real registered step.
	testGoSteps []migrationStep
}

// Open builds the pool AND a coexisting *gorm.DB (pgx stdlib driver) to the same
// DB. Migrated packages take the GORM handle; un-migrated ones keep the pool.
func Open(ctx context.Context, cfg Config) (*Storage, error) {
	if cfg.PGURL == "" {
		return nil, fmt.Errorf("storage: PGURL is required")
	}
	pool, err := pgxpool.New(ctx, cfg.PGURL)
	if err != nil {
		return nil, fmt.Errorf("storage: new pool: %w", err)
	}
	gormDB, err := gorm.Open(postgres.Open(cfg.PGURL), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: open gorm: %w", err)
	}
	// Bound the GORM (database/sql) pool — it is now the PRIMARY connection pool
	// (nearly every package uses the GORM handle; only the external authz lib still
	// holds the pgxpool). database/sql defaults to UNLIMITED open conns, which
	// together with the coexisting pgxpool can exhaust Postgres' default
	// max_connections (100). Conservative caps + recycling keep total connections
	// well under that ceiling and drop stale conns behind an LB / server idle timeout.
	sqlDB, err := gormDB.DB()
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("storage: gorm sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(time.Hour)
	sqlDB.SetConnMaxIdleTime(30 * time.Minute)
	return &Storage{pool: pool, gormDB: gormDB}, nil
}

// Pool returns the underlying pgxpool.
func (s *Storage) Pool() *pgxpool.Pool { return s.pool }

// GORM returns the coexisting *gorm.DB (same DB as Pool).
func (s *Storage) GORM() *gorm.DB { return s.gormDB }

// Close releases both the pool and the GORM sql.DB.
func (s *Storage) Close() {
	if s.gormDB != nil {
		if sqlDB, err := s.gormDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	s.pool.Close()
}

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

// m17Migrations: projects 加 kind 列。历史上还加过 picturebook_config，但绘本管线
// 已移除——该列的 ADD 从这条无条件 DDL 中删除，并由版本化步骤 m23 DROP，避免每次
// 启动被 IF NOT EXISTS 重新加回。kind 列保留（现存值经 m23 收敛为 'custom'）。
var m17Migrations = []string{
	`ALTER TABLE projects ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'standard'`,
}

// m18Migrations 建 custom_node_types (组织级 typed 自定义节点注册表) + node_outputs
// (通用产物表，custom 执行结果落地，下游变量读取 + 运行视图消费)。
// custom_node_types: 唯一索引 (org_id, slug)；编辑只改 label/color/params，不改 slug/kind。
// node_outputs: project scope；content 存文本或 JSON，format 标注。additive only。
var m18Migrations = []string{
	`CREATE TABLE IF NOT EXISTS custom_node_types (
		id TEXT PRIMARY KEY,
		org_id TEXT NOT NULL,
		slug TEXT NOT NULL,
		label TEXT NOT NULL,
		color TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL,
		params JSONB NOT NULL DEFAULT '{}',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS custom_node_types_org_slug_uniq ON custom_node_types (org_id, slug)`,
	`CREATE TABLE IF NOT EXISTS node_outputs (
		id TEXT PRIMARY KEY,
		project_id TEXT NOT NULL,
		todo_id TEXT NOT NULL,
		type TEXT NOT NULL,
		content TEXT NOT NULL DEFAULT '',
		format TEXT NOT NULL DEFAULT 'text',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE INDEX IF NOT EXISTS node_outputs_todo_idx ON node_outputs (todo_id)`,
	`CREATE INDEX IF NOT EXISTS node_outputs_project_idx ON node_outputs (project_id)`,
}

// m19Migrations 建 org_secrets (组织级命名密钥注册表)：value_enc 是 AES-256-GCM 密文
// (secretbox)，永不出服务端。被 http 自定义节点的 {{secret:NAME}} 自由文本引用。
// 唯一索引 (org_id, name)。无 delete-in-use 守卫 (自由文本引用无结构化 FK；执行时缺
// 密钥 → 不透明失败，见 spec 安全节)。additive only。
var m19Migrations = []string{
	`CREATE TABLE IF NOT EXISTS org_secrets (
		id TEXT PRIMARY KEY,
		org_id TEXT NOT NULL,
		name TEXT NOT NULL,
		value_enc BYTEA NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS org_secrets_org_name_uniq ON org_secrets (org_id, name)`,
}

// m20Migrations 加「运行期输入」双列：workflows.inputs_schema（设计期声明的 InputField[]，
// DEFAULT '[]'）与 plans.run_inputs（本次 run 的值+schema 快照，DEFAULT '{}'）。DEFAULT
// 保证旧行零回归。plans.run_inputs 列在此一并建，T3 才写入。幂等 DDL，additive only。
var m20Migrations = []string{
	`ALTER TABLE workflows ADD COLUMN IF NOT EXISTS inputs_schema JSONB NOT NULL DEFAULT '[]'`,
	`ALTER TABLE plans     ADD COLUMN IF NOT EXISTS run_inputs   JSONB NOT NULL DEFAULT '{}'`,
}

// schemaMigrationsDDL creates the version-tracking table for Go-coded migration
// steps. Runs FIRST (before legacy DDL) and is itself idempotent. Only the Go
// steps (goSteps) are version-tracked here; the legacy m1…m19 DDL runs
// unconditionally and self-skips via IF NOT EXISTS.
var schemaMigrationsDDL = []string{
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`,
}

// migrationStep is a Go-coded, version-tracked migration. run executes inside a
// per-step transaction; on success the version is recorded in schema_migrations
// so the step never runs twice.
type migrationStep struct {
	version string
	run     func(ctx context.Context, tx pgx.Tx) error
}

// goSteps returns the version-tracked Go migration steps. testGoSteps overrides
// the production registry when set (tests). The production registry is empty
// until m21 is appended in a later P2a task.
func (s *Storage) goSteps() []migrationStep {
	if s.testGoSteps != nil {
		return s.testGoSteps
	}
	return []migrationStep{
		{version: "m21", run: m21AddItemsColumn},
		{version: "m22", run: m22CreateExportJobs},
		{version: "m23", run: m23RemovePicturebookStandard},
		{version: "m24", run: m24CreateExportJobs},
		{version: "m25", run: m25CreateOrgAlertSettings},
		{version: "m26", run: m26AddProjectsDeletedAt},
		{version: "m27", run: m27StripPBConfigInputs},
		{version: "m28", run: m28CreateOrgAuditLog},
	}
}

// m21AddItemsColumn adds node_outputs.items (JSONB NOT NULL DEFAULT '[]') and
// backfills historical rows FORMAT-AWARE (★M-2): json valid → [{json: content}],
// invalid → [{json:{text:content,_parseError:true}}] (so the migration never
// half-fails); text/http-status → [{json:{text:content}}]. Only rows still at the
// default are backfilled (re-run safe).
func m21AddItemsColumn(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx,
		`ALTER TABLE node_outputs ADD COLUMN IF NOT EXISTS items JSONB NOT NULL DEFAULT '[]'`); err != nil {
		return fmt.Errorf("add items column: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT id, content, format FROM node_outputs WHERE items = '[]'::jsonb`)
	if err != nil {
		return fmt.Errorf("scan legacy rows: %w", err)
	}
	type row struct{ id, content, format string }
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.content, &r.format); err != nil {
			rows.Close()
			return fmt.Errorf("scan row: %w", err)
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate rows: %w", err)
	}
	for _, r := range batch {
		var inner json.RawMessage
		switch r.format {
		case "json":
			if json.Valid([]byte(r.content)) {
				inner = json.RawMessage(r.content)
			} else {
				inner, _ = json.Marshal(map[string]any{"text": r.content, "_parseError": true})
			}
		default:
			inner, _ = json.Marshal(map[string]string{"text": r.content})
		}
		items, err := json.Marshal([]map[string]json.RawMessage{{"json": inner}})
		if err != nil {
			return fmt.Errorf("marshal items for %s: %w", r.id, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE node_outputs SET items=$1 WHERE id=$2`, items, r.id); err != nil {
			return fmt.Errorf("backfill %s: %w", r.id, err)
		}
	}
	return nil
}

// m22CreateExportJobs creates the export_jobs queue table (picturebook 成书导出).
// It is an INDEPENDENT async queue (NOT the todos run-queue): exporting is a
// read-only consumption of accepted assets and must not pollute run lifecycle.
// It reuses the worker's claim/lease/reaper PATTERN (status + locked_by/
// locked_until + next_run_at), with the same FOR UPDATE SKIP LOCKED claim. Two
// indexes: claim path (status, next_run_at) and project listing (project_id).
// Idempotent (CREATE TABLE / INDEX IF NOT EXISTS).
func m22CreateExportJobs(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS export_jobs (
			id                TEXT PRIMARY KEY,
			project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			plan_id           TEXT NOT NULL DEFAULT '',
			format            TEXT NOT NULL,
			status            TEXT NOT NULL DEFAULT 'pending',
			blob_key          TEXT NOT NULL DEFAULT '',
			storage_config_id TEXT NOT NULL DEFAULT '',
			size_bytes        BIGINT NOT NULL DEFAULT 0,
			error             TEXT NOT NULL DEFAULT '',
			attempts          INT NOT NULL DEFAULT 0,
			locked_by         TEXT NOT NULL DEFAULT '',
			locked_until      TIMESTAMPTZ,
			next_run_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create export_jobs: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS export_jobs_claim_idx ON export_jobs (status, next_run_at)`); err != nil {
		return fmt.Errorf("create export_jobs_claim_idx: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS export_jobs_project_idx ON export_jobs (project_id)`); err != nil {
		return fmt.Errorf("create export_jobs_project_idx: %w", err)
	}
	return nil
}

// m23RemovePicturebookStandard retires the built-in picturebook + standard
// (LLM auto-planner) content pipelines: existing projects of those kinds collapse
// to the custom-workflow kind, the picturebook_config column is dropped, and the
// export_jobs queue (created by m22) is dropped. The projects.kind column itself
// is kept (now only 'custom'). Idempotent (UPDATE is a no-op once converged; both
// DROPs use IF EXISTS).
func m23RemovePicturebookStandard(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx,
		`UPDATE projects SET kind='custom' WHERE kind IN ('picturebook','standard')`); err != nil {
		return fmt.Errorf("converge project kinds: %w", err)
	}
	if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS export_jobs`); err != nil {
		return fmt.Errorf("drop export_jobs: %w", err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE projects DROP COLUMN IF EXISTS picturebook_config`); err != nil {
		return fmt.Errorf("drop picturebook_config: %w", err)
	}
	return nil
}

// m24CreateExportJobs re-creates the export_jobs queue table for the restored
// workflow 作品导出 (PDF/EPUB/ZIP) feature. The chain m22-create → m23-drop →
// m24-recreate is intentional: m23 retired the picturebook-coupled export queue,
// and this forward step re-establishes the same table for the workflow-only
// export path (do NOT edit m23 to keep the table — versioned migrations are
// append-only). DDL is byte-identical to m22 (INDEPENDENT async claim/lease/
// reaper queue, two indexes) and fully idempotent (CREATE ... IF NOT EXISTS).
func m24CreateExportJobs(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS export_jobs (
			id                TEXT PRIMARY KEY,
			project_id        TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			plan_id           TEXT NOT NULL DEFAULT '',
			format            TEXT NOT NULL,
			status            TEXT NOT NULL DEFAULT 'pending',
			blob_key          TEXT NOT NULL DEFAULT '',
			storage_config_id TEXT NOT NULL DEFAULT '',
			size_bytes        BIGINT NOT NULL DEFAULT 0,
			error             TEXT NOT NULL DEFAULT '',
			attempts          INT NOT NULL DEFAULT 0,
			locked_by         TEXT NOT NULL DEFAULT '',
			locked_until      TIMESTAMPTZ,
			next_run_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create export_jobs: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS export_jobs_claim_idx ON export_jobs (status, next_run_at)`); err != nil {
		return fmt.Errorf("create export_jobs_claim_idx: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS export_jobs_project_idx ON export_jobs (project_id)`); err != nil {
		return fmt.Errorf("create export_jobs_project_idx: %w", err)
	}
	return nil
}

// m25CreateOrgAlertSettings creates the org_alert_settings table (run 失败邮件
// 告警的 org 级配置)：每 org 一行（org_id 主键），email 为告警收件邮箱，enabled
// 为总开关。未配置行 = 完全静默（等价 enabled=false）。Forward-only、幂等
// (CREATE TABLE IF NOT EXISTS)。
func m25CreateOrgAlertSettings(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS org_alert_settings (
			org_id     TEXT PRIMARY KEY,
			email      TEXT NOT NULL DEFAULT '',
			enabled    BOOLEAN NOT NULL DEFAULT false,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create org_alert_settings: %w", err)
	}
	return nil
}

// m26AddProjectsDeletedAt adds projects.deleted_at (TIMESTAMPTZ, NULL = 活) for
// the DELETE project 软删 feature (docs/specs/project-delete.md §1)。NULL 行为
// 存量项目零回归；置非空即从 Get/List/中间件解析/worker claim 一切读路径消失，
// generations 计费账本与 blob 字节保留不动。Forward-only、幂等 (IF NOT EXISTS)。
func m26AddProjectsDeletedAt(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx,
		`ALTER TABLE projects ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ`); err != nil {
		return fmt.Errorf("add projects.deleted_at: %w", err)
	}
	return nil
}

// m27StripPBConfigInputs strips target='pbConfig' fields from workflows.
// inputs_schema：workflow-only 转型（m23）删掉绘本配置后，pbConfig 是死通道
// （Resolved.PBOverride 无任何消费方），runinputs 的 allowlist 已随之删除
// target=pbConfig 与 type=multiselect。不清洗则存量 workflow 保存 / 运行时会
// 撞新 allowlist 校验失败。multiselect 字段旧校验强制 target=pbConfig，故按
// target 过滤即一并剥离。Forward-only、幂等（首跑后无行再匹配 WHERE）。
func m27StripPBConfigInputs(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		UPDATE workflows
		SET inputs_schema = COALESCE(
			(SELECT jsonb_agg(f ORDER BY ord)
			   FROM jsonb_array_elements(inputs_schema) WITH ORDINALITY AS t(f, ord)
			  WHERE f->>'target' IS DISTINCT FROM 'pbConfig'),
			'[]'::jsonb)
		WHERE EXISTS (
			SELECT 1 FROM jsonb_array_elements(inputs_schema) AS f
			 WHERE f->>'target' = 'pbConfig')`); err != nil {
		return fmt.Errorf("strip pbConfig inputs: %w", err)
	}
	return nil
}

// m28CreateOrgAuditLog creates the org_audit_log table：安全敏感的管理操作审计流水
// （append-only）。每行记一次管理动作：actor（当前登录用户 id / 可选 email）、action
// （稳定字符串，如 model_key.reveal / model_config.update）、target（类型 + id）、detail
// （最小化、非敏感的 JSON，绝不含明文密钥）。索引 (org_id, created_at DESC, id DESC)
// 支撑「按 org 倒序 + keyset 翻页」的未来审计 UI。Forward-only、幂等
// (CREATE TABLE / INDEX IF NOT EXISTS)。
func m28CreateOrgAuditLog(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS org_audit_log (
			id            TEXT PRIMARY KEY,
			org_id        TEXT NOT NULL,
			actor_user_id TEXT NOT NULL DEFAULT '',
			actor_email   TEXT NOT NULL DEFAULT '',
			action        TEXT NOT NULL,
			target_type   TEXT NOT NULL DEFAULT '',
			target_id     TEXT NOT NULL DEFAULT '',
			detail        JSONB NOT NULL DEFAULT '{}',
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create org_audit_log: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`CREATE INDEX IF NOT EXISTS org_audit_log_org_created_idx ON org_audit_log (org_id, created_at DESC, id DESC)`); err != nil {
		return fmt.Errorf("create org_audit_log_org_created_idx: %w", err)
	}
	return nil
}

// Migrate applies the legacy idempotent DDL (M1 … M19) followed by the
// version-tracked Go migration steps. Hardened with a boot-time advisory lock
// (serializes concurrent migrators), per-step transactions, and a
// schema_migrations version table. The legacy DDL still runs unconditionally
// and byte-for-byte (it self-skips via IF NOT EXISTS); only Go steps are
// version-tracked. Idempotent.
func (s *Storage) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("storage: migrate acquire: %w", err)
	}
	defer conn.Release()
	const lockKey = "studio_schema_migrate"
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, lockKey); err != nil {
		return fmt.Errorf("storage: migrate lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, lockKey) }()

	all := append([]string{}, schemaMigrationsDDL...)
	all = append(all, m1Migrations...)
	all = append(all, m2Migrations...)
	all = append(all, m3Migrations...)
	all = append(all, m4Migrations...)
	all = append(all, m5Migrations...)
	all = append(all, m6Migrations...)
	all = append(all, m7Migrations...)
	all = append(all, m8Migrations...)
	all = append(all, m9Migrations...)
	all = append(all, m10Migrations...)
	all = append(all, m11Migrations...)
	all = append(all, m12Migrations...)
	all = append(all, m13Migrations...)
	all = append(all, m14Migrations...)
	all = append(all, m15Migrations...)
	all = append(all, m16Migrations...)
	all = append(all, m17Migrations...)
	all = append(all, m18Migrations...)
	all = append(all, m19Migrations...)
	all = append(all, m20Migrations...)
	for _, stmt := range all {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("storage: migrate: %w", err)
		}
	}

	for _, step := range s.goSteps() {
		var applied bool
		if err := conn.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, step.version).Scan(&applied); err != nil {
			return fmt.Errorf("storage: migrate check %s: %w", step.version, err)
		}
		if applied {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("storage: migrate begin %s: %w", step.version, err)
		}
		if err := step.run(ctx, tx); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("storage: migrate step %s: %w", step.version, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT (version) DO NOTHING`, step.version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("storage: migrate record %s: %w", step.version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("storage: migrate commit %s: %w", step.version, err)
		}
	}
	return nil
}
