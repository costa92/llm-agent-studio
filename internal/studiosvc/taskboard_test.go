package studiosvc

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

// TestTaskBoardBoard seeds one org with projects in varied states and asserts
// Board() reports the right progress / pending-review / failing-agent / Failed
// bool and orders by lastActivityAt DESC. Reuses projects.status verbatim.
func TestTaskBoardBoard(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run studiosvc tests")
	}
	ctx := context.Background()
	st, _ := storage.Open(ctx, storage.Config{PGURL: dsn})
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()
	org := "tb_" + randHexSvc()

	// running project: 2 todos done of 3 total (1 pending), a recent run_event,
	// and one pending_acceptance asset → pendingReview=1.
	pRun := "p_run_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,status,created_by) VALUES ($1,$2,'Run','running','u')`, pRun, org)
	// M5.2: Board 现在按"最新 plan 维度"算——todos 必须挂在真实 plan_id 下。
	_, _ = pool.Exec(ctx, `INSERT INTO plans (id,project_id,status,valid,fallback_used) VALUES ('pl_run',$1,'created',true,false)`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES ('tr1',$1,'pl_run','script','done')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES ('tr2',$1,'pl_run','script','done')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES ('tr3',$1,'pl_run','script','pending')`, pRun)
	// a canceled todo must NOT count toward total.
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES ('tr4',$1,'pl_run','script','canceled')`, pRun)
	// 资产要算入"最新 plan 的待审"，挂到最新 plan 的 todo 上。
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id,project_id,todo_id,status) VALUES ('ar1',$1,'tr1','pending_acceptance')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id,project_id,todo_id,status) VALUES ('ar2',$1,'tr1','accepted')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO run_events (project_id,kind,ts) VALUES ($1,'run.started',now())`, pRun)

	// failed project: status=failed with a failing todo carrying agent='ScriptAgent'.
	// updated_at pushed into the past so pRun (recent run_event) sorts above it.
	pFail := "p_fail_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,status,created_by,updated_at) VALUES ($1,$2,'Fail','failed','u',now()-interval '30 minute')`, pFail, org)
	_, _ = pool.Exec(ctx, `INSERT INTO plans (id,project_id,status,valid,fallback_used) VALUES ('pl_fail',$1,'failed',false,false)`, pFail)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status,agent) VALUES ('tf1',$1,'pl_fail','script','failed','ScriptAgent')`, pFail)

	// review / completed / draft projects (older activity, no events).
	pReview := "p_review_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,status,created_by,updated_at) VALUES ($1,$2,'Rev','review','u',now()-interval '1 hour')`, pReview, org)
	pDone := "p_done_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,status,created_by,updated_at) VALUES ($1,$2,'Done','completed','u',now()-interval '2 hour')`, pDone, org)
	pDraft := "p_draft_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,status,created_by,updated_at) VALUES ($1,$2,'Draft','draft','u',now()-interval '3 hour')`, pDraft, org)

	b := NewTaskBoard(pool)
	rows, err := b.Board(ctx, org)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(rows))
	}

	byID := map[string]TaskRow{}
	for _, r := range rows {
		byID[r.ProjectID] = r
	}

	run := byID[pRun]
	if run.Status != "running" || run.ProgressDone != 2 || run.ProgressTotal != 3 {
		t.Fatalf("run progress: status=%q done=%d total=%d", run.Status, run.ProgressDone, run.ProgressTotal)
	}
	if run.PendingReview != 1 {
		t.Fatalf("run pendingReview want 1, got %d", run.PendingReview)
	}
	if run.Failed {
		t.Fatalf("run must not be Failed")
	}

	fail := byID[pFail]
	if !fail.Failed {
		t.Fatalf("fail.Failed want true")
	}
	if fail.FailingAgent != "ScriptAgent" {
		t.Fatalf("failingAgent want ScriptAgent, got %q", fail.FailingAgent)
	}

	// Ordering: pRun has a now() run_event so it has the most recent activity and
	// must sort first; pDraft (updated_at now-3h, no events) must sort last.
	if rows[0].ProjectID != pRun {
		t.Fatalf("expected pRun first by lastActivityAt DESC, got %q", rows[0].ProjectID)
	}
	if rows[len(rows)-1].ProjectID != pDraft {
		t.Fatalf("expected pDraft last, got %q", rows[len(rows)-1].ProjectID)
	}
	if run.LastActivityAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("run lastActivityAt should be ~now, got %v", run.LastActivityAt)
	}

	// Empty org → non-nil empty slice.
	empty, err := b.Board(ctx, "tb_nobody_"+randHexSvc())
	if err != nil {
		t.Fatalf("board empty: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty org want non-nil empty slice, got %v", empty)
	}
}

