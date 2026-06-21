package todos

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func newStore(t *testing.T) (*Store, *pgxpool.Pool, string) {
	t.Helper()
	st, pool, _, projID := newStoreWithGorm(t)
	return st, pool, projID
}

func newStoreWithGorm(t *testing.T) (*Store, *pgxpool.Pool, *gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run todos tests")
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
	pool := st.Pool()
	// minimal project + plan rows for FK.
	projID := "p_" + randHex(t)
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o1','t','u1')`, projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return New(st.GORM()), pool, st.GORM(), projID
}

func randHex(t *testing.T) string {
	t.Helper()
	return time2hex()
}

func TestCreateGraphSetsReadyAndBlocked(t *testing.T) {
	s, pool, projID := newStore(t)
	ctx := context.Background()
	planID := "pl_" + randHex(t)
	specs := []NodeSpec{
		{LocalID: "a", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"x"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"a"}, InputJSON: []byte(`{}`)},
	}
	ids, err := s.CreateGraph(ctx, projID, planID, specs)
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 ids, got %d", len(ids))
	}
	var statusA, statusB string
	_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, ids["a"]).Scan(&statusA)
	_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, ids["b"]).Scan(&statusB)
	if statusA != "ready" {
		t.Fatalf("script todo should be ready, got %q", statusA)
	}
	if statusB != "blocked" {
		t.Fatalf("storyboard todo should be blocked, got %q", statusB)
	}
}

func TestMarkDoneUnblocksDependents(t *testing.T) {
	s, pool, projID := newStore(t)
	ctx := context.Background()
	planID := "pl_" + randHex(t)
	ids, err := s.CreateGraph(ctx, projID, planID, []NodeSpec{
		{LocalID: "a", Type: "script", DependsOn: nil, InputJSON: []byte(`{}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"a"}, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	// MarkDone guards on status='running' (a claimed todo), so move 'a' there
	// first to mirror the worker's claim → process flow.
	if _, err := pool.Exec(ctx, `UPDATE todos SET status='running' WHERE id=$1`, ids["a"]); err != nil {
		t.Fatalf("set running: %v", err)
	}
	if done, err := s.MarkDone(ctx, ids["a"], "script:"+ids["a"]); err != nil {
		t.Fatalf("mark done: %v", err)
	} else if !done {
		t.Fatalf("mark done: expected running todo to be marked done")
	}
	var statusB string
	_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, ids["b"]).Scan(&statusB)
	if statusB != "ready" {
		t.Fatalf("dependent should be ready after dep done, got %q", statusB)
	}
}

func TestAddDynamicCreatesReadyAssetTodos(t *testing.T) {
	st, pool, gdb, pid := newStoreWithGorm(t)
	ctx := context.Background()
	planID := "pl_" + randHex(t)
	// Seed a storyboard todo (the fan-out parent), mirroring the worker flow.
	parentID := newID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'storyboard','running')`,
		parentID, pid, planID); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	var ids []string
	if err := gdb.Transaction(func(tx *gorm.DB) error {
		var e error
		ids, e = st.AddDynamic(ctx, tx, pid, planID, parentID, []DynamicSpec{
			{Type: "asset", InputJSON: []byte(`{"shotId":"s1"}`)},
			{Type: "asset", InputJSON: []byte(`{"shotId":"s2"}`)},
		})
		return e
	}); err != nil {
		t.Fatalf("addDynamic: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 ids, got %d", len(ids))
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM todos WHERE project_id=$1 AND type='asset' AND status='ready'`, pid).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 ready asset todos, got %d", n)
	}
}

func TestMarkFailedCancelsDependents(t *testing.T) {
	s, pool, projID := newStore(t)
	ctx := context.Background()
	planID := "pl_" + randHex(t)
	// a (script) → b (storyboard) → c (asset); a fails → b,c must cancel.
	ids, err := s.CreateGraph(ctx, projID, planID, []NodeSpec{
		{LocalID: "a", Type: "script", DependsOn: nil, InputJSON: []byte(`{}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"a"}, InputJSON: []byte(`{}`)},
		{LocalID: "c", Type: "storyboard", DependsOn: []string{"b"}, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	if err := s.MarkFailed(ctx, ids["a"], "boom"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	var statusA, statusB, statusC string
	_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, ids["a"]).Scan(&statusA)
	_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, ids["b"]).Scan(&statusB)
	_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, ids["c"]).Scan(&statusC)
	if statusA != "failed" {
		t.Fatalf("failed todo status=%q want failed", statusA)
	}
	if statusB != "canceled" {
		t.Fatalf("direct dependent status=%q want canceled", statusB)
	}
	if statusC != "canceled" {
		t.Fatalf("transitive dependent status=%q want canceled", statusC)
	}
}
