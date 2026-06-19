// Package project owns project CRUD + the project status machine derived from
// the project's todos (spec §5). It mirrors orgkb's resource pattern but the
// org membership bootstrap lives in httpapi (POST /api/orgs) like kb.
package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/projectstate"
)

// ErrNotFound is returned when a project row does not exist.
var ErrNotFound = errors.New("project: not found")

// ErrInvalidStorageConfig 表示传入的 storage_config_id 不属于项目所在 org（或不存在）。
// 防跨租户存储写入：项目只能引用自身 org 的 scope='org' 存储配置（空 = 无 override，走默认）。
var ErrInvalidStorageConfig = errors.New("project: storage config not found for org")

// Project is a projects row.
type Project struct {
	ID             string `json:"id"`
	OrgID          string `json:"orgId"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	ContentType    string `json:"contentType"`
	TargetPlatform string `json:"targetPlatform"`
	Style          string `json:"style"`
	Status         string `json:"status"`
	CreatedBy      string `json:"createdBy"`
	FallbackUsed   bool   `json:"fallbackUsed"`
	// M5.1: per-project 规划模型 override。空 = 走 org 默认；非空时 runHandler
	// 经 modelrouter.ChatModelForNamed 查 org model_configs 拿 provider/model 对应
	// 的 key，再交给 planner.PlanWith 使用。
	PlannerProvider string `json:"plannerProvider"`
	PlannerModel    string `json:"plannerModel"`
	// M9: per-project 图片生成模型 override。空 = 走 org 默认；非空时 worker
	// 经 modelrouter.MediaGeneratorForNamed 查 org model_configs 拿 provider/model 对应
	// 的 key，并构造对应的 MediaGenerator。
	ImageProvider         string          `json:"imageProvider"`
	ImageModel            string          `json:"imageModel"`
	StorageMode           string          `json:"storageMode"`
	CustomWorkflowEnabled bool            `json:"customWorkflowEnabled"`
	WorkflowNodes         json.RawMessage `json:"workflowNodes"`
	// M14: cover image link — an assets row reused (served via GET
	// /api/assets/{id}/content). '' = no cover.
	CoverAssetID string `json:"coverAssetId"`
	// M16: per-project storage config override. Empty = use org default → builtin.
	// Wired through ResolveWriteTarget; worker/cover write paths consult this first.
	StorageConfigID string `json:"storageConfigId"`
	// 儿童绘本：Kind 区分项目类型（'standard' / 'picturebook'），
	// PictureBookConfig 存绘本参数原始 JSON 字符串（见 ParsePictureBookConfig）。
	Kind              string `json:"kind"`
	PictureBookConfig string `json:"pictureBookConfig"`
}

// CreateInput is the input to Create. Brief maps to the description column
// (the creative brief the planner/ScriptAgent consume).
type CreateInput struct {
	OrgID                 string
	Name                  string
	Brief                 string
	ContentType           string
	TargetPlatform        string
	Style                 string
	CreatedBy             string
	PlannerProvider       string
	PlannerModel          string
	ImageProvider         string
	ImageModel            string
	StorageMode           string
	StorageConfigID       string
	CustomWorkflowEnabled bool
	WorkflowNodes         json.RawMessage
	Kind                  string
	PictureBookConfig     string
}

// UpdateInput 用于后期修改项目元数据（M5.1/M9 edit 入口）。
// 基本信息（名称/创意需求/内容类型/目标平台/风格）也走这里编辑——early 设计
// 让用户「删了重建」，现按需补上原地编辑。
type UpdateInput struct {
	Name                  string          `json:"name"`
	Description           string          `json:"description"`
	ContentType           string          `json:"contentType"`
	TargetPlatform        string          `json:"targetPlatform"`
	Style                 string          `json:"style"`
	PlannerProvider       string          `json:"plannerProvider"`
	PlannerModel          string          `json:"plannerModel"`
	ImageProvider         string          `json:"imageProvider"`
	ImageModel            string          `json:"imageModel"`
	StorageMode           string          `json:"storageMode"`
	StorageConfigID       string          `json:"storageConfigId"`
	CustomWorkflowEnabled bool            `json:"customWorkflowEnabled"`
	WorkflowNodes         json.RawMessage `json:"workflowNodes"`
	Kind                  string          `json:"kind"`
	PictureBookConfig     string          `json:"pictureBookConfig"`
}

// Store persists projects.
type Store struct {
	pool *pgxpool.Pool
}

// New builds a Store.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// storageConfigBelongsToOrg 校验 configID 是否为 orgID 的 scope='org' 存储配置。
// 防跨租户存储写入：项目只能引用自身 org 的存储配置（空 configID 由调用方先行短路）。
func (s *Store) storageConfigBelongsToOrg(ctx context.Context, configID, orgID string) (bool, error) {
	var ok bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM storage_configs WHERE id=$1 AND org_id=$2 AND scope='org')`,
		configID, orgID).Scan(&ok); err != nil {
		return false, fmt.Errorf("project: validate storage config: %w", err)
	}
	return ok, nil
}

