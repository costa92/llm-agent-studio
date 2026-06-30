package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
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

// TestClampTargetPages: pageCount → storyboard agent's PBTargetPages soft constraint,
// clamped to [4,24]; 0/未设 passes through as 0 (no constraint).
func TestClampTargetPages(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 0}, {-5, 0}, {1, 4}, {3, 4}, {4, 4}, {12, 12}, {24, 24}, {25, 24}, {100, 24},
	}
	for _, c := range cases {
		if got := clampTargetPages(c.in); got != c.want {
			t.Fatalf("clampTargetPages(%d)=%d want %d", c.in, got, c.want)
		}
	}
}

// capturingStoryboardModel records the system prompt it receives and returns a fixed
// 绘本 shot list, so the storyboard's target-pages constraint (driven by the overlaid
// pageCount) can be asserted end-to-end through the worker.
type capturingStoryboardModel struct {
	llm.ScriptedLLM
	mu     sync.Mutex
	gotSys string
}

func (m *capturingStoryboardModel) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	m.mu.Lock()
	m.gotSys = req.SystemPrompt
	m.mu.Unlock()
	return llm.Response{Text: `{"shots":[` +
		`{"shotNo":1,"camera":"","scene":"封面","action":"","prompt":"封面插图","duration":0},` +
		`{"shotNo":2,"camera":"","scene":"内容","action":"第1页旁白","prompt":"第1页插图","duration":0}]}`}, nil
}

// runPBStoryboardCapture seeds a picturebook project whose latest plan overrides
// pageCount=overridePages, runs the full worker pipeline, and returns the system
// prompt the storyboard agent received. The captured prompt reflects PBTargetPages =
// clampTargetPages(overridden pageCount).
func runPBStoryboardCapture(t *testing.T, overridePages int) string {
	t.Helper()
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

	projID := "pbp_" + randHex3()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, kind, picturebook_config) VALUES ($1,'o','n','u','picturebook',$2)`,
		projID, overlayBaseConfig); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	baseCfg, _ := project.ParsePictureBookConfig(overlayBaseConfig)

	planID := "plp_" + projID[4:]
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used, run_inputs) VALUES ($1,$2,'created',true,false,$3)`,
		planID, projID, pbRunInputs(t, baseCfg, map[string]any{"pageCount": overridePages})); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if _, err := todoStore.CreateGraph(ctx, projID, planID, []todos.NodeSpec{
		{LocalID: "s", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"小白兔的故事"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"s"}, InputJSON: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("create graph: %v", err)
	}
	// Keep this project's todos out of OTHER tests' global claim window once we're done.
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `UPDATE todos SET next_run_at = now() + interval '1 hour' WHERE project_id=$1`, projID)
	})

	scriptModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"小白兔","logline":"勇敢的小白兔","scenes":[{"heading":"森林","description":"清晨","dialogue":""}],"characterSheet":"小白兔,蓝背带裤,长耳"}`,
	}))
	capModel := &capturingStoryboardModel{}
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("FAKE"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1,
	})
	w := New(Config{
		DB:         gormDB,
		Todos:      todoStore,
		Projects:   project.New(gormDB),
		Events:     events.New(gormDB),
		Script:     studioagents.NewScriptAgent(scriptModel),
		Storyboard: studioagents.NewStoryboardAgent(capModel),
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fakeGen),
		Storage:    testStorage(),
		Assets:     assets.New(gormDB),
		Cost:       cost.New(gormDB),
		WorkerID:   "test-pgpages",
	})
	for i := 0; i < 30; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}
	capModel.mu.Lock()
	defer capModel.mu.Unlock()
	if capModel.gotSys == "" {
		t.Fatalf("storyboard model never invoked (gotSys empty)")
	}
	return capModel.gotSys
}

// TestRunStoryboard_PageCountOverrideFeedsTargetPages: overriding pageCount via
// run_inputs feeds the storyboard agent a "约 N 页" soft constraint; out-of-range
// values clamp to [4,24].
func TestRunStoryboard_PageCountOverrideFeedsTargetPages(t *testing.T) {
	if sys := runPBStoryboardCapture(t, 12); !strings.Contains(sys, "12 页") {
		t.Fatalf("pageCount=12 override should inject \"12 页\": %q", sys)
	}
	// Out-of-range override clamps to 24.
	if sys := runPBStoryboardCapture(t, 100); !strings.Contains(sys, "24 页") {
		t.Fatalf("pageCount=100 override should clamp to \"24 页\": %q", sys)
	}
}
