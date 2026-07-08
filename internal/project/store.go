// Package project owns project CRUD + the project status machine derived from
// the project's todos (spec §5). It mirrors orgkb's resource pattern but the
// org membership bootstrap lives in httpapi (POST /api/orgs) like kb.
package project

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/projectstate"
)

// ProjectNameMaxLen 是项目名的长度上限（Unicode 字符数），与前端 projectFormSchema 对齐。
const ProjectNameMaxLen = 200

// ErrNotFound is returned when a project row does not exist.
var ErrNotFound = errors.New("project: not found")

// ErrInvalidStorageConfig 表示传入的 storage_config_id 不属于项目所在 org（或不存在）。
// 防跨租户存储写入：项目只能引用自身 org 的 scope='org' 存储配置（空 = 无 override，走默认）。
var ErrInvalidStorageConfig = errors.New("project: storage config not found for org")

// ErrEmptyName / ErrNameTooLong 是项目名校验错误（trim 后为空 / 超长）；handler 映射为 400。
var ErrEmptyName = errors.New("项目名称不能为空")
var ErrNameTooLong = fmt.Errorf("项目名称不能超过 %d 个字符", ProjectNameMaxLen)

// ErrNotCancelable 表示项目不在「在途」态（planning/running），无法取消；handler 映射为 409。
// 只有在途 run 可取消，静止态（draft/review/completed/failed/已 canceled）POST /cancel 是 no-op。
var ErrNotCancelable = errors.New("project: not in a cancelable state")

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
	// Kind 区分项目类型。绘本/standard 管线已移除；现存值经迁移收敛为 'custom'。
	Kind string `json:"kind"`
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
}

// Store persists projects.
type Store struct {
	db *gorm.DB
}

// New builds a Store.
func New(db *gorm.DB) *Store { return &Store{db: db} }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// storageConfigBelongsToOrg 校验 configID 是否为 orgID 的 scope='org' 存储配置。
// 防跨租户存储写入：项目只能引用自身 org 的存储配置（空 configID 由调用方先行短路）。
// 刻意只认 scope='org'：scope='global' 是系统级配置、不在 org 存储下拉里、也不作为项目级
// override（global 是 ResolveWriteTarget 在无 per-project/org 配置时的自动回落），故拒之。
func (s *Store) storageConfigBelongsToOrg(ctx context.Context, configID, orgID string) (bool, error) {
	var ok bool
	if err := s.db.WithContext(ctx).Raw(
		`SELECT EXISTS(SELECT 1 FROM storage_configs WHERE id=$1 AND org_id=$2 AND scope='org')`,
		configID, orgID).Row().Scan(&ok); err != nil {
		return false, fmt.Errorf("project: validate storage config: %w", err)
	}
	return ok, nil
}

