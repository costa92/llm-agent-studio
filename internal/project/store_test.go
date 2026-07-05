package project

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/projectstate"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// uniqueSuffix 返回 3-byte 十六进制后缀，让共享测试池里的硬编码 ID（如 org_id
// / plan_id）每跑一次都不同，避免先前 test 残留数据撞唯一约束。
func uniqueSuffix() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	s, pool, _ := newStoreG(t)
	return s, pool
}

// newStoreG additionally exposes the GORM handle（软删测试用它构造 cost.Store，
// 断言 org 成本聚合仍含已删项目历史）。
func newStoreG(t *testing.T) (*Store, *pgxpool.Pool, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run project tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(st.GORM()), st.Pool(), st.GORM()
}

func TestCreateGetListProject(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	// 用唯一 org_id：测试池共享，硬编码 "org-x" 会让 ListByOrg 把之前 run
	// 残留的 project 一起数进来（旧 test pollution 让 len 跑成 4 而不是 1）。
	orgID := "org_list_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{
		OrgID: orgID, Name: "Promo", Brief: "a promo", ContentType: "ad",
		TargetPlatform: "tiktok", Style: "cyberpunk", CreatedBy: "u1",
		PlannerProvider: "minimax", PlannerModel: "minimax-text-01",
		StorageMode:     "oss",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Status != "draft" {
		t.Fatalf("new project status=%q want draft", p.Status)
	}
	if p.PlannerProvider != "minimax" || p.PlannerModel != "minimax-text-01" {
		t.Fatalf("planner override not persisted: %+v", p)
	}
	if p.StorageMode != "oss" {
		t.Fatalf("storage mode not persisted: %+v", p)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Promo" || got.OrgID != orgID {
		t.Fatalf("get mismatch: %+v", got)
	}
	if got.PlannerProvider != "minimax" || got.PlannerModel != "minimax-text-01" {
		t.Fatalf("get: planner override not round-tripped: %+v", got)
	}
	if got.StorageMode != "oss" {
		t.Fatalf("get: storage mode not round-tripped: %+v", got)
	}
	items, _, err := s.ListByOrg(ctx, orgID, 50, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("list len=%d want 1", len(items))
	}
	if items[0].PlannerProvider != "minimax" {
		t.Fatalf("list: planner override missing in projection: %+v", items[0])
	}
	if items[0].StorageMode != "oss" {
		t.Fatalf("list: storage mode missing in projection: %+v", items[0])
	}
}

// TestProjectKindRoundTrip: kind 经 Create 写入，经 Get 读回。绘本/standard 管线
// 已移除；kind 仅剩 'custom'。
func TestProjectKindRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	orgID := "org_kind_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{
		OrgID: orgID, Name: "Custom", CreatedBy: "u",
		Kind: "custom",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Kind != "custom" {
		t.Fatalf("create Kind=%q want custom", p.Kind)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Kind != "custom" {
		t.Fatalf("get Kind=%q want custom", got.Kind)
	}

	// Kind 缺省时默认写 'custom'（前端创建项目不传 kind，走的正是这条路径）。
	def, err := s.Create(ctx, CreateInput{
		OrgID: orgID, Name: "Default", CreatedBy: "u",
	})
	if err != nil {
		t.Fatalf("create default: %v", err)
	}
	if def.Kind != "custom" {
		t.Fatalf("default Kind=%q want custom", def.Kind)
	}
	gotDef, err := s.Get(ctx, def.ID)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if gotDef.Kind != "custom" {
		t.Fatalf("get default Kind=%q want custom", gotDef.Kind)
	}
}

