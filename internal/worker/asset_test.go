package worker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-contract/llm"

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

func TestRunAssetPrescreensViaReviewAgent(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_rev','p','u') RETURNING id`).Scan(&pid)
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, pid, `{"shotId":"s1","shotPrompt":"a teahouse","style":""}`)
	fake := generate.NewFakeLooping(generate.GenResult{Bytes: []byte("PNG"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1})
	reviewModel := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: `{"score":42,"flags":["check_copyright"],"note":"meh"}`},
	))
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), fake),
		Review:   studioagents.NewReviewAgent(reviewModel),
		Blob:     blob.NewFake(),
		Assets:   assets.New(pool),
		Cost:     cost.New(pool),
		WorkerID: "prescreener", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a teahouse","style":""}`)}); err != nil {
		t.Fatalf("runAsset: %v", err)
	}
	var score int
	var status string
	if err := pool.QueryRow(ctx, `SELECT prescreen_score, status FROM assets WHERE project_id=$1`, pid).Scan(&score, &status); err != nil {
		t.Fatalf("load asset: %v", err)
	}
	if score != 42 {
		t.Fatalf("prescreen_score = %d, want 42", score)
	}
	// Prescreen is ADVISORY: the asset still rests in pending_acceptance (HITL
	// is the hard gate; prescreen never auto-accepts/rejects).
	if status != "pending_acceptance" {
		t.Fatalf("status = %q, want pending_acceptance", status)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE project_id=$1 AND kind='asset_prescreened'`, pid).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 asset_prescreened event, got %d", n)
	}
}

func TestClaimRespectsGlobalGenerationCap(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_cap','p','u') RETURNING id`).Scan(&pid)
	// One asset todo RUNNING with a live lease occupies the only slot…
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,locked_by,locked_until,input_json)
		 VALUES ($1,$2,'plan','asset','running','other', now() + interval '60 seconds','{}')`, newID(), pid)
	// …so this READY asset todo must not be claimable at cap=1.
	readyID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','ready','{}')`,
		readyID, pid)
	// Neutralize leftover claimable todos from earlier tests in this shared
	// package DB (评审修复 M7): the bounded drains below ORDER BY next_run_at
	// ASC and would otherwise burn their bound on stale rows and flake. Push
	// everything outside this test's project out of the claim window; our rows
	// (project pid) keep next_run_at = now() and sort first.
	_, _ = pool.Exec(ctx, `
		UPDATE todos
		SET next_run_at = now() + interval '1 hour',
		    locked_until = CASE WHEN status='running' THEN now() + interval '1 hour' ELSE locked_until END
		WHERE project_id <> $1 AND status IN ('ready','running')`, pid)

	capped := New(Config{Pool: pool, WorkerID: "capped", Lease: time.Minute, MaxConcurrentGen: 1})
	// Drain claims and assert OUR ready asset todo is never taken while the
	// slot is occupied (pre-existing rows were neutralized above, so the first
	// empty claim ends the loop deterministically).
	for i := 0; i < 20; i++ {
		c, ok, err := capped.claim(ctx)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if !ok {
			break
		}
		if c.todoID == readyID {
			t.Fatalf("cap=1 with an in-flight generation must not claim another asset todo")
		}
	}
	// cap=0 disables the gate: the todo becomes claimable.
	uncapped := New(Config{Pool: pool, WorkerID: "uncapped", Lease: time.Minute})
	claimedOurs := false
	for i := 0; i < 20; i++ {
		c, ok, err := uncapped.claim(ctx)
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if !ok {
			break
		}
		if c.todoID == readyID {
			claimedOurs = true
			break
		}
	}
	if !claimedOurs {
		t.Fatalf("cap=0 should leave the asset todo claimable")
	}
}

