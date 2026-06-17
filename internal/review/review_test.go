package review

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/storage"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run review tests")
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

func seedPendingAsset(t *testing.T, pool *pgxpool.Pool) (svc *Service, assetID, projectID string) {
	t.Helper()
	ctx := context.Background()
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org','p','u') RETURNING id`).Scan(&projectID)
	as := assets.New(pool)
	a, err := as.Create(ctx, assets.CreateInput{ProjectID: projectID, ShotID: "s1", Type: "image", Prompt: "p", Style: "国风", Status: "pending_acceptance"})
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	return New(as, todos.New(pool), pool), a.ID, projectID
}

// seedNarrationShot creates a shots row (action=oldText) plus an audio asset
// (kind=audio) hung on that shot, returning the service + audio asset id + shot
// id. Mirrors a Task-4 picture-book page (audio asset carries the shotId).
func seedNarrationShot(t *testing.T, pool *pgxpool.Pool, oldText string) (svc *Service, audioAssetID, shotID, projectID string) {
	t.Helper()
	ctx := context.Background()
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org','p','u') RETURNING id`).Scan(&projectID)
	_ = pool.QueryRow(ctx, `INSERT INTO shots (id,project_id,script_id,todo_id,shot_no,action) VALUES (md5(random()::text),$1,'sc','td',1,$2) RETURNING id`, projectID, oldText).Scan(&shotID)
	as := assets.New(pool)
	a, err := as.Create(ctx, assets.CreateInput{ProjectID: projectID, ShotID: shotID, Type: "audio", Prompt: oldText, Style: "国风", Status: "pending_acceptance"})
	if err != nil {
		t.Fatalf("seed audio asset: %v", err)
	}
	return New(as, todos.New(pool), pool), a.ID, shotID, projectID
}

func TestRegenerateNarration_UpdatesShotAndRegensAudio(t *testing.T) {
	pool := testPool(t)
	svc, audioID, shotID, _ := seedNarrationShot(t, pool, "旧旁白")
	ctx := context.Background()

	newAssetID, _, err := svc.RegenerateNarration(ctx, audioID, "新旁白")
	if err != nil {
		t.Fatalf("regenerate narration: %v", err)
	}

	// ① the shot's action is now the new narration.
	var action string
	_ = pool.QueryRow(ctx, `SELECT action FROM shots WHERE id=$1`, shotID).Scan(&action)
	if action != "新旁白" {
		t.Fatalf("want shot action 新旁白, got %q", action)
	}
	// ② a new audio version was spawned: kind still audio, prompt=new text.
	child, _ := assets.New(pool).Get(ctx, newAssetID)
	if child.Type != "audio" {
		t.Fatalf("want child kind audio, got %q", child.Type)
	}
	if child.Version != 2 || child.ParentAssetID != audioID {
		t.Fatalf("child lineage wrong: v=%d parent=%q", child.Version, child.ParentAssetID)
	}
	if child.Prompt != "新旁白" {
		t.Fatalf("want child prompt 新旁白, got %q", child.Prompt)
	}
}

func TestRegenerateNarration_EmptyTextErrorsAndKeepsData(t *testing.T) {
	pool := testPool(t)
	svc, audioID, shotID, _ := seedNarrationShot(t, pool, "旧旁白")
	ctx := context.Background()

	if _, _, err := svc.RegenerateNarration(ctx, audioID, ""); err == nil {
		t.Fatalf("want error on empty text")
	}
	// data untouched: shot action unchanged, audio asset still pending.
	var action string
	_ = pool.QueryRow(ctx, `SELECT action FROM shots WHERE id=$1`, shotID).Scan(&action)
	if action != "旧旁白" {
		t.Fatalf("shot action should be unchanged, got %q", action)
	}
	got, _ := assets.New(pool).Get(ctx, audioID)
	if got.Status != "pending_acceptance" {
		t.Fatalf("audio asset should be untouched, got status %q", got.Status)
	}
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
