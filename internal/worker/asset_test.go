package worker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/storage"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

func assetTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker asset tests")
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

func TestRunAssetWritesAssetAndGeneration(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org','p','u') RETURNING id`).Scan(&pid)
	// Seed an asset todo (ready) carrying the shot prompt.
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, pid, `{"shotId":"s1","shotPrompt":"a teahouse","style":"国风"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO pricing (provider, model, kind, micros_per_image, micros_per_1k_tokens)
		VALUES ('fake','m','image',7000,0) ON CONFLICT (provider, model) DO NOTHING`); err != nil {
		t.Fatalf("seed pricing: %v", err)
	}

	fake := generate.NewFakeLooping(generate.GenResult{Bytes: []byte("PNG"), MimeType: "image/png", Provider: "fake", Model: "m", Tokens: 7, ImageCount: 1, LatencyMS: 50})
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), fake),
		Blob:     blob.NewFake(),
		Assets:   assets.New(pool),
		Cost:     cost.New(pool),
		WorkerID: "test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	ref, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a teahouse","style":"国风"}`)})
	if err != nil {
		t.Fatalf("runAsset: %v", err)
	}
	if ref == "" {
		t.Fatalf("expected asset output ref")
	}
	var status, blobKey, provider string
	if err := pool.QueryRow(ctx, `SELECT status, blob_key, provider FROM assets WHERE project_id=$1`, pid).Scan(&status, &blobKey, &provider); err != nil {
		t.Fatalf("load asset: %v", err)
	}
	if status != "pending_acceptance" || blobKey == "" || provider != "fake" {
		t.Fatalf("asset row wrong: status=%q key=%q provider=%q", status, blobKey, provider)
	}
	var genCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM generations WHERE project_id=$1`, pid).Scan(&genCount)
	if genCount != 1 {
		t.Fatalf("want 1 generations row, got %d", genCount)
	}
	var micros int64
	_ = pool.QueryRow(ctx, `SELECT cost_micros FROM generations WHERE project_id=$1`, pid).Scan(&micros)
	if micros != 7000 {
		t.Fatalf("cost_micros = %d, want 7000 (1 image * 7000, M3 pricing)", micros)
	}
}

