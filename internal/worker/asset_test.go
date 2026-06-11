package worker

import (
	"context"
	"os"
	"strings"
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

func TestProcessDiscardsAssetWhenTodoCanceledMidFlight(t *testing.T) {
	// Race window: cancel lands AFTER SetBlob flipped the asset to
	// pending_acceptance but BEFORE MarkDone. MarkDone returns false (todo no
	// longer 'running'); the M3 discard path must push the orphan asset to a
	// terminal 'canceled' instead of stranding it in review.
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_dc','p','u') RETURNING id`).Scan(&pid)
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, pid, `{"shotId":"s1","shotPrompt":"x","style":""}`)
	// Cancel the project FIRST: the todo leaves 'running', generating assets are
	// swept; the worker then completes its in-flight processing of the claim.
	if err := project.New(pool).Cancel(ctx, pid); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	fake := generate.NewFakeLooping(generate.GenResult{Bytes: []byte("PNG"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1})
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), fake),
		Blob:     blob.NewFake(),
		Assets:   assets.New(pool),
		Cost:     cost.New(pool),
		WorkerID: "discarder", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	w.process(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"x","style":""}`)})
	// The asset row created during processing must NOT be left pending_acceptance.
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM assets WHERE project_id=$1`, pid).Scan(&status); err != nil {
		t.Fatalf("load asset: %v", err)
	}
	if status != "canceled" {
		t.Fatalf("orphan asset status = %q, want canceled", status)
	}
	// And no todo_finished event was emitted for the discarded work.
	var nFinished int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE project_id=$1 AND kind='todo_finished'`, pid).Scan(&nFinished)
	if nFinished != 0 {
		t.Fatalf("discarded work must not emit todo_finished, got %d", nFinished)
	}
}

// slowGen blocks until the ctx is canceled — proves the per-call timeout
// (WORKER_CALL_TIMEOUT < lease) bounds a single agent/generator call.
type slowGen struct{}

func (slowGen) Kind() string { return "image" }
func (slowGen) Generate(ctx context.Context, _ generate.GenRequest) (generate.GenResult, error) {
	<-ctx.Done()
	return generate.GenResult{}, ctx.Err()
}

func TestProcessAppliesCallTimeout(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_to','p','u') RETURNING id`).Scan(&pid)
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, pid, `{"shotId":"s1","shotPrompt":"x","style":""}`)
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:       studioagents.NewAssetAgent(prompt.NewBuilder(), slowGen{}),
		Blob:        blob.NewFake(),
		Assets:      assets.New(pool),
		Cost:        cost.New(pool),
		WorkerID:    "timeouter",
		Lease:       time.Minute,
		MaxAttempts: 1, // exhaust on first failure → terminal failed
		BaseBackoff: time.Millisecond,
		CallTimeout: 50 * time.Millisecond,
	})
	done := make(chan struct{})
	go func() {
		w.process(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
			input: []byte(`{"shotId":"s1","shotPrompt":"x","style":""}`)})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("process did not return — call timeout not applied")
	}
	var status, errMsg string
	_ = pool.QueryRow(ctx, `SELECT status, error FROM todos WHERE id=$1`, todoID).Scan(&status, &errMsg)
	if status != "failed" || !strings.Contains(errMsg, "deadline") {
		t.Fatalf("todo = %q err=%q, want failed with deadline error", status, errMsg)
	}
	// 评审修复 I1: the failure cleanup must outlive the expired dispatch ctx —
	// the asset row terminal-states to 'failed'; it must NOT strand in
	// 'generating' (cleanup SetBlob on the timed-out ctx would silently no-op).
	var assetStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM assets WHERE project_id=$1`, pid).Scan(&assetStatus); err != nil {
		t.Fatalf("load asset: %v", err)
	}
	if assetStatus != "failed" {
		t.Fatalf("asset status = %q, want failed (cleanup ran on an expired ctx?)", assetStatus)
	}
}

func TestRunStoryboardUsesParentScriptTodo(t *testing.T) {
	// M1 carry: "latest script ORDER BY created_at DESC" picks the WRONG script
	// when a newer one exists (e.g. a re-run). The storyboard todo's depends_on
	// parent carries output_ref='script:<id>' — resolve through it.
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_par','p','u') RETURNING id`).Scan(&pid)
	// The REAL upstream script + its done script todo.
	scriptID := newID()
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id, project_id, todo_id, content_json) VALUES ($1,$2,'t0','{}')`, scriptID, pid)
	parentTodo := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,output_ref) VALUES ($1,$2,'plan','script','done',$3)`,
		parentTodo, pid, "script:"+scriptID)
	// A DECOY script created later — the old heuristic would pick this one.
	decoyID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO scripts (id, project_id, todo_id, content_json, created_at) VALUES ($1,$2,'t9','{}', now() + interval '1 hour')`,
		decoyID, pid)
	// The storyboard todo depends on the real script todo.
	sbTodo := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,depends_on,input_json) VALUES ($1,$2,'plan','storyboard','running',$3,'{}')`,
		sbTodo, pid, []string{parentTodo})
	fake := generate.NewFakeLooping(generate.GenResult{Bytes: []byte("P"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1})
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Storyboard: newStoryboardAgentWithShots(t, 1),
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fake),
		Blob:       blob.NewFake(), Assets: assets.New(pool), Cost: cost.New(pool),
		WorkerID: "parent", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	ref, err := w.runStoryboard(ctx, claimed{todoID: sbTodo, projectID: pid, typ: "storyboard", attempts: 1, input: []byte(`{}`)})
	if err != nil {
		t.Fatalf("runStoryboard: %v", err)
	}
	if ref != "shots:"+scriptID {
		t.Fatalf("storyboard resolved %q, want shots:%s (the depends_on parent, not the newer decoy)", ref, scriptID)
	}
}
