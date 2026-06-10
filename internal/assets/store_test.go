package assets

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run assets store tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedProject inserts a minimal project row so the FK on assets resolves.
func seedProject(t *testing.T, pool *pgxpool.Pool, orgID string) string {
	t.Helper()
	id := newID()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,$2,'p','u')`, id, orgID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return id
}

func TestCreateAndGet(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	pid := seedProject(t, pool, "org-a")
	a, err := st.Create(ctx, CreateInput{
		ProjectID: pid, ShotID: "shot1", TodoID: "todo1", Type: "image",
		BlobKey: "k.png", Prompt: "p", Style: "国风", Provider: "fake", Model: "m",
		Status: "pending_acceptance", Tags: []string{"hero"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if a.Version != 1 || a.ParentAssetID != "" {
		t.Fatalf("first version should be 1 with no parent: %+v", a)
	}
	got, err := st.Get(ctx, a.ID)
	if err != nil || got.BlobKey != "k.png" || got.Status != "pending_acceptance" {
		t.Fatalf("get mismatch: %v %+v", err, got)
	}
}

func TestRegenerateBumpsVersionWithLineage(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	pid := seedProject(t, pool, "org-b")
	v1, _ := st.Create(ctx, CreateInput{ProjectID: pid, ShotID: "s", Type: "image", Prompt: "old", Status: "rejected"})
	// Regenerate: new row, version+1, parent = v1.
	v2, err := st.CreateVersion(ctx, v1.ID, CreateInput{ProjectID: pid, ShotID: "s", Type: "image", Prompt: "edited", Status: "generating"})
	if err != nil {
		t.Fatalf("createVersion: %v", err)
	}
	if v2.Version != 2 || v2.ParentAssetID != v1.ID {
		t.Fatalf("v2 lineage wrong: version=%d parent=%q", v2.Version, v2.ParentAssetID)
	}
	hist, err := st.VersionHistory(ctx, v2.ID)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history: %v len=%d", err, len(hist))
	}
}

func TestSetStatus409Semantics(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	pid := seedProject(t, pool, "org-c")
	a, _ := st.Create(ctx, CreateInput{ProjectID: pid, ShotID: "s", Type: "image", Status: "pending_acceptance"})
	ok, err := st.TransitionStatus(ctx, a.ID, "pending_acceptance", "accepted")
	if err != nil || !ok {
		t.Fatalf("first transition should succeed: %v %v", err, ok)
	}
	// Second transition from pending_acceptance fails (no longer pending) → ok=false.
	ok, err = st.TransitionStatus(ctx, a.ID, "pending_acceptance", "rejected")
	if err != nil || ok {
		t.Fatalf("second transition should be no-op: %v %v", err, ok)
	}
}

func TestLibrarySearchFiltersAndPaginates(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	pid := seedProject(t, pool, "org-lib")
	for i := 0; i < 3; i++ {
		_, _ = st.Create(ctx, CreateInput{ProjectID: pid, ShotID: "s", Type: "image", Style: "国风", Status: "accepted", Tags: []string{"hero"}})
	}
	_, _ = st.Create(ctx, CreateInput{ProjectID: pid, ShotID: "s", Type: "image", Style: "写实", Status: "rejected", Tags: []string{"bg"}})
	items, _, err := st.Library(ctx, LibraryFilter{OrgID: "org-lib", Status: "accepted", Style: "国风", Tag: "hero", Limit: 2})
	if err != nil {
		t.Fatalf("library: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 (limit), got %d", len(items))
	}
	for _, it := range items {
		if it.Status != "accepted" || it.Style != "国风" {
			t.Fatalf("filter leaked: %+v", it)
		}
	}
}