// Create inserts a project (status='draft').
func (s *Store) Create(ctx context.Context, in CreateInput) (Project, error) {
	if in.OrgID == "" || in.Name == "" || in.CreatedBy == "" {
		return Project{}, fmt.Errorf("project: OrgID, Name, CreatedBy required")
	}
	// 存储配置 override 必须属于本 org（防跨租户存储写入）；空 = 无 override，走默认。
	if in.StorageConfigID != "" {
		ok, err := s.storageConfigBelongsToOrg(ctx, in.StorageConfigID, in.OrgID)
		if err != nil {
			return Project{}, err
		}
		if !ok {
			return Project{}, ErrInvalidStorageConfig
		}
	}
	kind := in.Kind
	if kind == "" {
		kind = "standard"
	}
	p := Project{
		ID: newID(), OrgID: in.OrgID, Name: in.Name, Description: in.Brief,
		ContentType: in.ContentType, TargetPlatform: in.TargetPlatform,
		Style: in.Style, Status: "draft", CreatedBy: in.CreatedBy,
		PlannerProvider: in.PlannerProvider, PlannerModel: in.PlannerModel,
		ImageProvider: in.ImageProvider, ImageModel: in.ImageModel,
		StorageMode: in.StorageMode, StorageConfigID: in.StorageConfigID,
		CustomWorkflowEnabled: in.CustomWorkflowEnabled,
		WorkflowNodes:         in.WorkflowNodes,
		Kind:                  kind, PictureBookConfig: in.PictureBookConfig,
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, description, content_type, target_platform, style, status, created_by, planner_provider, planner_model, image_provider, image_model, storage_mode, custom_workflow_enabled, workflow_nodes, storage_config_id, kind, picturebook_config)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		p.ID, p.OrgID, p.Name, p.Description, p.ContentType, p.TargetPlatform, p.Style, p.Status, p.CreatedBy, p.PlannerProvider, p.PlannerModel, p.ImageProvider, p.ImageModel, p.StorageMode, p.CustomWorkflowEnabled, p.WorkflowNodes, p.StorageConfigID, p.Kind, p.PictureBookConfig); err != nil {
		return Project{}, fmt.Errorf("project: insert: %w", err)
	}
	return p, nil
}

