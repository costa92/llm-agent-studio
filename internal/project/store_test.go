package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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
	return New(st.Pool()), st.Pool()
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