// M5.1: Update 改 planner_provider/planner_model，其他字段不动；找不到 id → 404。
func TestUpdatePlannerOverride(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	orgID := "org_upd_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{
		OrgID: orgID, Name: "X", CreatedBy: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 初值空 → 改 → 拿到新值。
	upd, err := s.Update(ctx, p.ID, UpdateInput{
		Name:            "X",
		PlannerProvider: "ollama", PlannerModel: "gemma4:26b",
		StorageMode: "github",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.PlannerProvider != "ollama" || upd.PlannerModel != "gemma4:26b" || upd.StorageMode != "github" {
		t.Fatalf("update did not persist: %+v", upd)
	}
	// Get 拿到的也是新值。
	got, _ := s.Get(ctx, p.ID)
	if got.PlannerProvider != "ollama" || got.StorageMode != "github" {
		t.Fatalf("get after update: %+v", got)
	}
	// 清回空（name 仍需带上，否则会被写空）。
	cleared, err := s.Update(ctx, p.ID, UpdateInput{Name: "X", PlannerProvider: "", PlannerModel: "", StorageMode: ""})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cleared.PlannerProvider != "" || cleared.PlannerModel != "" || cleared.StorageMode != "" {
		t.Fatalf("clear did not reset: %+v", cleared)
	}
	// 找不到 → ErrNotFound。
	if _, err := s.Update(ctx, "nope", UpdateInput{Name: "x", PlannerProvider: "x", StorageMode: "localfs"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}
}

// 基本信息（名称/创意需求/内容类型/目标平台/风格）可原地编辑并持久化。
func TestUpdateBasicInfo(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	orgID := "org_updinfo_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{
		OrgID: orgID, Name: "旧名", Brief: "旧需求",
		ContentType: "短视频", TargetPlatform: "抖音", Style: "写实", CreatedBy: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	upd, err := s.Update(ctx, p.ID, UpdateInput{
		Name: "新名", Description: "新需求",
		ContentType: "广告片", TargetPlatform: "B 站", Style: "动画",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "新名" || upd.Description != "新需求" ||
		upd.ContentType != "广告片" || upd.TargetPlatform != "B 站" || upd.Style != "动画" {
		t.Fatalf("basic info not persisted: %+v", upd)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.Name != "新名" || got.Style != "动画" {
		t.Fatalf("get after update: %+v", got)
	}
}

// M14: SetCover links/clears a project's cover_asset_id; missing id → ErrNotFound.
func TestSetCover(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	orgID := "org_cover_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "Cov", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.CoverAssetID != "" {
		t.Fatalf("new project should have empty cover: %+v", p)
	}
	if err := s.SetCover(ctx, p.ID, "a1"); err != nil {
		t.Fatalf("set cover: %v", err)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CoverAssetID != "a1" {
		t.Fatalf("cover not persisted: %+v", got)
	}
	// Clear.
	if err := s.SetCover(ctx, p.ID, ""); err != nil {
		t.Fatalf("clear cover: %v", err)
	}
	cleared, _ := s.Get(ctx, p.ID)
	if cleared.CoverAssetID != "" {
		t.Fatalf("cover not cleared: %+v", cleared)
	}
	// Missing project → ErrNotFound.
	if err := s.SetCover(ctx, "missing", "a1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}
}

func TestCancelSweepsGeneratingAssets(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "org_cancel", Name: "P", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// One in-flight sync asset ('generating'), one in-flight async asset
	// ('submitted' — external job running), one already awaiting review.
	if _, err := pool.Exec(ctx, `INSERT INTO assets (id, project_id, status) VALUES
		(md5(random()::text), $1, 'generating'),
		(md5(random()::text), $1, 'submitted'),
		(md5(random()::text), $1, 'pending_acceptance')`, p.ID); err != nil {
		t.Fatalf("seed assets: %v", err)
	}
	if err := s.Cancel(ctx, p.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var canceled, pending int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='canceled'`, p.ID).Scan(&canceled)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='pending_acceptance'`, p.ID).Scan(&pending)
	// F1: BOTH 'generating' (sync) AND 'submitted' (async) in-flight assets must be
	// swept to 'canceled' — a submitted asset stranded otherwise (spec §5.4 必修).
	if canceled != 2 {
		t.Fatalf("generating + submitted assets should be canceled, got %d canceled (want 2)", canceled)
	}
	// Decision: pending_acceptance assets stay reviewable (real money was spent;
	// HITL accept/reject still applies after a cancel).
	if pending != 1 {
		t.Fatalf("pending_acceptance asset must survive cancel, got %d pending", pending)
	}
}

// 真实生产事故：项目跑挂（plan A）→ 再跑一次成功（plan B，5/5 done + 2 待审）→
// 项目 status 应解析为 "review"（=plan B 状态）。但旧 RefreshStatus 把所有 plan 的
// todos 全累加——plan A 有 1 failed 永远压住，"failed" 一票否决。这个 case 保护
// 新逻辑确实按"最新 plan 维度"聚合，没让历史失败污染当前状态。
func TestRefreshStatusScopesToLatestPlan(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{
		OrgID: "org-scope", Name: "E2E", Brief: "b", CreatedBy: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 用项目 ID 后缀做唯一 ID（测试池是共享的，硬编码会撞唯一约束）。
	plnOld := "pln_old_" + p.ID
	plnNew := "pln_new_" + p.ID
	// Plan A（旧）: 1 个失败 todo + 1 个 done todo。
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'failed', false, false, now() - interval '5 minutes')`, plnOld, p.ID); err != nil {
		t.Fatalf("insert old plan: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES
		 ($1, $2, $3, 'script', 'failed'),
		 ($4, $2, $3, 'script', 'done')`,
		"todo_old_fail_"+p.ID, p.ID, plnOld, "todo_old_done_"+p.ID); err != nil {
		t.Fatalf("insert old todos: %v", err)
	}
	// Plan B（最新）: 3 个 done todo + 2 个待审资产——应解析为 "review"。
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'review', true, false, now())`, plnNew, p.ID); err != nil {
		t.Fatalf("insert new plan: %v", err)
	}
	for i, id := range []string{"a", "b", "c"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES
			 ($1, $2, $3, 'asset', 'done')`,
			"todo_new_"+id+"_"+p.ID, p.ID, plnNew); err != nil {
			t.Fatalf("insert new todo %d: %v", i, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, type, status) VALUES
		 ($1, $2, $3, 'image', 'pending_acceptance'),
		 ($4, $2, $5, 'image', 'pending_acceptance')`,
		"as_new_1_"+p.ID, p.ID, "todo_new_a_"+p.ID,
		"as_new_2_"+p.ID, "todo_new_b_"+p.ID); err != nil {
		t.Fatalf("insert new assets: %v", err)
	}

	// 旧实现：累加两 plan 全部 todos → Failed=1 → 返 "failed"。
	// 新实现：只取最新 plan todos → Failed=0 / Done=3 / PendingAssets=2 → 返 "review"。
	got, err := s.RefreshStatus(ctx, p.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got != "review" {
		t.Fatalf("project status=%q want review (latest plan drives status; old failed run must not poison)", got)
	}
	// 二次 RefreshStatus 仍稳：幂等。
	got2, _ := s.RefreshStatus(ctx, p.ID)
	if got2 != "review" {
		t.Fatalf("second refresh status=%q want review", got2)
	}
}

// HITL regenerate 子资产 todo_id='' 对所有 "JOIN todos WHERE plan_id" 的盘点不可见。
// 当最新 plan 的 todos 全 done、资产全 accepted，但有一个 regenerate 子资产仍在
// 生成（generating/submitted/pending_acceptance），项目应保持 "review" 而非误判
// "completed"。新逻辑用 parent_asset_id 递归遍历，从最新 plan 的资产为根盘点在途
// regenerate 后代。
func TestRefreshStatus_RegenerateChildKeepsReview(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "org-regen", Name: "Regen", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	planID := "pln_regen_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'review', true, false, now())`, planID, p.ID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	// 2 asset todos 全 done。
	for _, id := range []string{"a", "b"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1, $2, $3, 'asset', 'done')`,
			"todo_regen_"+id+"_"+p.ID, p.ID, planID); err != nil {
			t.Fatalf("insert todo %s: %v", id, err)
		}
	}
	// 资产全 accepted（最新 plan 维度无 pending_acceptance）。
	parentAssetID := "as_regen_parent_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, type, status) VALUES
		 ($1, $2, $3, 'image', 'accepted'),
		 ($4, $2, $5, 'image', 'accepted')`,
		parentAssetID, p.ID, "todo_regen_a_"+p.ID,
		"as_regen_b_"+p.ID, "todo_regen_b_"+p.ID); err != nil {
		t.Fatalf("insert accepted assets: %v", err)
	}
	// regenerate 子资产：parent_asset_id 指向最新 plan 的资产，todo_id='', 仍在生成。
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, parent_asset_id, type, status) VALUES
		 ($1, $2, '', $3, 'image', 'generating')`,
		"as_regen_child_"+p.ID, p.ID, parentAssetID); err != nil {
		t.Fatalf("insert regenerate child: %v", err)
	}

	got, err := s.RefreshStatus(ctx, p.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got != "review" {
		t.Fatalf("project status=%q want review (in-flight regenerate child must keep review)", got)
	}
	// 幂等。
	got2, _ := s.RefreshStatus(ctx, p.ID)
	if got2 != "review" {
		t.Fatalf("second refresh status=%q want review", got2)
	}
}

// 负向（非可选）：regenerate 子资产其 lineage 根属于「旧/被取代」plan 的资产时，
// 绝不能 gate 当前 review——否则旧 run 会借递归遍历复活。这里最新 plan 全 done +
// accepted（无 pending），其状态应为 "completed"；旧 plan 下挂一个在途 regenerate
// 子资产不得把它拉回 "review"。证明递归遍历严格以「最新 plan 资产」为根。
func TestRefreshStatus_OldPlanRegenerateChildIgnored(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "org-regen-old", Name: "RegenOld", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 旧 plan：一个 asset todo done + 一个 accepted 资产，其下挂一个在途 regenerate 子资产。
	oldPlan := "pln_old_regen_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'review', true, false, now() - interval '5 minutes')`, oldPlan, p.ID); err != nil {
		t.Fatalf("insert old plan: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1, $2, $3, 'asset', 'done')`,
		"todo_old_regen_"+p.ID, p.ID, oldPlan); err != nil {
		t.Fatalf("insert old todo: %v", err)
	}
	oldParentAsset := "as_old_parent_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, type, status) VALUES ($1, $2, $3, 'image', 'accepted')`,
		oldParentAsset, p.ID, "todo_old_regen_"+p.ID); err != nil {
		t.Fatalf("insert old accepted asset: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, parent_asset_id, type, status) VALUES
		 ($1, $2, '', $3, 'image', 'generating')`,
		"as_old_child_"+p.ID, p.ID, oldParentAsset); err != nil {
		t.Fatalf("insert old regenerate child: %v", err)
	}
	// 最新 plan：一个 asset todo done + 一个 accepted 资产，无 pending、无 regenerate。
	newPlan := "pln_new_regen_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'completed', true, false, now())`, newPlan, p.ID); err != nil {
		t.Fatalf("insert new plan: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1, $2, $3, 'asset', 'done')`,
		"todo_new_regen_"+p.ID, p.ID, newPlan); err != nil {
		t.Fatalf("insert new todo: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, type, status) VALUES ($1, $2, $3, 'image', 'accepted')`,
		"as_new_accepted_"+p.ID, p.ID, "todo_new_regen_"+p.ID); err != nil {
		t.Fatalf("insert new accepted asset: %v", err)
	}

	got, err := s.RefreshStatus(ctx, p.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got != "completed" {
		t.Fatalf("project status=%q want completed (old-plan regenerate child must NOT gate latest plan)", got)
	}
}

// TestLoadState_RegenerateChildVisible: an in-flight regenerate child (todo_id='',
// parent_asset_id rooting in the latest plan, status generating) must reach
// Compute so the derived status is 'review' (not 'completed').
func TestLoadState_RegenerateChildVisible(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_regen_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LS-Regen", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	planID := "pln_ls_regen_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'review', true, false, now())`, planID, p.ID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	todoID := "todo_ls_regen_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1, $2, $3, 'asset', 'done')`,
		todoID, p.ID, planID); err != nil {
		t.Fatalf("insert todo: %v", err)
	}
	parentAssetID := "as_ls_parent_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, type, status) VALUES ($1, $2, $3, 'image', 'accepted')`,
		parentAssetID, p.ID, todoID); err != nil {
		t.Fatalf("insert accepted asset: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, todo_id, parent_asset_id, type, status) VALUES
		 ($1, $2, '', $3, 'image', 'generating')`,
		"as_ls_child_"+p.ID, p.ID, parentAssetID); err != nil {
		t.Fatalf("insert regenerate child: %v", err)
	}
	st, err := s.LoadState(ctx, p.ID, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.Status != "review" {
		t.Fatalf("LoadState status=%q want review (in-flight regenerate child must be visible)", st.Status)
	}
}

// 边界：无任何 plan 的项目不要被 RefreshStatus 改成 "planning"（DeriveStatus 对空
// TodoCounts 返 "planning"，但项目初始 status 是 "draft"，且未被运行的项目不该被
// 这次刷新改写）。覆盖 latestPlanTodoCounts 的 "no plans" 分支。
func TestRefreshStatusNoPlansKeepsStatus(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{
		OrgID: "org-nopln", Name: "Empty", CreatedBy: "u",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Status != "draft" {
		t.Fatalf("new project status=%q want draft", p.Status)
	}
	// RefreshStatus 看到没有 plan：保持现状不写。
	got, err := s.RefreshStatus(ctx, p.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got != "draft" {
		t.Fatalf("no-plan refresh status=%q want draft (unchanged)", got)
	}
	// 确认 DB 里的 status 还是 draft（没被改写）。
	cur, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur.Status != "draft" {
		t.Fatalf("db status after refresh=%q want draft", cur.Status)
	}
}

func TestOrgIDForProject(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "org-y", Name: "X", CreatedBy: "u1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	org, err := s.OrgIDForProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("org lookup: %v", err)
	}
	if org != "org-y" {
		t.Fatalf("org=%q want org-y", org)
	}
}

func TestCancelSweepsSubmittedAssets(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{OrgID: "org_cancel_sub", Name: "P", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// One submitted (external job in flight), one generating, one pending.
	if _, err := pool.Exec(ctx, `INSERT INTO assets (id, project_id, status) VALUES
		(md5(random()::text), $1, 'submitted'),
		(md5(random()::text), $1, 'generating'),
		(md5(random()::text), $1, 'pending_acceptance')`, p.ID); err != nil {
		t.Fatalf("seed assets: %v", err)
	}
	if err := s.Cancel(ctx, p.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var canceled, pending int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='canceled'`, p.ID).Scan(&canceled)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='pending_acceptance'`, p.ID).Scan(&pending)
	if canceled != 2 {
		t.Fatalf("submitted + generating must both be canceled, got %d canceled", canceled)
	}
	if pending != 1 {
		t.Fatalf("pending_acceptance must survive cancel, got %d", pending)
	}
}

// TestLoadState_NoPlan_Draft: a newly created project (no plans) must return a
// ProjectState with Status=="draft", RunStatus=="idle", and ProjectID==p.ID.
// Guards the no-plan passthrough branch in LoadState.
func TestLoadState_NoPlan_Draft(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_noplan_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LS-NoPlan", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	st, err := s.LoadState(ctx, p.ID, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.ProjectID != p.ID {
		t.Errorf("ProjectID=%q want %q", st.ProjectID, p.ID)
	}
	if st.Status != "draft" {
		t.Errorf("Status=%q want draft", st.Status)
	}
	if st.RunStatus != "idle" {
		t.Errorf("RunStatus=%q want idle", st.RunStatus)
	}
}

// TestLoadState_WithTodos: a project with a plan + a script todo must surface
// the script stage's status correctly (one script todo in status 'running' →
// script stage status 'running', RunStatus 'running').
func TestLoadState_WithTodos(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_todos_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LS-Todos", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	planID := "pln_ls_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'running', true, false, now())`, planID, p.ID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	todoID := "todo_ls_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1, $2, $3, 'script', 'running')`,
		todoID, p.ID, planID); err != nil {
		t.Fatalf("insert todo: %v", err)
	}
	st, err := s.LoadState(ctx, p.ID, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.ProjectID != p.ID {
		t.Errorf("ProjectID=%q want %q", st.ProjectID, p.ID)
	}
	if st.RunStatus != "running" {
		t.Errorf("RunStatus=%q want running", st.RunStatus)
	}
	// Find the script stage.
	var scriptStage *projectstate.Stage
	for i := range st.Stages {
		if st.Stages[i].Role == "script" {
			scriptStage = &st.Stages[i]
		}
	}
	if scriptStage == nil {
		t.Fatalf("script stage missing in %+v", st.Stages)
	}
	if scriptStage.Status != "running" {
		t.Errorf("script stage status=%q want running", scriptStage.Status)
	}
	if scriptStage.TodoID != todoID {
		t.Errorf("script stage TodoID=%q want %q", scriptStage.TodoID, todoID)
	}
}

// TestLoadState_SpecificPlan: two plans exist (older has a failed script todo,
// newer has a running script todo). LoadState with the older plan's ID must
// reflect the older plan's state; LoadState with "" must reflect the newer
// plan (latest). Pins the regression fix: historical pages must not show
// the latest run's state.
func TestLoadState_SpecificPlan(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_specific_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LS-Specific", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// older plan with a failed script todo
	olderPlanID := "pln_older_" + uniqueSuffix()
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'failed', true, false, now() - interval '10 minutes')`,
		olderPlanID, p.ID); err != nil {
		t.Fatalf("insert older plan: %v", err)
	}
	olderTodoID := "todo_older_" + uniqueSuffix()
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1, $2, $3, 'script', 'failed')`,
		olderTodoID, p.ID, olderPlanID); err != nil {
		t.Fatalf("insert older todo: %v", err)
	}

	// newer plan with a running script todo
	newerPlanID := "pln_newer_" + uniqueSuffix()
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'running', true, false, now())`,
		newerPlanID, p.ID); err != nil {
		t.Fatalf("insert newer plan: %v", err)
	}
	newerTodoID := "todo_newer_" + uniqueSuffix()
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1, $2, $3, 'script', 'running')`,
		newerTodoID, p.ID, newerPlanID); err != nil {
		t.Fatalf("insert newer todo: %v", err)
	}

	// LoadState with specific older plan ID must reflect the older plan.
	older, err := s.LoadState(ctx, p.ID, olderPlanID)
	if err != nil {
		t.Fatalf("LoadState(olderPlanID): %v", err)
	}
	if older.Plan == nil || older.Plan.PlanID != olderPlanID {
		t.Errorf("older: Plan.PlanID=%v want %q", older.Plan, olderPlanID)
	}
	var olderScript *projectstate.Stage
	for i := range older.Stages {
		if older.Stages[i].Role == "script" {
			olderScript = &older.Stages[i]
		}
	}
	if olderScript == nil {
		t.Fatalf("older: script stage missing in %+v", older.Stages)
	}
	if olderScript.Status != "failed" {
		t.Errorf("older: script stage status=%q want failed", olderScript.Status)
	}
	if olderScript.TodoID != olderTodoID {
		t.Errorf("older: script stage TodoID=%q want %q", olderScript.TodoID, olderTodoID)
	}

	// LoadState with "" must reflect the newer (latest) plan.
	latest, err := s.LoadState(ctx, p.ID, "")
	if err != nil {
		t.Fatalf("LoadState(latest): %v", err)
	}
	if latest.Plan == nil || latest.Plan.PlanID != newerPlanID {
		t.Errorf("latest: Plan.PlanID=%v want %q", latest.Plan, newerPlanID)
	}
	if latest.RunStatus != "running" {
		t.Errorf("latest: RunStatus=%q want running", latest.RunStatus)
	}
}

