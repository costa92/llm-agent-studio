package assets

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
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run assets store tests")
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
	// 用 unique 后缀避免共享测试池里硬编码 todo/shot ID 撞 assets_todo_uniq。
	uniq := newID()
	a, err := st.Create(ctx, CreateInput{
		ProjectID: pid, ShotID: "shot_" + uniq, TodoID: "todo_" + uniq, Type: "image",
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

func TestSetPrescreenAndReadBack(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pid := seedProject(t, pool, "org_prescreen")
	s := New(pool)
	a, err := s.Create(ctx, CreateInput{ProjectID: pid, Status: "pending_acceptance", Prompt: "x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PrescreenScore != -1 {
		t.Fatalf("fresh asset PrescreenScore = %d, want -1 (unscreened)", got.PrescreenScore)
	}
	if err := s.SetPrescreen(ctx, a.ID, 87, []string{"minor_blur"}, "ok"); err != nil {
		t.Fatalf("set prescreen: %v", err)
	}
	got, err = s.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	if got.PrescreenScore != 87 || len(got.PrescreenFlags) != 1 || got.PrescreenNote != "ok" {
		t.Fatalf("prescreen not persisted: %+v", got)
	}
}

func TestGetOrCreateForTodoIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pid := seedProject(t, pool, "org_goc")
	s := New(pool)
	todoID := "todo_goc_1"
	a1, err := s.GetOrCreateForTodo(ctx, CreateInput{ProjectID: pid, TodoID: todoID, Type: "video", Status: "generating"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	a2, err := s.GetOrCreateForTodo(ctx, CreateInput{ProjectID: pid, TodoID: todoID, Type: "video", Status: "generating"})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a1.ID != a2.ID {
		t.Fatalf("GetOrCreateForTodo must reuse the same row: %q vs %q", a1.ID, a2.ID)
	}
}

func TestSetSubmittedAndAsyncFailedAndInFlightCount(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pid := seedProject(t, pool, "org_async")
	s := New(pool)
	// 唯一 todo id 避免共享测试池撞 assets_todo_uniq。
	asyncTodoID := "todo_async_" + newID()
	a, err := s.GetOrCreateForTodo(ctx, CreateInput{ProjectID: pid, TodoID: asyncTodoID, Type: "video", Status: "generating"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.SetSubmitted(ctx, a.ID, "extjob-1"); err != nil {
		t.Fatalf("set submitted: %v", err)
	}
	got, _ := s.Get(ctx, a.ID)
	if got.Status != "submitted" || got.ExternalJobID != "extjob-1" {
		t.Fatalf("after submit: %+v", got)
	}
	// In-flight count by kind sees this submitted video.
	n, err := s.CountInFlightByKind(ctx, "video")
	if err != nil || n < 1 {
		t.Fatalf("CountInFlightByKind(video) = %d err=%v, want >=1", n, err)
	}
	// SetAsyncFailed terminal-states from submitted (B2: not just generating).
	if err := s.SetAsyncFailed(ctx, a.ID); err != nil {
		t.Fatalf("async failed: %v", err)
	}
	got, _ = s.Get(ctx, a.ID)
	if got.Status != "failed" {
		t.Fatalf("after async-fail: %q, want failed", got.Status)
	}
}

// TestCountInFlightByKindOrg verifies the per-org admission count (issue #21)
// isolates orgs: org A's submitted video does NOT inflate org B's per-org count,
// while the global CountInFlightByKind still sees both.
func TestCountInFlightByKindOrg(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := New(pool)
	pidA := seedProject(t, pool, "org_perorg_A")
	pidB := seedProject(t, pool, "org_perorg_B")
	for _, pid := range []string{pidA, pidB} {
		a, err := s.GetOrCreateForTodo(ctx, CreateInput{ProjectID: pid, TodoID: "t_" + pid, Type: "video", Status: "generating"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := s.SetSubmitted(ctx, a.ID, "ext-"+pid); err != nil {
			t.Fatalf("set submitted: %v", err)
		}
	}
	nA, err := s.CountInFlightByKindOrg(ctx, "video", "org_perorg_A")
	if err != nil || nA != 1 {
		t.Fatalf("CountInFlightByKindOrg(video, A) = %d err=%v, want 1", nA, err)
	}
	// audio for org A is unaffected by the video submit.
	if nAudio, err := s.CountInFlightByKindOrg(ctx, "audio", "org_perorg_A"); err != nil || nAudio != 0 {
		t.Fatalf("CountInFlightByKindOrg(audio, A) = %d err=%v, want 0", nAudio, err)
	}
}

func TestSetBlobAdvancesFromSubmitted(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pid := seedProject(t, pool, "org_setblob")
	s := New(pool)
	// 唯一 todo id 避免共享测试池撞 assets_todo_uniq。
	sbTodoID := "todo_sb_" + newID()
	a, _ := s.GetOrCreateForTodo(ctx, CreateInput{ProjectID: pid, TodoID: sbTodoID, Type: "video", Status: "generating"})
	_ = s.SetSubmitted(ctx, a.ID, "j")
	// poll-done completion: SetBlob from-guard must accept 'submitted' now, and
	// report won=true (it advanced exactly one row).
	won, err := s.SetBlob(ctx, a.ID, "k", "u", "fake", "fake-video-async", "pending_acceptance")
	if err != nil {
		t.Fatalf("set blob: %v", err)
	}
	if !won {
		t.Fatalf("SetBlob must report won=true when it advances submitted→pending_acceptance")
	}
	got, _ := s.Get(ctx, a.ID)
	if got.Status != "pending_acceptance" {
		t.Fatalf("SetBlob did not advance submitted→pending_acceptance: %q", got.Status)
	}
	// A second SetBlob on the now-pending_acceptance row is a no-op: won=false (the
	// F-INT-1 loser signal).
	won2, err := s.SetBlob(ctx, a.ID, "k2", "u2", "fake", "fake-video-async", "pending_acceptance")
	if err != nil {
		t.Fatalf("set blob #2: %v", err)
	}
	if won2 {
		t.Fatalf("SetBlob on an already-advanced row must report won=false (F-INT-1 loser signal)")
	}
}
