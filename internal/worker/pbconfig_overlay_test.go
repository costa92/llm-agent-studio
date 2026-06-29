package worker

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/runinputs"
	"github.com/costa92/llm-agent-studio/internal/storage"
	"github.com/costa92/llm-agent-studio/internal/todos"
	"github.com/jackc/pgx/v5/pgxpool"
)

const overlayBaseConfig = `{"ageBand":"3-6","bookType":"narrative","illustrationStyle":"watercolor","narrationStyle":"plain","voice":"warm","themes":["friendship"],"pageCount":16}`

// pbRunInputs builds a plans.run_inputs snapshot {values, schema} whose schema is the
// real PictureBookSchema(baseCfg) so the worker's re-validation passes — exactly the
// shape the run-handler persists.
func pbRunInputs(t *testing.T, baseCfg project.PictureBookConfig, values map[string]any) []byte {
	t.Helper()
	schema := runinputs.PictureBookSchema(baseCfg)
	vals := map[string]json.RawMessage{}
	for k, v := range values {
		raw, _ := json.Marshal(v)
		vals[k] = raw
	}
	schemaJSON, _ := json.Marshal(schema)
	out, _ := json.Marshal(struct {
		Values map[string]json.RawMessage `json:"values"`
		Schema json.RawMessage            `json:"schema"`
	}{Values: vals, Schema: schemaJSON})
	return out
}

// seedPBPlan inserts a plan (with the given run_inputs) plus a single storyboard todo
// under it, and returns the todo id. The todo's plan_id links it to the run_inputs so
// applyPBOverride reverse-looks it up.
func seedPBPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projID, planID string, runInputs []byte, todoStore *todos.Store) string {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, run_inputs) VALUES ($1,$2,'created',true,false,$3)`,
		planID, projID, runInputs); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	ids, err := todoStore.CreateGraph(ctx, projID, planID, []todos.NodeSpec{
		{LocalID: "b", Type: "storyboard", DependsOn: nil, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	// These todos exist only as a JOIN anchor for applyPBOverride (reverse lookup by
	// id, status-agnostic). Push them out of the global claim window so other worker
	// tests sharing this DB never claim them (they'd run storyboard on a script-less
	// project and pollute timing-sensitive sibling tests).
	if _, err := pool.Exec(ctx,
		`UPDATE todos SET next_run_at = now() + interval '1 hour' WHERE id=$1`, ids["b"]); err != nil {
		t.Fatalf("park todo: %v", err)
	}
	return ids["b"]
}

// TestPictureBookConfig_OverlayAndBaseline: a plan carrying a pbConfig override returns
// the overlaid cfg; a plan with no override returns the baseline; projects.picturebook_config
// is never mutated.
func TestPictureBookConfig_OverlayAndBaseline(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()
	gormDB := st.GORM()
	todoStore := todos.New(gormDB)

	projID := "pbo_" + randHex3()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, kind, picturebook_config) VALUES ($1,'o','n','u','picturebook',$2)`,
		projID, overlayBaseConfig); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	baseCfg, _ := project.ParsePictureBookConfig(overlayBaseConfig)

	// Plan A: override illustrationStyle + ageBand + themes (a complete-ish snapshot).
	ovInputs := pbRunInputs(t, baseCfg, map[string]any{
		"illustrationStyle": "cartoon", "ageBand": "6-8", "themes": []string{"courage", "honesty"},
	})
	todoA := seedPBPlan(t, ctx, pool, projID, "plA_"+projID[4:], ovInputs, todoStore)
	// Plan B: no override (run_inputs '{}').
	todoB := seedPBPlan(t, ctx, pool, projID, "plB_"+projID[4:], []byte(`{}`), todoStore)

	w := New(Config{DB: gormDB})

	isPB, cfgA, err := w.pictureBookConfig(ctx, claimed{todoID: todoA, projectID: projID})
	if err != nil {
		t.Fatalf("pictureBookConfig A: %v", err)
	}
	if !isPB {
		t.Fatalf("plan A: want picturebook=true")
	}
	if cfgA.IllustrationStyle != "cartoon" {
		t.Fatalf("plan A illustrationStyle=%q want cartoon (override)", cfgA.IllustrationStyle)
	}
	if cfgA.AgeBand != "6-8" {
		t.Fatalf("plan A ageBand=%q want 6-8 (override)", cfgA.AgeBand)
	}
	if len(cfgA.Themes) != 2 || cfgA.Themes[0] != "courage" {
		t.Fatalf("plan A themes=%v want [courage honesty]", cfgA.Themes)
	}

	_, cfgB, err := w.pictureBookConfig(ctx, claimed{todoID: todoB, projectID: projID})
	if err != nil {
		t.Fatalf("pictureBookConfig B: %v", err)
	}
	if cfgB.IllustrationStyle != "watercolor" || cfgB.AgeBand != "3-6" {
		t.Fatalf("plan B should return baseline, got style=%q ageBand=%q", cfgB.IllustrationStyle, cfgB.AgeBand)
	}

	// projects.picturebook_config must be untouched (override lives only in plans + memory).
	var rawAfter string
	if err := pool.QueryRow(ctx, `SELECT picturebook_config FROM projects WHERE id=$1`, projID).Scan(&rawAfter); err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if rawAfter != overlayBaseConfig {
		t.Fatalf("projects.picturebook_config mutated:\n got %s\nwant %s", rawAfter, overlayBaseConfig)
	}
}

// TestPictureBookConfig_ConcurrentNoCrossTalk: two plans of the same project carry
// different illustrationStyle overrides; interleaved concurrent lookups each resolve
// to their own plan's value (JOIN scoped by todo id, no cross-talk).
func TestPictureBookConfig_ConcurrentNoCrossTalk(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()
	gormDB := st.GORM()
	todoStore := todos.New(gormDB)

	projID := "pbc_" + randHex3()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, kind, picturebook_config) VALUES ($1,'o','n','u','picturebook',$2)`,
		projID, overlayBaseConfig); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	baseCfg, _ := project.ParsePictureBookConfig(overlayBaseConfig)

	todoA := seedPBPlan(t, ctx, pool, projID, "plca_"+projID[4:],
		pbRunInputs(t, baseCfg, map[string]any{"illustrationStyle": "cartoon"}), todoStore)
	todoB := seedPBPlan(t, ctx, pool, projID, "plcb_"+projID[4:],
		pbRunInputs(t, baseCfg, map[string]any{"illustrationStyle": "flat"}), todoStore)

	w := New(Config{DB: gormDB})

	var wg sync.WaitGroup
	errCh := make(chan error, 40)
	check := func(todoID, want string) {
		defer wg.Done()
		_, cfg, err := w.pictureBookConfig(ctx, claimed{todoID: todoID, projectID: projID})
		if err != nil {
			errCh <- err
			return
		}
		if cfg.IllustrationStyle != want {
			errCh <- &mismatch{todoID, want, cfg.IllustrationStyle}
		}
	}
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go check(todoA, "cartoon")
		go check(todoB, "flat")
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("concurrent cross-talk: %v", e)
	}
}

type mismatch struct {
	todoID, want, got string
}

func (m *mismatch) Error() string {
	return "todo " + m.todoID + " want illustrationStyle " + m.want + " got " + m.got
}