// M5.2: TaskBoard 行的 progressDone / progressTotal / pendingReview 必须按"最新
// plan 维度"算，与 RefreshStatus 一致（status 也已经按最新 plan）。否则重跑项目
// 之后任务中心会一直显示历史所有失败的 done 数 + 历史所有资产的 pendingReview
// 数——和徽标"待审核"对不上。
//
// 这个 case 搭 2 个 plan：旧 plan A 全 done 但 1 个 asset pending；新 plan B 全
// done 有 2 个 asset pending。期望 task center 行读到的是 B 的 2 done / 2 total /
// 2 pending，而不是 A+B 累加的 3/3/3。
func TestTaskBoardScopesProgressAndPendingToLatestPlan(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run studiosvc tests")
	}
	ctx := context.Background()
	st, _ := storage.Open(ctx, storage.Config{PGURL: dsn})
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()
	org := "tb_mp_" + randHexSvc()
	p := "p_mp_" + randHexSvc()

	_, _ = pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,status,created_by) VALUES ($1,$2,'Multi','failed','u')`, p, org)
	// 旧 plan A：1 script done + 1 asset done + 1 asset pending_acceptance
	// （"failed" 状态实际由 RefreshStatus 决定；这里先把它当旧失败 plan 用）。
	plA := "plA_" + randHexSvc()
	plB := "plB_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO plans (id,project_id,status,valid,fallback_used,created_at)
		VALUES ($1, $2, 'failed', false, false, now() - interval '5 minute')`, plA, p)
	_, _ = pool.Exec(ctx, `INSERT INTO plans (id,project_id,status,valid,fallback_used,created_at)
		VALUES ($1, $2, 'created', true, false, now())`, plB, p)
	// Plan A: 1 done todo + 1 done todo + 1 pending_acceptance asset
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES
		('a1',$1,$2,'script','done'),
		('a2',$1,$2,'asset','done')`, p, plA)
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id,project_id,todo_id,status) VALUES
		('aA1',$1,'a2','pending_acceptance')`, p)
	// Plan B（最新）：2 done todo + 2 pending_acceptance asset
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES
		('b1',$1,$2,'script','done'),
		('b2',$1,$2,'asset','done')`, p, plB)
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id,project_id,todo_id,status) VALUES
		('bA1',$1,'b2','pending_acceptance'),
		('bA2',$1,'b2','pending_acceptance')`, p)
	// RefreshStatus 后 projects.status 会变；本测试不动 status（保持 failed），
	// 只看行内 progress / pendingReview / failingAgent 是否按最新 plan 算。
	rows, err := NewTaskBoard(pool).Board(ctx, org)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]
	// 期望只计 plan B（最新）：2 done / 2 total / 2 pending。
	if row.ProgressDone != 2 {
		t.Fatalf("progressDone want 2 (latest plan only), got %d", row.ProgressDone)
	}
	if row.ProgressTotal != 2 {
		t.Fatalf("progressTotal want 2 (latest plan only), got %d", row.ProgressTotal)
	}
	if row.PendingReview != 2 {
		t.Fatalf("pendingReview want 2 (latest plan only), got %d", row.PendingReview)
	}
	// 没失败：failing_agent 空（plan B 全 done）。
	if row.FailingAgent != "" {
		t.Fatalf("failingAgent should be empty (latest plan has no failed), got %q", row.FailingAgent)
	}
}