// Create inserts a project (status='draft').
func (s *Store) Create(ctx context.Context, in CreateInput) (Project, error) {
	if in.OrgID == "" || in.CreatedBy == "" {
		return Project{}, fmt.Errorf("project: OrgID, CreatedBy required")
	}
	// 名称：trim 后拦空（含纯空白）并封顶长度；用 trim 后的值落库。
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Project{}, ErrEmptyName
	}
	if utf8.RuneCountInString(name) > ProjectNameMaxLen {
		return Project{}, ErrNameTooLong
	}
	in.Name = name
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
		kind = "custom" // workflow-only 转型后唯一的项目类型（m23 已收敛存量）
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
		Kind:                  kind,
	}
	if res := s.db.WithContext(ctx).Exec(
		`INSERT INTO projects (id, org_id, name, description, content_type, target_platform, style, status, created_by, planner_provider, planner_model, image_provider, image_model, storage_mode, custom_workflow_enabled, workflow_nodes, storage_config_id, kind)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		p.ID, p.OrgID, p.Name, p.Description, p.ContentType, p.TargetPlatform, p.Style, p.Status, p.CreatedBy, p.PlannerProvider, p.PlannerModel, p.ImageProvider, p.ImageModel, p.StorageMode, p.CustomWorkflowEnabled, p.WorkflowNodes, p.StorageConfigID, p.Kind); res.Error != nil {
		return Project{}, fmt.Errorf("project: insert: %w", res.Error)
	}
	return p, nil
}

// Get returns a project by id.
func (s *Store) Get(ctx context.Context, id string) (Project, error) {
	var p Project
	var nodesB []byte
	err := s.db.WithContext(ctx).Raw(
		`SELECT p.id, p.org_id, p.name, p.description, p.content_type, p.target_platform, p.style, p.status, p.created_by,
		        COALESCE(pl.fallback_used, false),
		        p.planner_provider, p.planner_model, p.image_provider, p.image_model, p.storage_mode,
		        p.custom_workflow_enabled, p.workflow_nodes, p.cover_asset_id, COALESCE(p.storage_config_id, ''),
		        COALESCE(p.kind, 'custom')
		 FROM projects p
		 LEFT JOIN (
		     SELECT DISTINCT ON (project_id) project_id, fallback_used
		     FROM plans
		     ORDER BY project_id, created_at DESC
		 ) pl ON p.id = pl.project_id
		 WHERE p.id=$1 AND p.deleted_at IS NULL`, id).Row().
		Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.ContentType, &p.TargetPlatform, &p.Style, &p.Status, &p.CreatedBy, &p.FallbackUsed, &p.PlannerProvider, &p.PlannerModel, &p.ImageProvider, &p.ImageModel, &p.StorageMode, &p.CustomWorkflowEnabled, &nodesB, &p.CoverAssetID, &p.StorageConfigID, &p.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err == nil {
		p.WorkflowNodes = json.RawMessage(nodesB)
	}
	return p, err
}

// OrgIDForProject resolves the org for a project (used by the RBAC middleware,
// which only has the project id from the path). Mirrors orgkb.OrgIDForKB.
//
// 刻意不过滤 deleted_at：(1) worker 在途任务的成本落账/告警要按 org 归属，项目
// 删除瞬间不能让这些内部解析失败；(2) DELETE 端点自身与 requireLiveProject 门禁
// 都要先经 RBAC——org 解析得通，org 成员才能拿到 404 而非 403。对外的软删排除
// 由 Get/List + httpapi 的 requireLiveProject 门禁承担。
func (s *Store) OrgIDForProject(ctx context.Context, projectID string) (string, error) {
	var orgID string
	err := s.db.WithContext(ctx).Raw(`SELECT org_id FROM projects WHERE id=$1`, projectID).Row().Scan(&orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return orgID, err
}

// ListByOrg returns up to limit projects for an org, keyset-paginated by id.
func (s *Store) ListByOrg(ctx context.Context, orgID string, limit int, cursor string) ([]Project, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT id, org_id, name, description, content_type, target_platform, style, status, created_by, planner_provider, planner_model, image_provider, image_model, storage_mode, custom_workflow_enabled, workflow_nodes, cover_asset_id, COALESCE(storage_config_id, ''), COALESCE(kind, 'custom')
		 FROM projects WHERE org_id=$1 AND deleted_at IS NULL AND id>$2 ORDER BY id ASC LIMIT $3`,
		orgID, cursor, limit).Rows()
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var nodesB []byte
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Description, &p.ContentType, &p.TargetPlatform, &p.Style, &p.Status, &p.CreatedBy, &p.PlannerProvider, &p.PlannerModel, &p.ImageProvider, &p.ImageModel, &p.StorageMode, &p.CustomWorkflowEnabled, &nodesB, &p.CoverAssetID, &p.StorageConfigID, &p.Kind); err != nil {
			return nil, "", err
		}
		p.WorkflowNodes = json.RawMessage(nodesB)
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
	return s.db.WithContext(ctx).Exec(`UPDATE projects SET status=$2, updated_at=now() WHERE id=$1`, id, status).Error
}