func TestRunStoryboardFansOutAssetTodos(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	// Seed the project WITH style='国风' — this is the ONLY source of style now
	// (B1). The storyboard todo's input is deliberately EMPTY ('{}') to prove the
	// fan-out no longer depends on the todo input carrying style.
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by,style) VALUES (md5(random()::text),'org','p','u','国风') RETURNING id`).Scan(&pid)
	// Seed a script + a running storyboard todo (empty input — real planner path).
	scriptID := newID()
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id,project_id,todo_id,content_json,version) VALUES ($1,$2,'t','{}',1)`, scriptID, pid)
	sbID := newID()
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','storyboard','running','{}')`, sbID, pid)

	// Storyboard agent (scripted via fakeStoryboard returning 3 shots) — reuse the
	// M1 storyboard agent over a ScriptedLLM with one shots response.
	sbAgent := newStoryboardAgentWithShots(t, 3)
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Storyboard: sbAgent, WorkerID: "test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	// Empty input — style must come from the project, not here.
	ref, err := w.runStoryboard(ctx, claimed{todoID: sbID, projectID: pid, typ: "storyboard", attempts: 1, input: []byte(`{}`)})
	if err != nil {
		t.Fatalf("runStoryboard: %v", err)
	}
	if ref == "" {
		t.Fatalf("expected shots ref")
	}
	var shots, assetTodos int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM shots WHERE project_id=$1`, pid).Scan(&shots)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM todos WHERE project_id=$1 AND type='asset' AND status='ready'`, pid).Scan(&assetTodos)
	if shots != 3 || assetTodos != 3 {
		t.Fatalf("fan-out mismatch: shots=%d assetTodos=%d (want 3/3)", shots, assetTodos)
	}
	// B1: every fanned-out asset todo's input carries the PROJECT style, even
	// though the storyboard todo input was empty.
	var styled int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM todos WHERE project_id=$1 AND type='asset' AND input_json->>'style'='国风'`, pid).Scan(&styled)
	if styled != 3 {
		t.Fatalf("want 3 asset todos with project style '国风', got %d (style not sourced from project row)", styled)
	}
}

// TestRunStoryboardFanOutIsIdempotent guards C1: a second runStoryboard for the
// same storyboard todo (crash/re-claim between commit and MarkDone) must NOT
// insert a second batch of shots + asset todos.
func TestRunStoryboardFanOutIsIdempotent(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by,style) VALUES (md5(random()::text),'org','p','u','国风') RETURNING id`).Scan(&pid)
	scriptID := newID()
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id,project_id,todo_id,content_json,version) VALUES ($1,$2,'t','{}',1)`, scriptID, pid)
	sbID := newID()
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','storyboard','running','{}')`, sbID, pid)
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Storyboard: newStoryboardAgentWithShots(t, 3), WorkerID: "test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	if _, err := w.runStoryboard(ctx, claimed{todoID: sbID, projectID: pid, typ: "storyboard", attempts: 1, input: []byte(`{}`)}); err != nil {
		t.Fatalf("runStoryboard #1: %v", err)
	}
	// Second run (re-claim). StoryboardAgent would yield 3 shots again, but the
	// guard must short-circuit before any insert.
	w2 := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Storyboard: newStoryboardAgentWithShots(t, 3), WorkerID: "test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	if _, err := w2.runStoryboard(ctx, claimed{todoID: sbID, projectID: pid, typ: "storyboard", attempts: 1, input: []byte(`{}`)}); err != nil {
		t.Fatalf("runStoryboard #2: %v", err)
	}
	var shots, assetTodos int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM shots WHERE project_id=$1`, pid).Scan(&shots)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM todos WHERE project_id=$1 AND type='asset'`, pid).Scan(&assetTodos)
	if shots != 3 || assetTodos != 3 {
		t.Fatalf("idempotency broken: after 2 runs shots=%d assetTodos=%d (want 3/3)", shots, assetTodos)
	}
}

func TestRunAssetRoutesViaOrgDefaultModelConfig(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	orgID := "org_route_" + randHex3()
	var pid string
	_ = pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID).Scan(&pid)
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, pid, `{"shotId":"s1","shotPrompt":"a teahouse","style":""}`)

	defGen := generate.NewFakeLooping(generate.GenResult{Provider: "default", Model: "d", Bytes: []byte("D"), ImageCount: 1})
	orgGen := generate.NewFakeLooping(generate.GenResult{Provider: "fakeB", Model: "mB", Bytes: []byte("B"), ImageCount: 1})
	reg := generate.NewRegistry()
	reg.SetDefault(defGen)
	reg.Register("fakeB", "mB", orgGen)

	ms := models.New(pool)
	if _, err := ms.Create(ctx, models.CreateInput{
		OrgID: orgID, Kind: "image", Provider: "fakeB", Model: "mB", Enabled: true, IsDefault: true,
	}); err != nil {
		t.Fatalf("create model config: %v", err)
	}

	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), defGen),
		Blob:     blob.NewFake(),
		Assets:   assets.New(pool),
		Cost:     cost.New(pool),
		Models:   ms,
		Registry: reg,
		WorkerID: "router", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a teahouse","style":""}`)}); err != nil {
		t.Fatalf("runAsset: %v", err)
	}
	var provider string
	if err := pool.QueryRow(ctx, `SELECT provider FROM assets WHERE project_id=$1`, pid).Scan(&provider); err != nil {
		t.Fatalf("load asset: %v", err)
	}
	if provider != "fakeB" {
		t.Fatalf("org default model config did not take effect: asset provider = %q, want fakeB", provider)
	}
}