func TestRunAssetQuotaBackstopFailsOverQuotaTodo(t *testing.T) {
	// 评审修复 M3: the worker-side quota backstop gets its own failing test.
	// The org has already spent its single quota slot; an asset todo run via
	// runAsset must fail with a quota error, record NO new generation, and
	// (评审修复 M4 锚点) strand NO fresh 'generating' asset row — the check
	// fires before createAsset.
	pool := assetTestPool(t)
	ctx := context.Background()
	orgID := "org_qbs_" + randHex3()
	var pid string
	_ = pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID).Scan(&pid)
	cs := cost.New(pool)
	if err := cs.Record(ctx, cost.Generation{
		ProjectID: pid, AssetID: "a0", TodoID: "t0", Kind: "image",
		Provider: "fake", Model: "m", ImageCount: 1,
	}); err != nil {
		t.Fatalf("seed generation: %v", err)
	}
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, pid, `{"shotId":"s1","shotPrompt":"x","style":""}`)
	fake := generate.NewFakeLooping(generate.GenResult{Bytes: []byte("P"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1})
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), fake),
		Blob:     blob.NewFake(),
		Assets:   assets.New(pool),
		Cost:     cs,
		GenQuota: 1,
		WorkerID: "backstop", Lease: time.Minute, MaxAttempts: 1, BaseBackoff: time.Millisecond,
	})
	_, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"x","style":""}`)})
	if err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("over-quota runAsset must fail with a quota error, got %v", err)
	}
	// No new generation was recorded (only the seed row remains)…
	var nGen int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM generations WHERE project_id=$1`, pid).Scan(&nGen)
	if nGen != 1 {
		t.Fatalf("over-quota run must not record a generation, ledger rows = %d, want 1", nGen)
	}
	// …and the quota check fired BEFORE createAsset: no orphan 'generating' row.
	var nAssets int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1`, pid).Scan(&nAssets)
	if nAssets != 0 {
		t.Fatalf("quota backstop must fire before createAsset, found %d asset rows", nAssets)
	}
}

func TestFanOutWritesKindAndDuration(t *testing.T) {
	// I3: storyboard fan-out must write input_json.kind + duration — without it the
	// per-kind concurrency cap never fires and video billing has no duration. M4
	// shots carry no kind, so fan-out writes the constant "image"; duration comes
	// from the shot. (DB-gated; reuses storyboard helpers.)
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by,style) VALUES (md5(random()::text),'org_fk','p','u','realistic') RETURNING id`).Scan(&pid)
	// Seed a script + its done script todo so runStoryboard resolves a parent.
	scriptID := newID()
	_, _ = pool.Exec(ctx, `INSERT INTO scripts (id, project_id, todo_id, content_json) VALUES ($1,$2,'t0','{}')`, scriptID, pid)
	parentTodo := newID()
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status,output_ref) VALUES ($1,$2,'plan','script','done',$3)`, parentTodo, pid, "script:"+scriptID)
	sbTodo := newID()
	_, _ = pool.Exec(ctx, `INSERT INTO todos (id,project_id,plan_id,type,status,depends_on,input_json) VALUES ($1,$2,'plan','storyboard','running',$3,'{}')`, sbTodo, pid, []string{parentTodo})

	fake := generate.NewFakeLooping(generate.GenResult{Bytes: []byte("P"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1})
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Storyboard: newStoryboardAgentWithShots(t, 1),
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fake),
		Blob:       blob.NewFake(), Assets: assets.New(pool), Cost: cost.New(pool),
		WorkerID: "fanout", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	if _, err := w.runStoryboard(ctx, claimed{todoID: sbTodo, projectID: pid, typ: "storyboard", attempts: 1, input: []byte(`{}`)}); err != nil {
		t.Fatalf("runStoryboard: %v", err)
	}
	var kind string
	var dur int
	if err := pool.QueryRow(ctx,
		`SELECT input_json->>'kind', (input_json->>'duration')::int FROM todos WHERE project_id=$1 AND type='asset' LIMIT 1`,
		pid).Scan(&kind, &dur); err != nil {
		t.Fatalf("read asset todo input: %v", err)
	}
	// M4 fan-out always writes the constant "image" (shots carry no kind).
	if kind != "image" {
		t.Fatalf("fan-out kind = %q, want image (M4 fan-out constant)", kind)
	}
}
