package health

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// uniqueSuffix gives each run distinct ids in the shared test DB so prior-run
// rows never pollute aggregate counts.
func uniqueSuffix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := envDSN(t)
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(st.GORM(), project.New(st.GORM())), st.Pool()
}

func envDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run health tests")
	}
	return dsn
}

// seedProject inserts a project with the given status and returns its id.
func seedProject(t *testing.T, pool *pgxpool.Pool, orgID, status string) string {
	t.Helper()
	id := "prj_" + uniqueSuffix()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO projects (id, org_id, name, status, created_by) VALUES ($1,$2,$3,$4,'u1')`,
		id, orgID, "health-test", status)
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

// seedPlan inserts a plan for a project and returns its id.
func seedPlan(t *testing.T, pool *pgxpool.Pool, projectID string) string {
	t.Helper()
	id := "pln_" + uniqueSuffix()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO plans (id, project_id, status, valid) VALUES ($1,$2,'created',true)`,
		id, projectID)
	if err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	return id
}

// seedTodo inserts a todo. lockedUntil nil = no lock.
func seedTodo(t *testing.T, pool *pgxpool.Pool, projectID, planID, status string, lockedUntil *time.Time) string {
	t.Helper()
	id := "tdo_" + uniqueSuffix()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO todos (id, project_id, plan_id, type, status, agent, error, locked_until)
		 VALUES ($1,$2,$3,'script',$4,'ScriptAgent','boom',$5)`,
		id, projectID, planID, status, lockedUntil)
	if err != nil {
		t.Fatalf("seed todo: %v", err)
	}
	return id
}

// seedAsset inserts an asset with explicit created_at / submitted_at.
func seedAsset(t *testing.T, pool *pgxpool.Pool, projectID, todoID, status string, createdAt time.Time, submittedAt *time.Time) string {
	t.Helper()
	id := "ast_" + uniqueSuffix()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO assets (id, project_id, todo_id, status, created_at, submitted_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		id, projectID, todoID, status, createdAt, submittedAt)
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	return id
}

func samplesContain(samples []string, id string) bool {
	for _, s := range samples {
		if s == id {
			return true
		}
	}
	return false
}

func findCheck(t *testing.T, rep Report, id string) Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q not found in report", id)
	return Check{}
}

func TestStuckTodos(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	org := "org_" + uniqueSuffix()
	prj := seedProject(t, pool, org, "running")
	pln := seedPlan(t, pool, prj)
	// abnormal: running with no lock; normal: running with a future lock.
	stuck := seedTodo(t, pool, prj, pln, "running", nil)
	future := time.Now().Add(time.Hour)
	seedTodo(t, pool, prj, pln, "running", &future)

	rep, err := s.Report(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	c := findCheck(t, rep, "stuck_todos")
	if c.Count < 1 || !samplesContain(c.Samples, stuck) {
		t.Fatalf("stuck_todos count=%d samples=%v want >=1 incl %s", c.Count, c.Samples, stuck)
	}
	if rep.System.StuckTodos != c.Count || rep.System.WorkerHealthy {
		t.Fatalf("system stuckTodos=%d workerHealthy=%v vs check count=%d", rep.System.StuckTodos, rep.System.WorkerHealthy, c.Count)
	}

	res, err := s.Repair(ctx, "stuck_todos")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if res.Repaired < 1 {
		t.Fatalf("repaired=%d want >=1", res.Repaired)
	}
	rep2, err := s.Report(ctx)
	if err != nil {
		t.Fatalf("report2: %v", err)
	}
	// The specific stuck todo is now 'ready', so it must no longer be sampled.
	c2 := findCheck(t, rep2, "stuck_todos")
	if samplesContain(c2.Samples, stuck) {
		t.Fatalf("stuck_todos still flags %s after repair: %v", stuck, c2.Samples)
	}
}

func TestStuckAssets(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	org := "org_" + uniqueSuffix()
	prj := seedProject(t, pool, org, "running")
	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now()
	// abnormal: generating, created 2h ago.
	stuck := seedAsset(t, pool, prj, "", "generating", old, nil)
	// abnormal: submitted, submitted 2h ago.
	subAt := old
	stuckSub := seedAsset(t, pool, prj, "", "submitted", recent, &subAt)
	// normal: generating but recent.
	seedAsset(t, pool, prj, "", "generating", recent, nil)

	rep, err := s.Report(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	c := findCheck(t, rep, "stuck_assets")
	if c.Count < 2 || !samplesContain(c.Samples, stuck) {
		t.Fatalf("stuck_assets count=%d samples=%v want >=2 incl %s", c.Count, c.Samples, stuck)
	}

	res, err := s.Repair(ctx, "stuck_assets")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if res.Repaired < 2 {
		t.Fatalf("repaired=%d want >=2", res.Repaired)
	}
	// both abnormal assets now 'failed'.
	for _, id := range []string{stuck, stuckSub} {
		var st string
		if err := pool.QueryRow(ctx, `SELECT status FROM assets WHERE id=$1`, id).Scan(&st); err != nil {
			t.Fatalf("recheck %s: %v", id, err)
		}
		if st != "failed" {
			t.Fatalf("asset %s status=%q want failed", id, st)
		}
	}
}

func TestFailedTodoLiveAssets(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	org := "org_" + uniqueSuffix()
	prj := seedProject(t, pool, org, "failed")
	pln := seedPlan(t, pool, prj)
	failedTodo := seedTodo(t, pool, prj, pln, "failed", nil)
	doneTodo := seedTodo(t, pool, prj, pln, "done", nil)
	now := time.Now()
	// abnormal: live asset on a failed todo.
	bad := seedAsset(t, pool, prj, failedTodo, "generating", now, nil)
	// normal: live asset on a done todo.
	seedAsset(t, pool, prj, doneTodo, "generating", now, nil)

	rep, err := s.Report(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	c := findCheck(t, rep, "failed_todo_live_assets")
	if c.Count < 1 || !samplesContain(c.Samples, bad) {
		t.Fatalf("failed_todo_live_assets count=%d samples=%v want >=1 incl %s", c.Count, c.Samples, bad)
	}

	res, err := s.Repair(ctx, "failed_todo_live_assets")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if res.Repaired < 1 {
		t.Fatalf("repaired=%d want >=1", res.Repaired)
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM assets WHERE id=$1`, bad).Scan(&st); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	if st != "failed" {
		t.Fatalf("asset %s status=%q want failed", bad, st)
	}
}

