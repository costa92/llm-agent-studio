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
	"gorm.io/gorm"

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
	return New(st.GORM()), st.Pool()
}

func projectGormForTest(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run workflow tests")
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
	return st.GORM()
}

// newProject creates a real project row (workflows FK to projects) and returns its id.
func newProject(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ps := project.New(projectGormForTest(t))
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
	w, err := s.Create(ctx, pid, "工作流 A", nodes, nil, nil)
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

func TestWorkflowInputsSchemaRoundTrip(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	schema := json.RawMessage(`[{"name":"heroName","label":"主角","type":"text","target":"variable","required":true}]`)
	w, err := s.Create(ctx, pid, "带 schema", json.RawMessage(`[]`), schema, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !sameJSON(t, w.InputsSchema, schema) {
		t.Fatalf("create did not return inputs_schema: %q", w.InputsSchema)
	}

	got, err := s.Get(ctx, pid, w.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sameJSON(t, got.InputsSchema, schema) {
		t.Fatalf("get round-trip inputs_schema mismatch: %q", got.InputsSchema)
	}

	// Update replaces the schema.
	schema2 := json.RawMessage(`[{"name":"voice","type":"select","target":"variable","options":[{"value":"warm"}]}]`)
	upd, err := s.Update(ctx, pid, w.ID, "带 schema", json.RawMessage(`[]`), schema2, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !sameJSON(t, upd.InputsSchema, schema2) {
		t.Fatalf("update inputs_schema mismatch: %q", upd.InputsSchema)
	}

	// ListByProject also carries inputs_schema.
	list, err := s.ListByProject(ctx, pid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, lw := range list {
		if lw.ID == w.ID {
			found = true
			if !sameJSON(t, lw.InputsSchema, schema2) {
				t.Fatalf("list inputs_schema mismatch: %q", lw.InputsSchema)
			}
		}
	}
	if !found {
		t.Fatalf("created workflow not in list")
	}
}

func TestWorkflowSettingsRoundTrip(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	settings := json.RawMessage(`{"style":"皮克斯","contentType":"短视频","targetPlatform":"抖音"}`)
	w, err := s.Create(ctx, pid, "带 settings", json.RawMessage(`[]`), nil, settings)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !sameJSON(t, w.Settings, settings) {
		t.Fatalf("create did not return settings: %q", w.Settings)
	}

	got, err := s.Get(ctx, pid, w.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sameJSON(t, got.Settings, settings) {
		t.Fatalf("get round-trip settings mismatch: %q", got.Settings)
	}

	// nil settings → defaults to '{}' (= 继承项目行), never NULL.
	w2, err := s.Create(ctx, pid, "无 settings", nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if string(w2.Settings) != "{}" {
		t.Fatalf("nil settings should default to {}: %q", w2.Settings)
	}

	// Update replaces settings; ListByProject also carries it.
	settings2 := json.RawMessage(`{"style":"写实"}`)
	upd, err := s.Update(ctx, pid, w.ID, "带 settings", json.RawMessage(`[]`), nil, settings2)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !sameJSON(t, upd.Settings, settings2) {
		t.Fatalf("update settings mismatch: %q", upd.Settings)
	}
	list, err := s.ListByProject(ctx, pid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, lw := range list {
		if lw.ID == w.ID && !sameJSON(t, lw.Settings, settings2) {
			t.Fatalf("list settings mismatch: %q", lw.Settings)
		}
	}
}

func TestWorkflowInputsSchemaDefaultsEmpty(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	// nil schema → defaults to '[]' (not NULL).
	w, err := s.Create(ctx, pid, "无 schema", json.RawMessage(`[]`), nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if string(w.InputsSchema) != "[]" {
		t.Fatalf("nil schema should default to []: %q", w.InputsSchema)
	}
	got, err := s.Get(ctx, pid, w.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.InputsSchema) != "[]" {
		t.Fatalf("get nil schema should be []: %q", got.InputsSchema)
	}
}

func TestCreateDefaultsEmptyNodes(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	w, err := s.Create(ctx, pid, "空节点", nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if string(w.Nodes) != "[]" {
		t.Fatalf("nil nodes should default to []: %q", w.Nodes)
	}
	if _, err := s.Create(ctx, pid, "", nil, nil, nil); err == nil {
		t.Fatalf("empty name should error")
	}
}

func TestUpdateWorkflow(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	w, _ := s.Create(ctx, pid, "old", json.RawMessage(`[]`), nil, nil)
	updated, err := s.Update(ctx, pid, w.ID, "new", json.RawMessage(`[{"id":"x","type":"asset","promptId":"","dependsOn":[]}]`), nil, nil)
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
	if _, err := s.Update(ctx, "other-project", w.ID, "n", nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project update: want ErrNotFound, got %v", err)
	}
}

func TestDeleteWorkflow(t *testing.T) {
	s, pool := newStore(t)
	ctx := context.Background()
	pid := newProject(t, pool)

	w, _ := s.Create(ctx, pid, "del", nil, nil, nil)
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

	wNoRun, _ := s.Create(ctx, pid, "never run", nil, nil, nil)
	wRun, _ := s.Create(ctx, pid, "has run", nil, nil, nil)

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