// TryBeginRun 原子地把项目状态从「非在途」翻到 planning，用于运行入口的并发/幂等门禁：
// 一个项目同一时刻只允许一个在途 run（project.status 单值），故用条件 UPDATE
// （compare-and-swap）而非「先读后写」，杜绝两个并发请求同时读到空闲态、双双建 plan 的
// TOCTOU 竞态。返回 (false, nil) 表示项目已在 planning/running（调用方回 409）；
// 0 行影响也可能是项目不存在，但运行入口已先 Get 过（404 已处理），这里的 0 行即视为在途。
func (s *Store) TryBeginRun(ctx context.Context, id string) (bool, error) {
	res := s.db.WithContext(ctx).Exec(
		`UPDATE projects SET status='planning', updated_at=now()
		 WHERE id=$1 AND status NOT IN ('planning','running')`, id)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// SetCover links a project to its cover asset (M14). assetID="" clears the cover.
// 0 rows affected = no such project → ErrNotFound (404 not 200).
func (s *Store) SetCover(ctx context.Context, projectID, assetID string) error {
	res := s.db.WithContext(ctx).Exec(
		`UPDATE projects SET cover_asset_id=$2, updated_at=now() WHERE id=$1`, projectID, assetID)
	if res.Error != nil {
		return fmt.Errorf("project: set cover: %w", res.Error)
	}
	if res.RowsAffected == 0 {
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
		if err := s.db.WithContext(ctx).Raw(`SELECT org_id FROM projects WHERE id=$1`, id).Row().Scan(&orgID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
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
	res := s.db.WithContext(ctx).Exec(
		`UPDATE projects
		 SET name=$2, description=$3, content_type=$4, target_platform=$5, style=$6,
		     planner_provider=$7, planner_model=$8, image_provider=$9, image_model=$10, storage_mode=$11,
		     -- storage_config_id 无条件写入（非 COALESCE）：编辑表单总会显式发该值，空串=用户选
		     -- 「继承组织默认」清除 override。若改成 COALESCE(NULLIF...) 反而让用户无法清除已设的 override。
		     -- 非空值已在上方校验属于本 org（防跨租户）。kind 用 COALESCE 是因表单不发它。
		     storage_config_id=$12,
		     kind=COALESCE(NULLIF($13, ''), kind),
		     updated_at=now()
		 WHERE id=$1`,
		id, in.Name, in.Description, in.ContentType, in.TargetPlatform, in.Style,
		in.PlannerProvider, in.PlannerModel, in.ImageProvider, in.ImageModel, in.StorageMode, in.StorageConfigID,
		in.Kind)
	if res.Error != nil {
		return Project{}, fmt.Errorf("project: update: %w", res.Error)
	}
	if res.RowsAffected == 0 {
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
	err := s.db.WithContext(ctx).Raw(
		`SELECT id FROM plans WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
		projectID).Row().Scan(&latestPlanID)
	if errors.Is(err, sql.ErrNoRows) {
		// 无 plan：不改写 project.status（保持 create 时的 draft）。返回
		// Get 出来的当前值，便于 caller 行为对称。
		return s.currentStatus(ctx, projectID)
	}
	if err != nil {
		return "", fmt.Errorf("project: find latest plan: %w", err)
	}

	var c TodoCounts
	err = s.db.WithContext(ctx).Raw(`
		SELECT count(*),
		       count(*) FILTER (WHERE status='ready'),
		       count(*) FILTER (WHERE status='running'),
		       count(*) FILTER (WHERE status='blocked'),
		       count(*) FILTER (WHERE status='done'),
		       count(*) FILTER (WHERE status='failed'),
		       count(*) FILTER (WHERE status='canceled')
		FROM todos WHERE plan_id=$1`, latestPlanID).Row().
		Scan(&c.Total, &c.Ready, &c.Running, &c.Blocked, &c.Done, &c.Failed, &c.Canceled)
	if err != nil {
		return "", fmt.Errorf("project: tally latest plan todos: %w", err)
	}
	// 资产通过 todos 关联到 plan（assets.todo_id → todos.plan_id）；这样
	// pending_acceptance 也只计最新 plan 的，与 ListPlans 的关联方式一致。
	if err := s.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM assets a
		 JOIN todos t ON a.todo_id = t.id
		 WHERE t.plan_id = $1 AND a.status = 'pending_acceptance'`,
		latestPlanID).Row().Scan(&c.PendingAssets); err != nil {
		return "", fmt.Errorf("project: tally latest plan pending assets: %w", err)
	}
	// HITL regenerate 子资产 todo_id='' → 对上面的 "JOIN todos WHERE plan_id" 盘点
	// 不可见。以最新 plan 的资产为根，沿 parent_asset_id 递归遍历（chain v2→v3 可能，
	// 故必须递归），盘点在途 regenerate 后代。以「最新 plan 资产」为根保住多 plan 不
	// 变式：lineage 根属旧 plan 的 regenerate 子资产不得 gate 当前 review。
	if err := s.db.WithContext(ctx).Raw(`
		WITH RECURSIVE regen AS (
			SELECT a.id, a.status FROM assets a
			WHERE a.parent_asset_id IN (
				SELECT a2.id FROM assets a2 JOIN todos t ON a2.todo_id = t.id WHERE t.plan_id = $1
			)
			UNION ALL
			SELECT a.id, a.status FROM assets a JOIN regen r ON a.parent_asset_id = r.id
		)
		SELECT count(*) FILTER (WHERE status IN ('generating','submitted','pending_acceptance')) FROM regen`,
		latestPlanID).Row().Scan(&c.InFlightRegen); err != nil {
		return "", fmt.Errorf("project: tally latest plan in-flight regenerate descendants: %w", err)
	}
	status := DeriveStatus(c)
	// canceled 终态守卫：worker 的自动回刷（本函数）绝不能把已取消项目移出 canceled。
	// 取消-扇出竞态里逃逸的子 asset todo 出图后 MarkDone→RefreshStatus 会算出非终态→"running"，
	// 无条件写就把 canceled 覆盖回 running（canceled 自我复活）。用条件 UPDATE 排除 canceled 行
	// 即可。显式重跑走 TryBeginRun 的独立 CAS（canceled→planning，不经此路径），不受此守卫影响。
	res := s.db.WithContext(ctx).Exec(
		`UPDATE projects SET status=$2, updated_at=now() WHERE id=$1 AND status <> 'canceled'`,
		projectID, status)
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		// 0 行 = 项目已处于 canceled 终态（被 WHERE 排除）→ 不改写，返回真实当前值。
		return s.currentStatus(ctx, projectID)
	}
	return status, nil
}

// currentStatus returns the project's status column without modification.
// Used by RefreshStatus to return the un-touched value when there are no
// plans yet (so the function's return semantics stay uniform for callers).
func (s *Store) currentStatus(ctx context.Context, projectID string) (string, error) {
	var status string
	err := s.db.WithContext(ctx).Raw(
		`SELECT status FROM projects WHERE id=$1`, projectID).Row().Scan(&status)
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
	// 仅「在途」项目可取消：先用条件 UPDATE 做 CAS，把 planning/running 翻到 canceled。
	// 非在途态（draft/review/completed/failed/已 canceled）0 行影响 → 返回 ErrNotCancelable，
	// 由 handler 映射 409，杜绝 API 直连把静止项目强打 canceled（前端 canCancel 已门控 UI，但
	// API 须 fail-safe）。与 TryBeginRun 的 CAS 风格对齐。projects 行的排他锁也与扇出事务的
	// SELECT ... FOR UPDATE 串行化：本 UPDATE 一旦提交，随后的 todo 扫帚必能扫到扇出刚落库的子 todo。
	res := s.db.WithContext(ctx).Exec(
		`UPDATE projects SET status='canceled', updated_at=now()
		 WHERE id=$1 AND status IN ('planning','running')`, projectID)
	if res.Error != nil {
		return fmt.Errorf("project: cancel: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotCancelable
	}
	// 已翻 canceled，扫在途 todo/asset（原语义）。
	if err := s.db.WithContext(ctx).Exec(
		`UPDATE todos SET status='canceled', locked_by='', locked_until=NULL, updated_at=now()
		 WHERE project_id=$1 AND status IN ('pending','ready','blocked','running')`, projectID).Error; err != nil {
		return fmt.Errorf("project: cancel todos: %w", err)
	}
	if err := s.db.WithContext(ctx).Exec(
		`UPDATE assets SET status='canceled' WHERE project_id=$1 AND status IN ('generating','submitted')`, projectID).Error; err != nil {
		return fmt.Errorf("project: cancel assets: %w", err)
	}
	return nil
}

// Deleted reports whether a project row is soft-deleted (deleted_at 非空)。
// Missing row → ErrNotFound。httpapi 的 requireLiveProject 门禁用它把已删项目
// 从一切 project-scoped 路由上 404 掉（RBAC 之后，防跨租户枚举）。
func (s *Store) Deleted(ctx context.Context, id string) (bool, error) {
	var deleted bool
	err := s.db.WithContext(ctx).Raw(
		`SELECT deleted_at IS NOT NULL FROM projects WHERE id=$1`, id).Row().Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return deleted, err
}

// SoftDelete tombstones a project (deleted_at=now()) and cascade-cancels its
// in-flight work in ONE transaction (docs/specs/project-delete.md §1):
//   - todos: 复用 Cancel 的语义（pending/ready/blocked/running → canceled，清租约）；
//     worker 正持租约执行的 todo 由 MarkDone/MarkFailed 的 status 守卫幂等收口。
//   - assets: in-flight（generating/submitted）→ canceled，与 Cancel 同一把扫帚。
//   - export_jobs: 在途（pending/running）→ 复用其状态机的 failed 终态（该状态机无
//     canceled；running 持有者随后的 MarkDone/MarkFailed 因 status 守卫 no-op）。
//   - generations 计费账本与 blob 字节刻意不动（账单保留 / blob GC 不做）。
//
// 幂等：已删除或不存在 → ErrNotFound（0 行 tombstone），与仓库 DELETE 端点对
// missing 资源一致返 404 的惯例对齐。
func (s *Store) SoftDelete(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`UPDATE projects SET deleted_at=now(), updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, id)
		if res.Error != nil {
			return fmt.Errorf("project: soft delete: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := tx.Exec(
			`UPDATE todos SET status='canceled', locked_by='', locked_until=NULL, updated_at=now()
			 WHERE project_id=$1 AND status IN ('pending','ready','blocked','running')`, id).Error; err != nil {
			return fmt.Errorf("project: soft delete: cancel todos: %w", err)
		}
		if err := tx.Exec(
			`UPDATE assets SET status='canceled' WHERE project_id=$1 AND status IN ('generating','submitted')`, id).Error; err != nil {
			return fmt.Errorf("project: soft delete: cancel assets: %w", err)
		}
		if err := tx.Exec(
			`UPDATE export_jobs SET status='failed', error='project deleted',
			        locked_by='', locked_until=NULL, updated_at=now()
			 WHERE project_id=$1 AND status IN ('pending','running')`, id).Error; err != nil {
			return fmt.Errorf("project: soft delete: cancel export jobs: %w", err)
		}
		if err := tx.Exec(
			`UPDATE projects SET status='canceled', updated_at=now() WHERE id=$1`, id).Error; err != nil {
			return fmt.Errorf("project: soft delete: set status: %w", err)
		}
		return nil
	})
}

// Plan represents a run/plan for a project.
type Plan struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"projectId"`
	Status       string    `json:"status"`
	Valid        bool      `json:"valid"`
	FallbackUsed bool      `json:"fallbackUsed"`
	CreatedAt    time.Time `json:"createdAt"`
	// WorkflowID 为该 plan 所属自定义工作流的 id（COALESCE 空 = 项目级默认管线，
	// 无关联工作流）。供前端把自定义 run 直接定向到画布运行模式。
	WorkflowID string `json:"workflowId"`
}

// ListPlans lists all plans/runs for a specific project.
func (s *Store) ListPlans(ctx context.Context, projectID string) ([]Plan, error) {
	q := `
		SELECT p.id, p.project_id, p.valid, p.fallback_used, p.created_at,
		       COALESCE(p.workflow_id, ''),
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

	rows, err := s.db.WithContext(ctx).Raw(q, projectID).Rows()
	if err != nil {
		return nil, fmt.Errorf("project: list plans: %w", err)
	}
	defer rows.Close()

	var out []Plan
	for rows.Next() {
		var p Plan
		var c TodoCounts
		if err := rows.Scan(
			&p.ID, &p.ProjectID, &p.Valid, &p.FallbackUsed, &p.CreatedAt, &p.WorkflowID,
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
	if err := s.db.WithContext(ctx).Raw(
		`SELECT COALESCE(max(seq), 0) FROM run_events WHERE project_id=$1`, projectID).Row().
		Scan(&in.Version); err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state version: %w", err)
	}

	// resolve plan: when planID is provided use that plan (scoped to project);
	// otherwise fall back to the latest plan for this project.
	var planRowID, workflowID string
	var valid, fallbackUsed bool
	if planID == "" {
		err = s.db.WithContext(ctx).Raw(
			`SELECT id, valid, fallback_used, COALESCE(workflow_id,'') FROM plans WHERE project_id=$1 ORDER BY created_at DESC LIMIT 1`,
			projectID).Row().Scan(&planRowID, &valid, &fallbackUsed, &workflowID)
	} else {
		err = s.db.WithContext(ctx).Raw(
			`SELECT id, valid, fallback_used, COALESCE(workflow_id,'') FROM plans WHERE id=$1 AND project_id=$2`,
			planID, projectID).Row().Scan(&planRowID, &valid, &fallbackUsed, &workflowID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return projectstate.Compute(in), nil // no plan / not found: draft passthrough
	}
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state plan: %w", err)
	}
	in.HasPlan = true
	in.Plan = &projectstate.Plan{PlanID: planRowID, Valid: valid, FallbackUsed: fallbackUsed}
	in.WorkflowID = workflowID

	// todos of the resolved plan
	rows, err := s.db.WithContext(ctx).Raw(
		`SELECT id, type, status, COALESCE(error,''), depends_on, created_at FROM todos WHERE plan_id=$1 ORDER BY updated_at ASC`, planRowID).Rows()
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state todos: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var t projectstate.Todo
		var deps pq.StringArray
		if err := rows.Scan(&t.ID, &t.Type, &t.Status, &t.Error, &deps, &t.CreatedAt); err != nil {
			return projectstate.ProjectState{}, fmt.Errorf("project: scan state todo: %w", err)
		}
		t.DependsOn = []string(deps)
		in.Todos = append(in.Todos, t)
	}
	if err := rows.Err(); err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state todos rows: %w", err)
	}

	// assets of the resolved plan (joined via todos), PLUS in-flight HITL
	// regenerate descendants of those assets. Regenerate children carry
	// todo_id='' so the JOIN-via-todos set misses them; the recursive CTE walks
	// parent_asset_id down from this plan's assets so Compute sees the in-flight
	// signal (todo_id='' + generating/submitted/pending_acceptance) and keeps the
	// run in 'review'. Rooting at THIS plan's assets preserves the multi-plan
	// invariant. The two sets are disjoint (plan assets have todo_id<>'';
	// descendants have todo_id=''), so no double-count; descendants don't match
	// any todo in assetByTodo, so they never become pips. created_at ordering is
	// kept for the plan-asset rows (the in-flight descendants only feed the
	// status tally, not pip layout).
	arows, err := s.db.WithContext(ctx).Raw(
		`WITH RECURSIVE plan_assets AS (
			SELECT a.id, a.todo_id, a.status, a.created_at FROM assets a
			JOIN todos t ON a.todo_id = t.id
			WHERE t.plan_id = $1
		), regen AS (
			SELECT a.id, a.todo_id, a.status, a.created_at FROM assets a
			WHERE a.parent_asset_id IN (SELECT id FROM plan_assets)
			UNION ALL
			SELECT a.id, a.todo_id, a.status, a.created_at FROM assets a
			JOIN regen r ON a.parent_asset_id = r.id
		)
		SELECT id, todo_id, status, created_at FROM plan_assets
		UNION ALL
		SELECT id, todo_id, status, created_at FROM regen
		ORDER BY created_at ASC`, planRowID).Rows()
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state assets: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var a projectstate.Asset
		var createdAt time.Time
		if err := arows.Scan(&a.ID, &a.TodoID, &a.Status, &createdAt); err != nil {
			return projectstate.ProjectState{}, fmt.Errorf("project: scan state asset: %w", err)
		}
		in.Assets = append(in.Assets, a)
	}
	if err := arows.Err(); err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load state assets rows: %w", err)
	}

	// custom 节点产物 (node_outputs)，按本 plan 的 todo 关联 (T3 运行视图最小面板 +
	// P5d per-item inspector items[])。一个 todo 可有多行 node_outputs（items-only 行
	// + 旧 content 行）；DISTINCT ON 每 todo 取一行，排序键 (OQ2 tie-break)：
	//   1. items 非空 ('[]'::jsonb 视为空) 优先 —— 让 inspector 拿到带 items 的行，
	//      即便它不是该 todo 最新的那行（最新行可能只是无 items 的 content 行）；
	//   2. 再按 created_at DESC —— 多个带 items 的行里取最新。
	// items 逐字读出为 JSONB（→ json.RawMessage），在 store 层 Unmarshal（保持
	// Compute 纯净，不在 Compute 里做 I/O/反序列化）。查询仍按 t.plan_id 限定，
	// 不扩大 project/org scope（无新暴露面）。
	norows, err := s.db.WithContext(ctx).Raw(
		`SELECT DISTINCT ON (no.todo_id) no.todo_id, no.content, no.format, no.items
		 FROM node_outputs no
		 JOIN todos t ON no.todo_id = t.id
		 WHERE t.plan_id=$1
		 ORDER BY no.todo_id, (no.items <> '[]'::jsonb) DESC, no.created_at DESC`, planRowID).Rows()
	if err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: load node outputs: %w", err)
	}
	defer norows.Close()
	for norows.Next() {
		var o projectstate.NodeOutput
		var itemsRaw []byte
		if err := norows.Scan(&o.TodoID, &o.Content, &o.Format, &itemsRaw); err != nil {
			return projectstate.ProjectState{}, fmt.Errorf("project: scan node output: %w", err)
		}
		// items 逐字透传：解析 JSONB bytes 成 []InspectorItem（NOT 重新解析每个
		// item.json —— P2a 已落地解析后的对象，重解析会重新引入 M-2）。'[]' → 空切片。
		if len(itemsRaw) > 0 {
			if err := json.Unmarshal(itemsRaw, &o.Items); err != nil {
				return projectstate.ProjectState{}, fmt.Errorf("project: unmarshal node output items (todo %s): %w", o.TodoID, err)
			}
		}
		// 空 items（旧 content-only 行或 '[]'）→ 归一为 nil，让 GraphNode.Items 的
		// omitempty 真正省略 JSON 键（避免出 "items":[]，对齐"无 inspector 数据"）。
		if len(o.Items) == 0 {
			o.Items = nil
		}
		in.Outputs = append(in.Outputs, o)
	}
	if err := norows.Err(); err != nil {
		return projectstate.ProjectState{}, fmt.Errorf("project: node outputs rows: %w", err)
	}

	return projectstate.Compute(in), nil
}