// TestLoadState_UnknownPlan_Passthrough: requesting an unknown planID (or a
// planID that belongs to a different project) must return the draft passthrough
// state without error (HasPlan==false semantics).
func TestLoadState_UnknownPlan_Passthrough(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_unk_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LS-Unknown", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	assertPassthrough := func(label, planID string) {
		st, err := s.LoadState(ctx, p.ID, planID)
		if err != nil {
			t.Fatalf("LoadState(%s): %v", label, err)
		}
		if st.ProjectID != p.ID {
			t.Errorf("%s: ProjectID=%q want %q", label, st.ProjectID, p.ID)
		}
		if st.Plan != nil {
			t.Errorf("%s: Plan should be nil, got %+v", label, st.Plan)
		}
		if st.Status != "draft" {
			t.Errorf("%s: Status=%q want draft", label, st.Status)
		}
	}

	// A planID that simply does not exist → draft passthrough.
	assertPassthrough("nonexistent", "nonexistent-plan-id")

	// A planID that exists but belongs to ANOTHER project must NOT leak that
	// project's state — the WHERE project_id=$2 guard turns it into passthrough.
	otherP, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LS-Other", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	otherPlanID := "pln_other_" + uniqueSuffix()
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'running', true, false, now())`,
		otherPlanID, otherP.ID); err != nil {
		t.Fatalf("insert other-project plan: %v", err)
	}
	assertPassthrough("cross-project", otherPlanID)
}

// TestLoadState_CustomGraph: 自定义工作流(plan.workflow_id 非空 + 两 todo 带
// depends_on)→ LoadState 返回 isCustom + 正确 nodes/edges。
func TestLoadState_CustomGraph(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_graph_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LS-Graph", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wfID := "wf_ls_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO workflows (id, project_id, name, nodes) VALUES ($1,$2,'wf','[]'::jsonb)`,
		wfID, p.ID); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	planID := "pln_lsg_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, workflow_id, created_at)
		 VALUES ($1,$2,'running',true,false,$3, now())`, planID, p.ID, wfID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	scriptID := "todo_s_" + p.ID
	boardID := "todo_b_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, created_at)
		 VALUES ($1,$2,$3,'script','done','{}', now()),
		        ($4,$2,$3,'storyboard','running',ARRAY[$1], now() + interval '1 second')`,
		scriptID, p.ID, planID, boardID); err != nil {
		t.Fatalf("insert todos: %v", err)
	}
	st, err := s.LoadState(ctx, p.ID, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !st.IsCustom {
		t.Fatalf("IsCustom = false, want true (workflow_id set)")
	}
	if len(st.Nodes) != 2 {
		t.Fatalf("nodes = %+v want 2", st.Nodes)
	}
	if len(st.Edges) != 1 || st.Edges[0].From != boardID || st.Edges[0].To != scriptID {
		t.Fatalf("edges = %+v want one board→script", st.Edges)
	}
	if st.Nodes[0].ID != scriptID || st.Nodes[1].ID != boardID {
		t.Fatalf("node order = %s,%s want script,board", st.Nodes[0].ID, st.Nodes[1].ID)
	}
}