// Get returns a project by id.
func (s *Store) Get(ctx context.Context, id string) (Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx,
		`SELECT p.id, p.org_id, p.name, p.description, p.content_type, p.target_platform, p.style, p.status, p.created_by,
		        COALESCE(pl.fallback_used, false),
		        p.planner_provider, p.planner_model, p.image_provider, p.image_model, p.storage_mode,
		        p.custom_workflow_enabled, p.workflow_nodes, p.cover_asset_id, COALESCE(p.storage_config_id, ''),
		        COALESCE(p.kind, 'standard'), COALESCE(p.picturebook_config, '')
		 FROM projects p
		 LEFT JOIN (
		     SELECT DISTINCT ON (project_id) project_id, fallback_used
		     FROM plans
		     ORDER BY project_id, created_at DESC
		 ) pl ON p.id = pl.project_id
		 WHERE p.id=$1`, id).
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.ContentType, &p.TargetPlatform, &p.Style, &p.Status, &p.CreatedBy, &p.FallbackUsed, &p.PlannerProvider, &p.PlannerModel, &p.ImageProvider, &p.ImageModel, &p.StorageMode, &p.CustomWorkflowEnabled, &p.WorkflowNodes, &p.CoverAssetID, &p.StorageConfigID, &p.Kind, &p.PictureBookConfig)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return p, err
}

