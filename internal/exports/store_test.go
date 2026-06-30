package exports

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

// open returns (pool, gorm) against a migrated fresh DB, or skips when no PG URL.
func open(t *testing.T) (*pgxpool.Pool, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run exports store tests")
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
	// export_jobs is a global queue (Claim picks ANY due pending row), so leftover
	// rows from prior tests in this shared DB would pollute Claim ordering. Start
	// each test from an empty queue (serial -p 1 makes this safe).
	if _, err := st.Pool().Exec(ctx, `TRUNCATE export_jobs`); err != nil {
		t.Fatalf("truncate export_jobs: %v", err)
	}
	return st.Pool(), st.GORM()
}

// seedProject inserts a minimal project so export_jobs' FK resolves.
func seedProject(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := newID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'org','p','u')`, id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

func TestCreateIsPending(t *testing.T) {
	pool, db := open(t)
	st := New(db)
	ctx := context.Background()
	pid := seedProject(t, pool)

	j, err := st.Create(ctx, pid, "plan-1", "pdf")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if j.Status != "pending" || j.Format != "pdf" || j.PlanID != "plan-1" || j.Attempts != 0 {
		t.Fatalf("unexpected job: %+v", j)
	}
	if j.ProjectID != pid {
		t.Fatalf("project_id mismatch: %q != %q", j.ProjectID, pid)
	}
	got, err := st.Get(ctx, j.ID)
	if err != nil || got.ID != j.ID || got.Status != "pending" {
		t.Fatalf("get back: %v %+v", err, got)
	}
}

func TestGetNotFound(t *testing.T) {
	_, db := open(t)
	st := New(db)
	if _, err := st.Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestClaimMutualExclusion: two pending jobs → two claims return distinct jobs;
// a single pending job → the second claim finds nothing (found=false).
func TestClaimMutualExclusion(t *testing.T) {
	pool, db := open(t)
	st := New(db)
	ctx := context.Background()
	pid := seedProject(t, pool)

	a, _ := st.Create(ctx, pid, "", "zip")
	b, _ := st.Create(ctx, pid, "", "zip")

	c1, ok1, err := st.Claim(ctx, "w1", time.Minute)
	if err != nil || !ok1 {
		t.Fatalf("claim 1: ok=%v err=%v", ok1, err)
	}
	c2, ok2, err := st.Claim(ctx, "w2", time.Minute)
	if err != nil || !ok2 {
		t.Fatalf("claim 2: ok=%v err=%v", ok2, err)
	}
	if c1.ID == c2.ID {
		t.Fatalf("two claims returned the SAME job %q (SKIP LOCKED broken)", c1.ID)
	}
	if c1.Status != "running" || c1.LockedBy != "w1" || c1.LockedUntil.IsZero() {
		t.Fatalf("claim 1 not leased running: %+v", c1)
	}
	got := map[string]bool{c1.ID: true, c2.ID: true}
	if !got[a.ID] || !got[b.ID] {
		t.Fatalf("claims did not cover both seeded jobs: c1=%s c2=%s a=%s b=%s", c1.ID, c2.ID, a.ID, b.ID)
	}
	// Queue now empty → no claim.
	_, ok3, err := st.Claim(ctx, "w3", time.Minute)
	if err != nil {
		t.Fatalf("claim 3: %v", err)
	}
	if ok3 {
		t.Fatalf("claim 3 should find nothing, queue is drained")
	}
}

// TestReapStrandedLease: a claimed (running) job whose lease expired beyond ttl
// is terminal-stated to failed by Reap.
func TestReapStrandedLease(t *testing.T) {
	pool, db := open(t)
	st := New(db)
	ctx := context.Background()
	pid := seedProject(t, pool)

	j, _ := st.Create(ctx, pid, "", "pdf")
	claimed, ok, err := st.Claim(ctx, "w1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	// Force the lease far into the past so it is older than the reaper ttl.
	if _, err := pool.Exec(ctx,
		`UPDATE export_jobs SET locked_until = now() - interval '1 hour' WHERE id=$1`, claimed.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	n, err := st.Reap(ctx, time.Minute)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 reaped, got %d", n)
	}
	got, _ := st.Get(ctx, j.ID)
	if got.Status != "failed" {
		t.Fatalf("reaped job should be failed, got %q", got.Status)
	}
	// A still-live lease is NOT reaped.
	j2, _ := st.Create(ctx, pid, "", "pdf")
	c2, _, _ := st.Claim(ctx, "w2", time.Minute)
	if c2.ID != j2.ID {
		t.Fatalf("setup: expected to claim j2")
	}
	n2, err := st.Reap(ctx, time.Minute)
	if err != nil || n2 != 0 {
		t.Fatalf("live lease must not be reaped: n=%d err=%v", n2, err)
	}
}

// TestMarkDoneRunningGuard: MarkDone only advances a running job; on a
// pending/already-done job it is a no-op (ErrNotRunning).
func TestMarkDoneRunningGuard(t *testing.T) {
	pool, db := open(t)
	st := New(db)
	ctx := context.Background()
	pid := seedProject(t, pool)

	j, _ := st.Create(ctx, pid, "", "zip")
	// pending → MarkDone refused.
	if err := st.MarkDone(ctx, j.ID, "k", "cfg", 10); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("MarkDone on pending should be ErrNotRunning, got %v", err)
	}
	// claim → running → MarkDone wins.
	c, ok, err := st.Claim(ctx, "w1", time.Minute)
	if err != nil || !ok || c.ID != j.ID {
		t.Fatalf("claim: %+v ok=%v err=%v", c, ok, err)
	}
	if err := st.MarkDone(ctx, j.ID, "book.zip", "cfg-1", 4096); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	got, _ := st.Get(ctx, j.ID)
	if got.Status != "done" || got.BlobKey != "book.zip" || got.StorageConfigID != "cfg-1" || got.SizeBytes != 4096 {
		t.Fatalf("done row wrong: %+v", got)
	}
	if !got.LockedUntil.IsZero() || got.LockedBy != "" {
		t.Fatalf("done row should clear lease: %+v", got)
	}
	// Second MarkDone on a now-done job is a no-op.
	if err := st.MarkDone(ctx, j.ID, "x", "y", 1); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("MarkDone on done should be ErrNotRunning, got %v", err)
	}
}

// TestMarkFailedReschedulesThenFails: attempts<max → pending+backoff (not yet
// due, so Claim skips it); attempts≥max → terminal failed.
func TestMarkFailedReschedulesThenFails(t *testing.T) {
	pool, db := open(t)
	st := New(db)
	ctx := context.Background()
	pid := seedProject(t, pool)

	j, _ := st.Create(ctx, pid, "", "pdf")
	if _, ok, err := st.Claim(ctx, "w1", time.Minute); err != nil || !ok {
		t.Fatalf("claim 1: ok=%v err=%v", ok, err)
	}
	// First failure: attempts 1 < max 2 → reschedule pending with a long backoff.
	if err := st.MarkFailed(ctx, j.ID, "boom-1", 2, time.Hour); err != nil {
		t.Fatalf("mark failed 1: %v", err)
	}
	got, _ := st.Get(ctx, j.ID)
	if got.Status != "pending" || got.Attempts != 1 || got.Error != "boom-1" {
		t.Fatalf("after failure 1: %+v", got)
	}
	if !got.NextRunAt.After(time.Now().Add(30 * time.Minute)) {
		t.Fatalf("backoff should push next_run_at ~1h out, got %v", got.NextRunAt)
	}
	// Backed-off job is not yet due → Claim skips it.
	if _, ok, err := st.Claim(ctx, "w2", time.Minute); err != nil || ok {
		t.Fatalf("backed-off job must not be claimable: ok=%v err=%v", ok, err)
	}
	// Reset next_run_at to now so we can claim and fail it again.
	if _, err := pool.Exec(ctx, `UPDATE export_jobs SET next_run_at = now() WHERE id=$1`, j.ID); err != nil {
		t.Fatalf("reset next_run_at: %v", err)
	}
	if _, ok, err := st.Claim(ctx, "w3", time.Minute); err != nil || !ok {
		t.Fatalf("claim 2: ok=%v err=%v", ok, err)
	}
	// Second failure: attempts 2 >= max 2 → terminal failed.
	if err := st.MarkFailed(ctx, j.ID, "boom-2", 2, time.Hour); err != nil {
		t.Fatalf("mark failed 2: %v", err)
	}
	got, _ = st.Get(ctx, j.ID)
	if got.Status != "failed" || got.Attempts != 2 || got.Error != "boom-2" {
		t.Fatalf("after failure 2 (terminal): %+v", got)
	}
	// MarkFailed on a terminal job is a no-op.
	if err := st.MarkFailed(ctx, j.ID, "boom-3", 2, time.Hour); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("MarkFailed on failed should be ErrNotRunning, got %v", err)
	}
}

func TestListByProject(t *testing.T) {
	pool, db := open(t)
	st := New(db)
	ctx := context.Background()
	pid := seedProject(t, pool)
	other := seedProject(t, pool)

	_, _ = st.Create(ctx, pid, "", "pdf")
	_, _ = st.Create(ctx, pid, "", "zip")
	_, _ = st.Create(ctx, other, "", "epub")

	list, err := st.ListByProject(ctx, pid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 jobs for project, got %d", len(list))
	}
	for _, j := range list {
		if j.ProjectID != pid {
			t.Fatalf("list leaked other project: %+v", j)
		}
	}
}