// TestListPlans_WorkflowID: ListPlans 应在每条 plan 上回填 WorkflowID
// （自定义 run = 非空工作流 id；默认管线 run = COALESCE 空串）。供前端把
// 自定义 run 直接定向到画布运行模式。
func TestListPlans_WorkflowID(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_lp_wf_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "LP-WF", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wfID := "wf_lp_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO workflows (id, project_id, name, nodes) VALUES ($1,$2,'wf','[]'::jsonb)`,
		wfID, p.ID); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	customPlan := "pln_lpc_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, workflow_id, created_at)
		 VALUES ($1,$2,'running',true,false,$3, now() + interval '1 second')`,
		customPlan, p.ID, wfID); err != nil {
		t.Fatalf("insert custom plan: %v", err)
	}
	defaultPlan := "pln_lpd_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1,$2,'running',true,false, now())`, defaultPlan, p.ID); err != nil {
		t.Fatalf("insert default plan: %v", err)
	}
	plans, err := s.ListPlans(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	got := map[string]string{}
	for _, pl := range plans {
		got[pl.ID] = pl.WorkflowID
	}
	if got[customPlan] != wfID {
		t.Fatalf("custom plan WorkflowID = %q want %q", got[customPlan], wfID)
	}
	if got[defaultPlan] != "" {
		t.Fatalf("default plan WorkflowID = %q want empty", got[defaultPlan])
	}
}

