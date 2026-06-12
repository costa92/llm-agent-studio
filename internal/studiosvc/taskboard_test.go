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
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES ('tr1',$1,'pl','script','done')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES ('tr2',$1,'pl','script','done')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES ('tr3',$1,'pl','script','pending')`, pRun)
	// a canceled todo must NOT count toward total.
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status) VALUES ('tr4',$1,'pl','script','canceled')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id,project_id,status) VALUES ('ar1',$1,'pending_acceptance')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id,project_id,status) VALUES ('ar2',$1,'accepted')`, pRun)
	_, _ = pool.Exec(ctx, `INSERT INTO run_events (project_id,kind,ts) VALUES ($1,'run.started',now())`, pRun)

	// failed project: status=failed with a failing todo carrying agent='ScriptAgent'.
	// updated_at pushed into the past so pRun (recent run_event) sorts above it.
	pFail := "p_fail_" + randHexSvc()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id,org_id,name,status,created_by,updated_at) VALUES ($1,$2,'Fail','failed','u',now()-interval '30 minute')`, pFail, org)
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status,agent) VALUES ('tf1',$1,'pl','script','failed','ScriptAgent')`, pFail)

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
