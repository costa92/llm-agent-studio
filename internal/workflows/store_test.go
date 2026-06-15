package workflows

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// sameJSON compares two JSON payloads ignoring whitespace/key-order (JSONB
// re-serializes on store, so byte equality is wrong).
func sameJSON(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

func uniqueSuffix() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run workflows tests")
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
	return New(st.Pool()), st.Pool()
}

// newProject creates a real project row (workflows FK to projects) and returns its id.
func newProject(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ps := project.New(pool)
	p, err := ps.Create(context.Background(), project.CreateInput{
		OrgID: "org_wf_" + uniqueSuffix(), Name: "WF Project", Brief: "b",
		ContentType: "ad", TargetPlatform: "tiktok", Style: "clean", CreatedBy: "u1",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p.ID
}

func TestCreateGetWorkflow(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	nodes := json.RawMessage(`[{"id":"n1","type":"script","promptId":"","dependsOn":[]}]`)
	w, err := s.Create(ctx, pid, "工作流 A", nodes)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.ID == "" || w.ProjectID != pid || w.Name != "工作流 A" {
		t.Fatalf("create result mismatch: %+v", w)
	}
	if w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() {
		t.Fatalf("create did not return timestamps: %+v", w)
	}

	got, err := s.Get(ctx, pid, w.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// JSONB re-serializes (whitespace differs), so compare semantically.
	if got.Name != "工作流 A" || !sameJSON(t, got.Nodes, nodes) {
		t.Fatalf("get round-trip mismatch: %+v", got)
	}

	// Cross-project Get is ErrNotFound (scoped by project_id), not a leak.
	if _, err := s.Get(ctx, "other-project", w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project get: want ErrNotFound, got %v", err)
	}
}

func TestCreateDefaultsEmptyNodes(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	w, err := s.Create(ctx, pid, "空节点", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if string(w.Nodes) != "[]" {
		t.Fatalf("nil nodes should default to []: %q", w.Nodes)
	}
	if _, err := s.Create(ctx, pid, "", nil); err == nil {
		t.Fatalf("empty name should error")
	}
}

func TestUpdateWorkflow(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	w, _ := s.Create(ctx, pid, "old", json.RawMessage(`[]`))
	updated, err := s.Update(ctx, pid, w.ID, "new", json.RawMessage(`[{"id":"x","type":"asset","promptId":"","dependsOn":[]}]`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "new" || string(updated.Nodes) == "[]" {
		t.Fatalf("update not applied: %+v", updated)
	}
	if !updated.UpdatedAt.After(w.UpdatedAt) && !updated.UpdatedAt.Equal(w.UpdatedAt) {
		t.Fatalf("updated_at not bumped: %v -> %v", w.UpdatedAt, updated.UpdatedAt)
	}
	// Wrong project → ErrNotFound.
	if _, err := s.Update(ctx, "other-project", w.ID, "n", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project update: want ErrNotFound, got %v", err)
	}
}

func TestDeleteWorkflow(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	w, _ := s.Create(ctx, pid, "del", nil)
	if err := s.Delete(ctx, pid, w.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete(ctx, pid, w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: want ErrNotFound, got %v", err)
	}
}

func TestListByProjectDerivesLatestRunStatus(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	wNoRun, _ := s.Create(ctx, pid, "never run", nil)
	wRun, _ := s.Create(ctx, pid, "has run", nil)

	// Seed a plan tagged with wRun + a done todo → latest run status = completed.
	planID := "plan_" + uniqueSuffix()
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, workflow_id, status, valid) VALUES ($1,$2,$3,'created',true)`,
		planID, pid, wRun.ID); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id, project_id, plan_id, type, status) VALUES ($1,$2,$3,'script','done')`,
		"todo_"+uniqueSuffix(), pid, planID); err != nil {
		t.Fatalf("seed todo: %v", err)
	}

	list, err := s.ListByProject(ctx, pid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 workflows, got %d", len(list))
	}
	byID := map[string]Workflow{}
	for _, w := range list {
		byID[w.ID] = w
	}
	if got := byID[wNoRun.ID]; got.LatestPlanID != "" || got.LatestRunStatus != "" {
		t.Fatalf("never-run workflow should have empty run status: %+v", got)
	}
	if got := byID[wRun.ID]; got.LatestPlanID != planID || got.LatestRunStatus != "completed" {
		t.Fatalf("run workflow status: want plan=%s completed, got %+v", planID, got)
	}
}
