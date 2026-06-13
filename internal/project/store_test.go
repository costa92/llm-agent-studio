package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

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
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Status != "draft" {
		t.Fatalf("new project status=%q want draft", p.Status)
	}
	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Promo" || got.OrgID != orgID {
		t.Fatalf("get mismatch: %+v", got)
	}
	items, _, err := s.ListByOrg(ctx, orgID, 50, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("list len=%d want 1", len(items))
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
