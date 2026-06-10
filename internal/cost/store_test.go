package cost

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run cost store tests")
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
	return st.Pool()
}

func seedProject(t *testing.T, pool *pgxpool.Pool, orgID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO projects (id, org_id, name, created_by) VALUES (md5(random()::text), $1, 'p', 'u') RETURNING id`,
		orgID).Scan(&id); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

func TestRecordAndAggregateByProject(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	pid := seedProject(t, pool, "org-cost")
	if err := st.Record(ctx, Generation{
		ProjectID: pid, TodoID: "t", Kind: "image", Provider: "fake", Model: "m",
		Tokens: 100, ImageCount: 1, CostMicros: 2000, LatencyMS: 350,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	_ = st.Record(ctx, Generation{ProjectID: pid, Kind: "image", Provider: "fake", Model: "m", ImageCount: 1, CostMicros: 3000})
	agg, err := st.ByProject(ctx, pid)
	if err != nil {
		t.Fatalf("byProject: %v", err)
	}
	if agg.Generations != 2 || agg.CostMicros != 5000 || agg.Tokens != 100 || agg.ImageCount != 2 {
		t.Fatalf("aggregate mismatch: %+v", agg)
	}
}

func TestAggregateByOrg(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	pid := seedProject(t, pool, "org-only")
	_ = st.Record(ctx, Generation{ProjectID: pid, Kind: "image", Provider: "fake", Model: "m", CostMicros: 1500, ImageCount: 1})
	agg, err := st.ByOrg(ctx, "org-only")
	if err != nil {
		t.Fatalf("byOrg: %v", err)
	}
	if agg.CostMicros != 1500 || agg.Generations != 1 {
		t.Fatalf("org aggregate mismatch: %+v", agg)
	}
}