// OrgIDForProject resolves the org for a project (used by the RBAC middleware,
// which only has the project id from the path). Mirrors orgkb.OrgIDForKB.
func (s *Store) OrgIDForProject(ctx context.Context, projectID string) (string, error) {
	var orgID string
	err := s.pool.QueryRow(ctx, `SELECT org_id FROM projects WHERE id=$1`, projectID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return orgID, err
}

// ListByOrg returns up to limit projects for an org, keyset-paginated by id.
func (s *Store) ListByOrg(ctx context.Context, orgID string, limit int, cursor string) ([]Project, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, name, description, content_type, target_platform, style, status, created_by, planner_provider, planner_model, image_provider, image_model, storage_mode, custom_workflow_enabled, workflow_nodes, cover_asset_id, COALESCE(storage_config_id, ''), COALESCE(kind, 'standard'), COALESCE(picturebook_config, '')
		 FROM projects WHERE org_id=$1 AND id>$2 ORDER BY id ASC LIMIT $3`,
		orgID, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.ContentType, &p.TargetPlatform, &p.Style, &p.Status, &p.CreatedBy, &p.PlannerProvider, &p.PlannerModel, &p.ImageProvider, &p.ImageModel, &p.StorageMode, &p.CustomWorkflowEnabled, &p.WorkflowNodes, &p.CoverAssetID, &p.StorageConfigID, &p.Kind, &p.PictureBookConfig); err != nil {
			return nil, "", err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) == limit {
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// SetStatus writes the project status directly (used on run kickoff: planning).
func (s *Store) SetStatus(ctx context.Context, id, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}

// SetCover links a project to its cover asset (M14). assetID="" clears the cover.
// 0 rows affected = no such project → ErrNotFound (404 not 200).
func (s *Store) SetCover(ctx context.Context, projectID, assetID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE projects SET cover_asset_id=$2, updated_at=now() WHERE id=$1`, projectID, assetID)
	if err != nil {
		return fmt.Errorf("project: set cover: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Update 修改项目的 planner_provider / planner_model 以及 image_provider / image_model
// （其他字段不允许改 — 想改 brief / style / 内容类型只能删了重建，避免污染已有 run 事件 history）。
// 0 行影响 = 找不到该 id（POST 一致返 404 而非 200）。
func (s *Store) Update(ctx context.Context, id string, in UpdateInput) (Project, error) {
	// 存储配置 override 必须属于本项目 org（防跨租户存储写入）；先取项目 org（保留 ErrNotFound 语义）。
	if in.StorageConfigID != "" {
		var orgID string
		if err := s.pool.QueryRow(ctx, `SELECT org_id FROM projects WHERE id=$1`, id).Scan(&orgID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Project{}, ErrNotFound
			}
			return Project{}, fmt.Errorf("project: update: lookup org: %w", err)
		}
		ok, err := s.storageConfigBelongsToOrg(ctx, in.StorageConfigID, orgID)
		if err != nil {
			return Project{}, err
		}
		if !ok {
			return Project{}, ErrInvalidStorageConfig
		}
	}
	// custom_workflow_enabled / workflow_nodes are intentionally NOT updated here:
	// custom workflows are now first-class rows in the workflows table (m12), and
	// the project edit form no longer sends them. Leaving the legacy columns
	// untouched preserves any pre-migration data instead of zeroing it on edit.
	tag, err := s.pool.Exec(ctx,
		`UPDATE projects
		 SET name=$2, description=$3, content_type=$4, target_platform=$5, style=$6,
		     planner_provider=$7, planner_model=$8, image_provider=$9, image_model=$10, storage_mode=$11,
		     storage_config_id=$12,
		     kind=COALESCE(NULLIF($13, ''), kind),
		     picturebook_config=COALESCE(NULLIF($14, ''), picturebook_config),
		     updated_at=now()
		 WHERE id=$1`,
		id, in.Name, in.Description, in.ContentType, in.TargetPlatform, in.Style,
		in.PlannerProvider, in.PlannerModel, in.ImageProvider, in.ImageModel, in.StorageMode, in.StorageConfigID,
		in.Kind, in.PictureBookConfig)
	if err != nil {
		return Project{}, fmt.Errorf("project: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return Project{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

// RefreshStatus recomputes the project status from its LATEST plan's todo
// tally and persists it. Called by the worker after each todo transition
// (spec §7.3 step 5).
//
// 关键不变式：项目 status 必须按"最新 plan 维度"算。历史失败 run 不该污染当前
// 重跑——计划 A 6/6 跑挂后再重跑成功（计划 B 5/5 done + 2 待审），项目应
// 解析为 "review"，旧实现因为累加 todos（Failed=1）会卡在 "failed"（生产
// 真实事故）。todos 表带 plan_id，SELECT 加 WHERE plan_id=<最新> 即可。
//
// 无 plan 的项目（draft 初始态 / 还没跑过）保持现状：DeriveStatus 对空
// TodoCounts 返 "planning"，但项目初始就是 "draft"，硬改写会误导。
func (s *Store) RefreshStatus(ctx context.Context, projectID string) (string, error) {
	var latestPlanID string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM plans WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
		projectID).Scan(&latestPlanID)
	if errors.Is(err, pgx.ErrNoRows) {
		// 无 plan：不改写 project.status（保持 create 时的 draft）。返回
		// Get 出来的当前值，便于 caller 行为对称。
		return s.currentStatus(ctx, projectID)
	}
	if err != nil {
		return "", fmt.Errorf("project: find latest plan: %w", err)
	}

	var c TodoCounts
	err = s.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE status='ready'),
		       count(*) FILTER (WHERE status='running'),
		       count(*) FILTER (WHERE status='blocked'),
		       count(*) FILTER (WHERE status='done'),
		       count(*) FILTER (WHERE status='failed'),
		       count(*) FILTER (WHERE status='canceled')
		FROM todos WHERE plan_id=$1`, latestPlanID).
		Scan(&c.Total, &c.Ready, &c.Running, &c.Blocked, &c.Done, &c.Failed, &c.Canceled)
	if err != nil {
		return "", fmt.Errorf("project: tally latest plan todos: %w", err)
	}
	// 资产通过 todos 关联到 plan（assets.todo_id → todos.plan_id）；这样
	// pending_acceptance 也只计最新 plan 的，与 ListPlans 的关联方式一致。
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM assets a
		 JOIN todos t ON a.todo_id = t.id
		 WHERE t.plan_id = $1 AND a.status = 'pending_acceptance'`,
		latestPlanID).Scan(&c.PendingAssets); err != nil {
		return "", fmt.Errorf("project: tally latest plan pending assets: %w", err)
	}
	status := DeriveStatus(c)
	if err := s.SetStatus(ctx, projectID, status); err != nil {
		return "", err
	}
	return status, nil
}

// currentStatus returns the project's status column without modification.
// Used by RefreshStatus to return the un-touched value when there are no
// plans yet (so the function's return semantics stay uniform for callers).
func (s *Store) currentStatus(ctx context.Context, projectID string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT status FROM projects WHERE id=$1`, projectID).Scan(&status)
	return status, err
}

// Cancel marks all non-terminal todos canceled, sweeps in-flight assets
// ('generating' sync + 'submitted' async, spec §5.4 必修) to a terminal
// 'canceled' (M3 取消语义 — in-flight generation results then no-op on arrival
// because assets.SetBlob is guarded on status='generating'/'submitted'), and
// sets the project canceled. An async 'submitted' asset has an external job
// running for real money; the cancel sweep terminal-states it so the poll-path
// worker (whose guarded reschedule then finds 0 rows) stops and the row does
// not strand in 'submitted'. pending_acceptance
// assets are deliberately KEPT reviewable: the generation already cost real
// money and HITL accept/reject still applies; DeriveStatus's Canceled branch
// outranks the review branch so the project status stays canceled.
func (s *Store) Cancel(ctx context.Context, projectID string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE todos SET status='canceled', locked_by='', locked_until=NULL, updated_at=now()
		 WHERE project_id=$1 AND status IN ('pending','ready','blocked','running')`, projectID); err != nil {
		return fmt.Errorf("project: cancel todos: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE assets SET status='canceled' WHERE project_id=$1 AND status IN ('generating','submitted')`, projectID); err != nil {
		return fmt.Errorf("project: cancel assets: %w", err)
	}
	return s.SetStatus(ctx, projectID, "canceled")
}

// Plan represents a run/plan for a project.
type Plan struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"projectId"`
	Status       string    `json:"status"`
	Valid        bool      `json:"valid"`
	FallbackUsed bool      `json:"fallbackUsed"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ListPlans lists all plans/runs for a specific project.
func (s *Store) ListPlans(ctx context.Context, projectID string) ([]Plan, error) {
	q := `
		SELECT p.id, p.project_id, p.valid, p.fallback_used, p.created_at,
		       COALESCE(t.total, 0),
		       COALESCE(t.ready, 0),
		       COALESCE(t.running, 0),
		       COALESCE(t.blocked, 0),
		       COALESCE(t.done, 0),
		       COALESCE(t.failed, 0),
		       COALESCE(t.canceled, 0),
		       COALESCE(a.pending_assets, 0)
		FROM plans p
		LEFT JOIN (
		    SELECT plan_id,
		           count(*) as total,
		           count(*) FILTER (WHERE status='ready') as ready,
		           count(*) FILTER (WHERE status='running') as running,
		           count(*) FILTER (WHERE status='blocked') as blocked,
		           count(*) FILTER (WHERE status='done') as done,
		           count(*) FILTER (WHERE status='failed') as failed,
		           count(*) FILTER (WHERE status='canceled') as canceled
		    FROM todos
		    GROUP BY plan_id
		) t ON p.id = t.plan_id
		LEFT JOIN (
		    SELECT t.plan_id, count(*) as pending_assets
		    FROM assets a
		    JOIN todos t ON a.todo_id = t.id
		    WHERE a.status = 'pending_acceptance'
		    GROUP BY t.plan_id
		) a ON p.id = a.plan_id
		WHERE p.project_id = $1
		ORDER BY p.created_at DESC`

	rows, err := s.pool.Query(ctx, q, projectID)
	if err != nil {
		return nil, fmt.Errorf("project: list plans: %w", err)
	}
	defer rows.Close()

	var out []Plan
	for rows.Next() {
		var p Plan
		var c TodoCounts
		if err := rows.Scan(
			&p.ID, &p.ProjectID, &p.Valid, &p.FallbackUsed, &p.CreatedAt,
			&c.Total, &c.Ready, &c.Running, &c.Blocked, &c.Done, &c.Failed, &c.Canceled, &c.PendingAssets,
		); err != nil {
			return nil, fmt.Errorf("project: list plans: scan: %w", err)
		}
		p.Status = DeriveStatus(c)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("project: list plans: rows: %w", err)
	}
	return out, nil
}

// LoadState loads a plan's todos + assets + event version and computes the
// authoritative ProjectState (single source of truth for render). Used by
// the GET /state endpoint and the SSE pusher so both channels agree.
//
// planID: when non-empty, loads state for that specific plan (guarded to
// projectID to prevent cross-project leakage). When empty, loads the latest
// plan — preserves existing behavior for callers that pass "".
func (s *Store) LoadState(ctx context.Context, projectID, planID string) (projectstate.ProjectState, error) {
	p, err := s.Get(ctx, projectID)
	if err != nil {
		return projectstate.ProjectState{}, err
	}
	in := projectstate.Input{
		ProjectID:             projectID,
		ProjectStatus:         p.Status,
		CustomWorkflowEnabled: p.CustomWorkflowEnabled,
	}

	// version = max event seq for the project (monotonic; 0 if none).
	// Note: this is project-wide, not scoped to a single plan. It may
	// over-trigger a re-push on a historical page when a newer run emits
	// events, but that is harmless — the re-pushed payload is still computed
	// for THIS planID below, so the client just receives its own (unchanged)
	// snapshot again rather than another run's state.
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(max(seq), 0) FROM run_events WHERE project_id=$1`, projectID).
		Scan(&in.Version); err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state version: %w", err)
	}

	// resolve plan: when planID is provided use that plan (scoped to project);
	// otherwise fall back to the latest plan for this project.
	var planRowID, workflowID string
	var valid, fallbackUsed bool
	if planID == "" {
		err = s.pool.QueryRow(ctx,
			`SELECT id, valid, fallback_used, COALESCE(workflow_id,'') FROM plans WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
			projectID).Scan(&planRowID, &valid, &fallbackUsed, &workflowID)
	} else {
		err = s.pool.QueryRow(ctx,
			`SELECT id, valid, fallback_used, COALESCE(workflow_id,'') FROM plans WHERE id=$1 AND project_id=$2`,
			planID, projectID).Scan(&planRowID, &valid, &fallbackUsed, &workflowID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return projectstate.Compute(in), nil // no plan / not found: draft passthrough
	}
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state plan: %w", err)
	}
	in.HasPlan = true
	in.Plan = &projectstate.Plan{PlanID: planRowID, Valid: valid, FallbackUsed: fallbackUsed}
	in.WorkflowID = workflowID

	// todos of the resolved plan
	rows, err := s.pool.Query(ctx,
		`SELECT id, type, status, COALESCE(error,''), depends_on, created_at FROM todos WHERE plan_id=$1 ORDER BY updated_at ASC`, planRowID)
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state todos: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t projectstate.Todo
		if err := rows.Scan(&t.ID, &t.Type, &t.Status, &t.Error, &t.DependsOn, &t.CreatedAt); err != nil {
			return projectstate.ProjectState{}, fmt.Errorf("project: scan state todo: %w", err)
		}
		in.Todos = append(in.Todos, t)
	}
	if err := rows.Err(); err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state todos rows: %w", err)
	}

	// assets of the resolved plan (joined via todos)
	arows, err := s.pool.Query(ctx,
		`SELECT a.id, a.todo_id, a.status FROM assets a
		 JOIN todos t ON a.todo_id = t.id
		 WHERE t.plan_id=$1 ORDER BY a.created_at ASC`, planRowID)
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state assets: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var a projectstate.Asset
		if err := arows.Scan(&a.ID, &a.TodoID, &a.Status); err != nil {
			return projectstate.ProjectState{}, fmt.Errorf("project: scan state asset: %w", err)
		}
		in.Assets = append(in.Assets, a)
	}
	if err := arows.Err(); err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state assets rows: %w", err)
	}

	return projectstate.Compute(in), nil
}
