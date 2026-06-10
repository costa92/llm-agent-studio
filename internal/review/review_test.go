package review

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run review tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func seedPendingAsset(t *testing.T, pool *pgxpool.Pool) (svc *Service, assetID, projectID string) {
	t.Helper()
	ctx := context.Background()
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org','p','u') RETURNING id`).Scan(&projectID)
	as := assets.New(pool)
	a, err := as.Create(ctx, assets.CreateInput{ProjectID: projectID, ShotID: "s1", Type: "image", Prompt: "p", Style: "国风", Status: "pending_acceptance"})
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	return New(as, todos.New(pool)), a.ID, projectID
}

func TestAcceptMovesToAccepted(t *testing.T) {
	pool := testPool(t)
	svc, id, _ := seedPendingAsset(t, pool)
	if err := svc.Accept(context.Background(), id); err != nil {
		t.Fatalf("accept: %v", err)
	}
	got, _ := assets.New(pool).Get(context.Background(), id)
	if got.Status != "accepted" {
		t.Fatalf("want accepted, got %q", got.Status)
	}
}

func TestAcceptNonPendingReturnsConflict(t *testing.T) {
	pool := testPool(t)
	svc, id, _ := seedPendingAsset(t, pool)
	_ = svc.Accept(context.Background(), id)
	if err := svc.Accept(context.Background(), id); err != ErrConflict {
		t.Fatalf("want ErrConflict on re-accept, got %v", err)
	}
}

func TestRejectMovesToRejected(t *testing.T) {
	pool := testPool(t)
	svc, id, _ := seedPendingAsset(t, pool)
	if err := svc.Reject(context.Background(), id); err != nil {
		t.Fatalf("reject: %v", err)
	}
	got, _ := assets.New(pool).Get(context.Background(), id)
	if got.Status != "rejected" {
		t.Fatalf("want rejected, got %q", got.Status)
	}
}

func TestRegenerateSpawnsVersionAndTodo(t *testing.T) {
	pool := testPool(t)
	svc, id, projectID := seedPendingAsset(t, pool)
	ctx := context.Background()
	newAssetID, todoID, err := svc.Regenerate(ctx, id, "edited prompt")
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	// parent moved to rejected; new asset is v2 generating with parent lineage.
	parent, _ := assets.New(pool).Get(ctx, id)
	if parent.Status != "rejected" {
		t.Fatalf("parent should be rejected after regenerate, got %q", parent.Status)
	}
	child, _ := assets.New(pool).Get(ctx, newAssetID)
	if child.Version != 2 || child.ParentAssetID != id {
		t.Fatalf("child lineage wrong: v=%d parent=%q", child.Version, child.ParentAssetID)
	}
	// A ready asset todo exists carrying the parent + edited prompt.
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM todos WHERE id=$1 AND project_id=$2 AND type='asset' AND status='ready'`, todoID, projectID).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 ready regenerate todo, got %d", n)
	}
}