// TestLoadState_LegacyCustomEnabled: custom_workflow_enabled=true 但 plan
// 的 workflow_id 为 NULL(经 runHandler 的项目级自定义路径)→ 仍判 isCustom。
func TestLoadState_LegacyCustomEnabled(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "org_ls_legacy_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{
		OrgID: orgID, Name: "LS-Legacy", CreatedBy: "u", CustomWorkflowEnabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	planID := "pln_lsl_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1,$2,'running',true,false, now())`, planID, p.ID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	st, err := s.LoadState(ctx, p.ID, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !st.IsCustom {
		t.Fatalf("IsCustom = false, want true (custom_workflow_enabled)")
	}
	if len(st.Nodes) != 0 {
		t.Fatalf("Nodes = %+v want empty", st.Nodes)
	}
	if len(st.Edges) != 0 {
		t.Fatalf("Edges = %+v want empty", st.Edges)
	}
}

// insertOrgStorageConfig 直接插入一条 org-scope storage_configs 行（测试用），返回其 id。
// 供「storage_config_id 必须属于本 org」校验的测试构造真实配置。
func insertOrgStorageConfig(t *testing.T, pool *pgxpool.Pool, id, orgID string) string {
	t.Helper()
	// 唯一 mode：迁移表里有 transient 的 (org_id, mode) WHERE scope='org' 唯一索引
	// （后续迁移会 DROP，但共享测试池每次 newStore 重跑迁移会瞬时重建它）；同 org 同 mode
	// 会在重建时撞 23505。故每条配置用唯一 mode，彻底回避。
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO storage_configs (id, scope, org_id, mode, enabled) VALUES ($1, 'org', $2, $3, true)`,
		id, orgID, "m_"+uniqueSuffix()); err != nil {
		t.Fatalf("insert storage_config: %v", err)
	}
	return id
}

// TestProject_StorageConfigIDRoundTrip: storage_config_id persists through
// Create and is readable back via Get and ListByOrg. 必须引用本 org 真实存在的配置。
func TestProject_StorageConfigIDRoundTrip(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgID := "o_" + uniqueSuffix()
	cfg1 := insertOrgStorageConfig(t, pool, "cfg_"+uniqueSuffix(), orgID)
	cfg2 := insertOrgStorageConfig(t, pool, "cfg_"+uniqueSuffix(), orgID)
	p, err := s.Create(ctx, CreateInput{
		OrgID: orgID, Name: "P", CreatedBy: "u", StorageConfigID: cfg1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.StorageConfigID != cfg1 {
		t.Fatalf("create returned StorageConfigID=%q want %q", p.StorageConfigID, cfg1)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.StorageConfigID != cfg1 {
		t.Fatalf("get roundtrip StorageConfigID=%q want %q", got.StorageConfigID, cfg1)
	}
	items, _, err := s.ListByOrg(ctx, orgID, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].StorageConfigID != cfg1 {
		t.Fatalf("list roundtrip StorageConfigID=%q want %q", items[0].StorageConfigID, cfg1)
	}
	// Update should persist a new value.
	upd, err := s.Update(ctx, p.ID, UpdateInput{Name: "P", StorageConfigID: cfg2})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.StorageConfigID != cfg2 {
		t.Fatalf("update StorageConfigID=%q want %q", upd.StorageConfigID, cfg2)
	}
}

// TestProject_StorageConfigID_RejectsForeignOrg: 创建/更新项目时 storage_config_id 必须属于
// 项目自身 org（scope='org'）。引用他 org 的配置 id 应被拒，防跨租户存储写入（资产用他 org 凭证/桶）。
func TestProject_StorageConfigID_RejectsForeignOrg(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	orgA := "oa_" + uniqueSuffix()
	orgB := "ob_" + uniqueSuffix()
	cfgA := insertOrgStorageConfig(t, pool, "cfg_"+uniqueSuffix(), orgA)
	cfgB := insertOrgStorageConfig(t, pool, "cfg_"+uniqueSuffix(), orgB)

	// Create 引用他 org（orgB）配置 → 拒。
	if _, err := s.Create(ctx, CreateInput{OrgID: orgA, Name: "P", CreatedBy: "u", StorageConfigID: cfgB}); !errors.Is(err, ErrInvalidStorageConfig) {
		t.Fatalf("create with foreign storage config: err=%v want ErrInvalidStorageConfig", err)
	}
	// Create 本 org 配置 → 成功。
	p, err := s.Create(ctx, CreateInput{OrgID: orgA, Name: "P", CreatedBy: "u", StorageConfigID: cfgA})
	if err != nil {
		t.Fatalf("create with own storage config: %v", err)
	}
	// Create 空 = 无 override → 成功。
	if _, err := s.Create(ctx, CreateInput{OrgID: orgA, Name: "P2", CreatedBy: "u"}); err != nil {
		t.Fatalf("create with empty storage config: %v", err)
	}
	// Update 改成他 org 配置 → 拒。
	if _, err := s.Update(ctx, p.ID, UpdateInput{Name: "P", StorageConfigID: cfgB}); !errors.Is(err, ErrInvalidStorageConfig) {
		t.Fatalf("update with foreign storage config: err=%v want ErrInvalidStorageConfig", err)
	}
	// Update 本 org 配置 → 成功。
	if _, err := s.Update(ctx, p.ID, UpdateInput{Name: "P", StorageConfigID: cfgA}); err != nil {
		t.Fatalf("update with own storage config: %v", err)
	}
}

// seedNodeOutput inserts one node_outputs row with explicit content/format/items.
// createdAt offset (interval) lets the multi-row tie-break test order rows.
func seedNodeOutput(t *testing.T, pool *pgxpool.Pool, id, projectID, todoID, format, content, items, createdInterval string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items, created_at)
		 VALUES ($1, $2, $3, 'custom:x', $4, $5, $6::jsonb, now() + $7::interval)`,
		id, projectID, todoID, content, format, items, createdInterval); err != nil {
		t.Fatalf("seed node_output %s: %v", id, err)
	}
}