func TestStatusDivergence(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	org := "org_" + uniqueSuffix()
	// divergent: stored 'draft' but latest plan's todos all 'done' → derives 'completed'.
	prj := seedProject(t, pool, org, "draft")
	pln := seedPlan(t, pool, prj)
	seedTodo(t, pool, prj, pln, "done", nil)
	seedTodo(t, pool, prj, pln, "done", nil)

	rep, err := s.Report(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	c := findCheck(t, rep, "status_divergence")
	if c.Count < 1 || !samplesContain(c.Samples, prj) {
		t.Fatalf("status_divergence count=%d samples=%v want >=1 incl %s", c.Count, c.Samples, prj)
	}

	res, err := s.Repair(ctx, "status_divergence")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if res.Repaired < 1 {
		t.Fatalf("repaired=%d want >=1", res.Repaired)
	}
	var st string
	if err := pool.QueryRow(ctx, `SELECT status FROM projects WHERE id=$1`, prj).Scan(&st); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	if st != "completed" {
		t.Fatalf("project %s status=%q want completed after repair", prj, st)
	}
	// re-run report: this project no longer diverges.
	rep2, err := s.Report(ctx)
	if err != nil {
		t.Fatalf("report2: %v", err)
	}
	c2 := findCheck(t, rep2, "status_divergence")
	if samplesContain(c2.Samples, prj) {
		t.Fatalf("status_divergence still flags %s after repair: %v", prj, c2.Samples)
	}
}

func TestOrphanAssets(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	org := "org_" + uniqueSuffix()
	prj := seedProject(t, pool, org, "running")
	pln := seedPlan(t, pool, prj)
	realTodo := seedTodo(t, pool, prj, pln, "done", nil)
	now := time.Now()
	// abnormal: asset references a non-existent todo id.
	orphan := seedAsset(t, pool, prj, "no_such_todo_"+uniqueSuffix(), "pending_acceptance", now, nil)
	// normal: asset references a real todo.
	seedAsset(t, pool, prj, realTodo, "pending_acceptance", now, nil)

	rep, err := s.Report(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	c := findCheck(t, rep, "orphan_assets")
	if c.Count < 1 || !samplesContain(c.Samples, orphan) {
		t.Fatalf("orphan_assets count=%d samples=%v want >=1 incl %s", c.Count, c.Samples, orphan)
	}
	if c.Repairable {
		t.Fatalf("orphan_assets should be report-only")
	}
	if _, err := s.Repair(ctx, "orphan_assets"); err == nil {
		t.Fatalf("Repair(orphan_assets) should error (report-only)")
	}
}

func TestRepairUnknown(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Repair(context.Background(), "bogus"); err == nil {
		t.Fatalf("Repair(bogus) should error")
	}
}

func TestPingAndSystem(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	rep, err := s.Report(ctx)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if !rep.System.DBOK {
		t.Fatalf("DBOK should be true")
	}
	if len(rep.Checks) != 5 {
		t.Fatalf("expected 5 checks, got %d", len(rep.Checks))
	}
}

func TestRecentFailures(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	org := "org_" + uniqueSuffix()
	prj := seedProject(t, pool, org, "failed")
	pln := seedPlan(t, pool, prj)
	failed := seedTodo(t, pool, prj, pln, "failed", nil)

	fs, err := s.RecentFailures(ctx, 200)
	if err != nil {
		t.Fatalf("recent failures: %v", err)
	}
	var found bool
	for _, f := range fs {
		if f.TodoID == failed {
			found = true
			if f.OrgID != org || f.Error != "boom" || f.Agent != "ScriptAgent" {
				t.Fatalf("failure fields wrong: %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("recent failures missing %s", failed)
	}
}