// loadStateNode runs LoadState and returns the GraphNode for todoID.
func loadStateNode(t *testing.T, s *Store, projectID, todoID string) projectstate.GraphNode {
	t.Helper()
	st, err := s.LoadState(context.Background(), projectID, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for _, n := range st.Nodes {
		if n.ID == todoID {
			return n
		}
	}
	t.Fatalf("node %q not found in %+v", todoID, st.Nodes)
	return projectstate.GraphNode{}
}

// setupCustomRun creates a project + custom workflow plan + one custom todo,
// returning (projectID, todoID). Caller seeds node_outputs against todoID.
func setupCustomRun(t *testing.T, s *Store, pool *pgxpool.Pool, orgPrefix string) (string, string) {
	t.Helper()
	ctx := context.Background()
	orgID := orgPrefix + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "Items", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	wfID := "wf_it_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO workflows (id, project_id, name, nodes) VALUES ($1,$2,'wf','[]'::jsonb)`,
		wfID, p.ID); err != nil {
		t.Fatalf("insert workflow: %v", err)
	}
	planID := "pln_it_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, workflow_id, created_at)
		 VALUES ($1,$2,'running',true,false,$3, now())`, planID, p.ID, wfID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	todoID := "todo_it_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'custom:http','done')`,
		todoID, p.ID, planID); err != nil {
		t.Fatalf("insert todo: %v", err)
	}
	return p.ID, todoID
}

// TestLoadState_Items_JSONParsedObject (I1/M-2 regression guard): a format='json'
// node_outputs row stores a real parsed object in items; LoadState must surface
// it verbatim — $json.field reachable, NOT a {text:"<json string>"} wrapper.
func TestLoadState_Items_JSONParsedObject(t *testing.T) {
	s, pool := newStore(t)
	pid, tid := setupCustomRun(t, s, pool, "org_it_json_")
	// items as P2a would land for a format='json' output: [{json:<parsed object>}]
	seedNodeOutput(t, pool, "no_json_"+tid, pid, tid, "json", `{"field":"value","n":3}`,
		`[{"json":{"field":"value","n":3}}]`, "0 seconds")
	n := loadStateNode(t, s, pid, tid)
	if len(n.Items) != 1 {
		t.Fatalf("items len=%d want 1", len(n.Items))
	}
	var obj map[string]any
	if err := json.Unmarshal(n.Items[0].JSON, &obj); err != nil {
		t.Fatalf("item json not an object (M-2 regression?): %v raw=%s", err, n.Items[0].JSON)
	}
	if obj["field"] != "value" {
		t.Fatalf("$json.field=%v want value (parsed object, not {text:...} wrapper); raw=%s", obj["field"], n.Items[0].JSON)
	}
	if _, isWrapped := obj["text"]; isWrapped {
		t.Fatalf("json-format item must NOT be wrapped as {text:...}; raw=%s", n.Items[0].JSON)
	}
}

// TestLoadState_Items_TextWrapped: a format='text' row's item json == {"text":"..."}.
func TestLoadState_Items_TextWrapped(t *testing.T) {
	s, pool := newStore(t)
	pid, tid := setupCustomRun(t, s, pool, "org_it_text_")
	seedNodeOutput(t, pool, "no_text_"+tid, pid, tid, "text", "Hello world",
		`[{"json":{"text":"Hello world"}}]`, "0 seconds")
	n := loadStateNode(t, s, pid, tid)
	if len(n.Items) != 1 {
		t.Fatalf("items len=%d want 1", len(n.Items))
	}
	// JSONB normalizes whitespace, so compare semantically (parsed object), not
	// byte-for-byte: the item must be {"text":"Hello world"} with no other keys.
	var obj map[string]any
	if err := json.Unmarshal(n.Items[0].JSON, &obj); err != nil {
		t.Fatalf("item json not an object: %v raw=%s", err, n.Items[0].JSON)
	}
	if len(obj) != 1 || obj["text"] != "Hello world" {
		t.Fatalf("text item = %v want {text:\"Hello world\"}; raw=%s", obj, n.Items[0].JSON)
	}
}

// TestLoadState_Items_BinaryRoundTrip: an items-only row carrying a binary ref
// round-trips assetId/kind/mimeType/status through InspectorBinaryRef.
func TestLoadState_Items_BinaryRoundTrip(t *testing.T) {
	s, pool := newStore(t)
	pid, tid := setupCustomRun(t, s, pool, "org_it_bin_")
	items := `[{"json":{"caption":"a"},"binary":{"data":{"assetId":"as9","mimeType":"image/png","kind":"image","status":"accepted"}}}]`
	seedNodeOutput(t, pool, "no_bin_"+tid, pid, tid, "items", "[]", items, "0 seconds")
	n := loadStateNode(t, s, pid, tid)
	if len(n.Items) != 1 {
		t.Fatalf("items len=%d want 1", len(n.Items))
	}
	br, ok := n.Items[0].Binary["data"]
	if !ok {
		t.Fatalf("binary[data] missing in %+v", n.Items[0].Binary)
	}
	if br.AssetID != "as9" || br.MimeType != "image/png" || br.Kind != "image" || br.Status != "accepted" {
		t.Fatalf("binary ref round-trip mismatch: %+v", br)
	}
}

// TestLoadState_Items_MultiRowNewestWithItemsWins (OQ2 tie-break): a todo with a
// legacy content row (empty items) plus a newer items-bearing row → the newest
// non-empty-items row wins.
func TestLoadState_Items_MultiRowNewestWithItemsWins(t *testing.T) {
	s, pool := newStore(t)
	pid, tid := setupCustomRun(t, s, pool, "org_it_multi_")
	// Oldest: a legacy content row with empty items '[]'.
	seedNodeOutput(t, pool, "no_legacy_"+tid, pid, tid, "text", "legacy", `[]`, "-10 seconds")
	// Middle: an items-bearing row (older than the newest).
	seedNodeOutput(t, pool, "no_mid_"+tid, pid, tid, "items", "[]",
		`[{"json":{"which":"middle"}}]`, "-5 seconds")
	// Newest: the row that must win.
	seedNodeOutput(t, pool, "no_new_"+tid, pid, tid, "items", "[]",
		`[{"json":{"which":"newest"}}]`, "0 seconds")
	n := loadStateNode(t, s, pid, tid)
	if len(n.Items) != 1 {
		t.Fatalf("items len=%d want 1 (newest-with-items)", len(n.Items))
	}
	var obj map[string]any
	if err := json.Unmarshal(n.Items[0].JSON, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if obj["which"] != "newest" {
		t.Fatalf("tie-break picked %v want newest; raw=%s", obj["which"], n.Items[0].JSON)
	}
}

// TestLoadState_Items_NewestEmptyDoesNotMaskItems: if the NEWEST row has empty
// items but an older row has items, the older items-bearing row still wins
// (we prefer the newest row WHOSE items are non-empty, not just the newest row).
func TestLoadState_Items_NewestEmptyDoesNotMaskItems(t *testing.T) {
	s, pool := newStore(t)
	pid, tid := setupCustomRun(t, s, pool, "org_it_empty_")
	seedNodeOutput(t, pool, "no_haveitems_"+tid, pid, tid, "items", "[]",
		`[{"json":{"which":"hasitems"}}]`, "-5 seconds")
	// Newest row has empty items — must not mask the older items-bearing row.
	seedNodeOutput(t, pool, "no_emptynew_"+tid, pid, tid, "text", "later", `[]`, "0 seconds")
	n := loadStateNode(t, s, pid, tid)
	if len(n.Items) != 1 {
		t.Fatalf("items len=%d want 1 (older non-empty items wins over newest empty)", len(n.Items))
	}
	var obj map[string]any
	_ = json.Unmarshal(n.Items[0].JSON, &obj)
	if obj["which"] != "hasitems" {
		t.Fatalf("tie-break picked %v want hasitems; raw=%s", obj["which"], n.Items[0].JSON)
	}
}

// TestLoadState_Items_CrossTenantIsolation: LoadState for project A never
// surfaces project B's items (the node_outputs query is plan/project-scoped).
func TestLoadState_Items_CrossTenantIsolation(t *testing.T) {
	s, pool := newStore(t)
	pidA, tidA := setupCustomRun(t, s, pool, "org_it_xa_")
	pidB, tidB := setupCustomRun(t, s, pool, "org_it_xb_")
	seedNodeOutput(t, pool, "no_a_"+tidA, pidA, tidA, "items", "[]",
		`[{"json":{"owner":"A"}}]`, "0 seconds")
	seedNodeOutput(t, pool, "no_b_"+tidB, pidB, tidB, "items", "[]",
		`[{"json":{"owner":"B"}}]`, "0 seconds")
	nA := loadStateNode(t, s, pidA, tidA)
	var obj map[string]any
	_ = json.Unmarshal(nA.Items[0].JSON, &obj)
	if obj["owner"] != "A" {
		t.Fatalf("project A node items owner=%v want A (cross-tenant leak?)", obj["owner"])
	}
	// Project A's state must contain no node carrying B's items.
	stA, _ := s.LoadState(context.Background(), pidA, "")
	for _, n := range stA.Nodes {
		for _, it := range n.Items {
			if bytes.Contains(it.JSON, []byte(`"B"`)) {
				t.Fatalf("project A leaked project B items: %s", it.JSON)
			}
		}
	}
}

// TestLoadState_Items_MarshalParity: json.Marshal of the full ProjectState
// contains "items" (proves SSE/REST parity — both marshal the same struct).
func TestLoadState_Items_MarshalParity(t *testing.T) {
	s, pool := newStore(t)
	pid, tid := setupCustomRun(t, s, pool, "org_it_marshal_")
	seedNodeOutput(t, pool, "no_m_"+tid, pid, tid, "items", "[]",
		`[{"json":{"field":"value"}}]`, "0 seconds")
	st, err := s.LoadState(context.Background(), pid, "")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"items"`)) {
		t.Fatalf("marshalled ProjectState missing items key: %s", b)
	}
	// The verbatim object survives the marshal round-trip. JSONB may normalize
	// whitespace, so assert on the field/value tokens independently rather than a
	// fixed compact form.
	if !bytes.Contains(b, []byte(`"field"`)) || !bytes.Contains(b, []byte(`"value"`)) {
		t.Fatalf("marshalled items lost the verbatim object: %s", b)
	}
}

// TestSoftDeleteProject 覆盖 spec（docs/specs/project-delete.md §3）的 store 层验收：
// 删除后 Get/List 不可见、在途 todos/assets/export_jobs 被级联取消、重复删除幂等
// （ErrNotFound → 端点 404）、generations 计费账本保留（org 聚合仍含历史）。
func TestSoftDeleteProject(t *testing.T) {
	s, pool, db := newStoreG(t)
	ctx := context.Background()
	orgID := "org_del_" + uniqueSuffix()
	p, err := s.Create(ctx, CreateInput{OrgID: orgID, Name: "Doomed", Brief: "b", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	pln := "pln_del_" + p.ID
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, created_at)
		 VALUES ($1, $2, 'running', true, false, now())`, pln, p.ID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	// 在途 todo 全谱（pending/ready/blocked/running-with-lease）+ 一个已完成的。
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status, locked_by, locked_until) VALUES
		 ($1, $6, $7, 'script', 'pending', '', NULL),
		 ($2, $6, $7, 'script', 'ready', '', NULL),
		 ($3, $6, $7, 'asset', 'blocked', '', NULL),
		 ($4, $6, $7, 'asset', 'running', 'w1', now() + interval '60 seconds'),
		 ($5, $6, $7, 'script', 'done', '', NULL)`,
		"td_p_"+p.ID, "td_r_"+p.ID, "td_b_"+p.ID, "td_run_"+p.ID, "td_d_"+p.ID, p.ID, pln); err != nil {
		t.Fatalf("insert todos: %v", err)
	}
	// 在途资产（generating 同步 + submitted 异步）+ 一个待审的（保留可审）。
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, status) VALUES
		 ($1, $4, 'generating'), ($2, $4, 'submitted'), ($3, $4, 'pending_acceptance')`,
		"as_g_"+p.ID, "as_s_"+p.ID, "as_pa_"+p.ID, p.ID); err != nil {
		t.Fatalf("insert assets: %v", err)
	}
	// 在途导出任务（pending + running-with-lease）+ 一个已完成的。
	if _, err := pool.Exec(ctx,
		`INSERT INTO export_jobs (id, project_id, plan_id, format, status, locked_by, locked_until) VALUES
		 ($1, $4, $5, 'pdf', 'pending', '', NULL),
		 ($2, $4, $5, 'epub', 'running', 'w1', now() + interval '60 seconds'),
		 ($3, $4, $5, 'zip', 'done', '', NULL)`,
		"ej_p_"+p.ID, "ej_r_"+p.ID, "ej_d_"+p.ID, p.ID, pln); err != nil {
		t.Fatalf("insert export jobs: %v", err)
	}
	// 计费账本一行（删除后 org 聚合必须仍含它）。
	cs := cost.New(db)
	if err := cs.Record(ctx, cost.Generation{
		ProjectID: p.ID, AssetID: "as_g_" + p.ID, TodoID: "td_run_" + p.ID,
		Kind: "image", Provider: "fake", Model: "m", ImageCount: 1, CostMicros: 100,
	}); err != nil {
		t.Fatalf("record generation: %v", err)
	}

	if err := s.SoftDelete(ctx, p.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	// 验收 1：删除后 Get → ErrNotFound；List 排除。
	if _, err := s.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: err=%v want ErrNotFound", err)
	}
	list, _, err := s.ListByOrg(ctx, orgID, 50, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, it := range list {
		if it.ID == p.ID {
			t.Fatalf("deleted project still listed: %+v", it)
		}
	}
	if deleted, err := s.Deleted(ctx, p.ID); err != nil || !deleted {
		t.Fatalf("Deleted() = (%v, %v), want (true, nil)", deleted, err)
	}
	// worker 落账/告警路径：org 解析对已删项目刻意仍可用。
	if org, err := s.OrgIDForProject(ctx, p.ID); err != nil || org != orgID {
		t.Fatalf("OrgIDForProject after delete = (%q, %v), want (%q, nil)", org, err, orgID)
	}

	// 验收 3：在途 todos 全部取消（租约清空），done 不动。
	var canceled, done int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status='canceled' AND locked_by='' AND locked_until IS NULL),
		        count(*) FILTER (WHERE status='done')
		 FROM todos WHERE project_id=$1`, p.ID).Scan(&canceled, &done); err != nil {
		t.Fatalf("count todos: %v", err)
	}
	if canceled != 4 || done != 1 {
		t.Fatalf("todos after delete: canceled=%d done=%d, want 4/1", canceled, done)
	}
	// 在途资产 → canceled；pending_acceptance 保留（与 Cancel 语义一致）。
	var aCanceled, aPending int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status='canceled'),
		        count(*) FILTER (WHERE status='pending_acceptance')
		 FROM assets WHERE project_id=$1`, p.ID).Scan(&aCanceled, &aPending); err != nil {
		t.Fatalf("count assets: %v", err)
	}
	if aCanceled != 2 || aPending != 1 {
		t.Fatalf("assets after delete: canceled=%d pending=%d, want 2/1", aCanceled, aPending)
	}
	// 在途导出任务 → failed（该状态机的非成功终态），done 不动。
	var ejFailed, ejDone int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE status='failed' AND error='project deleted' AND locked_by=''),
		        count(*) FILTER (WHERE status='done')
		 FROM export_jobs WHERE project_id=$1`, p.ID).Scan(&ejFailed, &ejDone); err != nil {
		t.Fatalf("count export jobs: %v", err)
	}
	if ejFailed != 2 || ejDone != 1 {
		t.Fatalf("export jobs after delete: failed=%d done=%d, want 2/1", ejFailed, ejDone)
	}

	// 验收 4：重复删除幂等 → ErrNotFound（端点 404）。
	if err := s.SoftDelete(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second soft delete: err=%v want ErrNotFound", err)
	}

	// 验收 5：org 成本聚合仍含已删项目的历史消费（generations 不动）。
	agg, err := cs.ByOrg(ctx, orgID)
	if err != nil {
		t.Fatalf("cost by org: %v", err)
	}
	if agg.Generations != 1 || agg.CostMicros != 100 {
		t.Fatalf("org cost after delete = %+v, want 1 generation / 100 micros retained", agg)
	}
}

// TestSoftDeleteMissingProject：不存在的 id → ErrNotFound（端点 404，不泄露信息）。
func TestSoftDeleteMissingProject(t *testing.T) {
	s, _ := newStore(t)
	if err := s.SoftDelete(context.Background(), "no_such_"+uniqueSuffix()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soft delete missing: err=%v want ErrNotFound", err)
	}
}
