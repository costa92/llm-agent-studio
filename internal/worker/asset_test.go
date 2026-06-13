package worker

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-contract/llm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
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
		Storage:  testStorage(),
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

	ms := models.New(pool, nil)
	if _, err := ms.Create(ctx, models.CreateInput{
		OrgID: orgID, Kind: "image", Provider: "fakeB", Model: "mB", Enabled: true, IsDefault: true,
	}); err != nil {
		t.Fatalf("create model config: %v", err)
	}

	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), defGen),
		Storage:  testStorage(),
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
		Storage:  testStorage(),
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

// flakyGen fails its first failUntil Generate calls, then succeeds with ok.
// Proves the sync asset retry path reuses the same asset row (BUG #3) instead
// of inserting a duplicate that violates assets_todo_uniq.
type flakyGen struct {
	mu        sync.Mutex
	calls     int
	failUntil int
	ok        generate.GenResult
}

func (g *flakyGen) Kind() string { return "image" }
func (g *flakyGen) Generate(context.Context, generate.GenRequest) (generate.GenResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.calls <= g.failUntil {
		return generate.GenResult{}, fmt.Errorf("flakyGen: induced failure #%d", g.calls)
	}
	return g.ok, nil
}

// TestRunAssetSyncRetryReusesRowNoDuplicateKey proves BUG #3: a sync image asset
// todo whose FIRST generation fails (asset row → 'failed') must, on retry, REUSE
// the same asset row via GetOrCreateForTodo — no assets_todo_uniq duplicate-key
// error — and the successful retry must reach 'pending_acceptance'.
func TestRunAssetSyncRetryReusesRowNoDuplicateKey(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_retry','p','u') RETURNING id`).Scan(&pid)
	todoID := newID()
	in := `{"shotId":"s1","shotPrompt":"a teahouse","style":"国风"}`
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, pid, in)

	gen := &flakyGen{failUntil: 1, ok: generate.GenResult{
		Bytes: []byte("PNG"), MimeType: "image/png", Provider: "fake", Model: "m", ImageCount: 1,
	}}
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), gen),
		Storage:  testStorage(),
		Assets:   assets.New(pool),
		Cost:     cost.New(pool),
		WorkerID: "retry", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	// Dispatch 1: generation fails → asset row terminal-stated 'failed', error returned.
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(in)}); err == nil {
		t.Fatalf("first dispatch must fail (induced generation error)")
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE todo_id=$1`, todoID).Scan(&n)
	if n != 1 {
		t.Fatalf("after first failed dispatch: want exactly 1 asset row, got %d", n)
	}
	// Dispatch 2 (retry): must REUSE the same row (no duplicate-key) and succeed.
	ref, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 2,
		input: []byte(in)})
	if err != nil {
		t.Fatalf("retry dispatch must succeed reusing the row, got: %v", err)
	}
	if ref == "" {
		t.Fatalf("retry must return an asset output ref")
	}
	// Still exactly ONE asset row, now pending_acceptance.
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE todo_id=$1`, todoID).Scan(&n)
	if n != 1 {
		t.Fatalf("retry must reuse the row, found %d asset rows (duplicate insert?)", n)
	}
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM assets WHERE todo_id=$1`, todoID).Scan(&status)
	if status != "pending_acceptance" {
		t.Fatalf("reused row status = %q, want pending_acceptance", status)
	}
}

// TestRunAssetSyncPermanentFailureTerminalFailsViaProcess proves a permanently-
// failing sync generation eventually terminal-fails the todo (status 'failed')
// through the full process()/fail() retry loop — it must NOT loop forever on
// duplicate-key (BUG #3 regression).
func TestRunAssetSyncPermanentFailureTerminalFailsViaProcess(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_permfail','p','u') RETURNING id`).Scan(&pid)
	todoID := newID()
	in := `{"shotId":"s1","shotPrompt":"x","style":""}`
	// Seed 'ready' so claim() can pick it up (a 'running' row with locked_until=NULL
	// is not claimable). Push other projects' claimable rows out of the window so
	// the bounded loop spends its budget on OUR todo (shared package DB).
	_, _ = pool.Exec(ctx, `
		UPDATE todos SET next_run_at = now() + interval '1 hour',
		    locked_until = CASE WHEN status='running' THEN now() + interval '1 hour' ELSE locked_until END
		WHERE project_id <> $1 AND status IN ('ready','running')`, pid)
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','ready',$3)`,
		todoID, pid, in)

	// failUntil huge → always fails.
	gen := &flakyGen{failUntil: 1 << 30}
	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), gen),
		Storage:  testStorage(),
		Assets:   assets.New(pool),
		Cost:     cost.New(pool),
		WorkerID: "permfail", Lease: time.Minute, MaxAttempts: 2, BaseBackoff: 0,
	})
	// Drive the full claim→process loop. Each failed attempt reschedules the todo
	// (BaseBackoff=0 → immediately re-claimable); the retry must NOT hit duplicate-
	// key, so attempts climb to MaxAttempts and the todo terminal-fails. Bound the
	// loop so a duplicate-key spin can't hang the test; the empty-claim break ends it.
	for i := 0; i < 20; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}
	var status, errMsg string
	_ = pool.QueryRow(ctx, `SELECT status, error FROM todos WHERE id=$1`, todoID).Scan(&status, &errMsg)
	if status != "failed" {
		t.Fatalf("permanently-failing todo status = %q, want failed", status)
	}
	if strings.Contains(errMsg, "duplicate key") || strings.Contains(errMsg, "assets_todo_uniq") {
		t.Fatalf("todo must not terminal-fail on a duplicate-key loop: %q", errMsg)
	}
	// Exactly one asset row exists (reused across retries), terminal-stated failed.
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE todo_id=$1`, todoID).Scan(&n)
	if n != 1 {
		t.Fatalf("want exactly 1 (reused) asset row, got %d", n)
	}
}

// TestRunAssetWithDevFakeGeneratorReachesPendingAcceptance proves FIX D: the
// keyless dev fake generator (generate.NewDevFakeGenerator) drives a sync image
// asset todo all the way to 'pending_acceptance' with non-empty stored bytes —
// i.e. a deployment with NO provider API keys can run the image path end-to-end.
func TestRunAssetWithDevFakeGeneratorReachesPendingAcceptance(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_fake','p','u') RETURNING id`).Scan(&pid)
	todoID := newID()
	in := `{"shotId":"s1","shotPrompt":"a teahouse","style":"国风"}`
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','asset','running',$3)`,
		todoID, pid, in)

	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:    studioagents.NewAssetAgent(prompt.NewBuilder(), generate.NewDevFakeGenerator()),
		Storage:  testStorage(),
		Assets:   assets.New(pool),
		Cost:     cost.New(pool),
		WorkerID: "fakegen", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
	ref, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1, input: []byte(in)})
	if err != nil {
		t.Fatalf("runAsset with dev fake generator: %v", err)
	}
	if ref == "" {
		t.Fatalf("expected asset output ref")
	}
	var status, blobKey, provider string
	if err := pool.QueryRow(ctx, `SELECT status, blob_key, provider FROM assets WHERE project_id=$1`, pid).Scan(&status, &blobKey, &provider); err != nil {
		t.Fatalf("load asset: %v", err)
	}
	if status != "pending_acceptance" || blobKey == "" || provider != "fake" {
		t.Fatalf("keyless asset wrong: status=%q key=%q provider=%q", status, blobKey, provider)
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
		Storage:     testStorage(),
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
		Storage:    testStorage(), Assets: assets.New(pool), Cost: cost.New(pool),
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
		Storage:  testStorage(),
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
	// 用 unique 后缀避免共享测试池里硬编码 "a0" / "t0" 撞 generations_asset_todo_uniq。
	seedAssetID := "as_seed_" + randHex3()
	seedTodoID := "td_seed_" + randHex3()
	if err := cs.Record(ctx, cost.Generation{
		ProjectID: pid, AssetID: seedAssetID, TodoID: seedTodoID, Kind: "image",
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
		Storage:  testStorage(),
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
		Storage:    testStorage(), Assets: assets.New(pool), Cost: cost.New(pool),
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

// asyncWorkerSetup builds a worker wired with a FakeAsync video generator routed
// via an org default model_config. pollsToDone controls the poll lifecycle length.
func asyncWorkerSetup(t *testing.T, pool *pgxpool.Pool, pollsToDone int) (*Worker, string, string, *generate.FakeAsync) {
	t.Helper()
	ctx := context.Background()
	orgID := "org_async_" + randHex3()
	var pid string
	_ = pool.QueryRow(ctx, `INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`, orgID).Scan(&pid)
	// Seed video per-second pricing so the ledger asserts a real amount.
	_, _ = pool.Exec(ctx, `INSERT INTO pricing (provider, model, kind, micros_per_second)
		VALUES ('fake','fake-video-async','video',500000) ON CONFLICT (provider, model) DO NOTHING`)
	ms := models.New(pool, nil)
	_, _ = ms.Create(ctx, models.CreateInput{OrgID: orgID, Kind: "video", Provider: "fake", Model: "fake-video-async", Enabled: true, IsDefault: true})

	fakeAsync := generate.NewFakeAsync("video", pollsToDone, generate.GenResult{
		URL: "", Bytes: []byte("VIDEO"), MimeType: "video/mp4", Provider: "fake", Model: "fake-video-async",
	})
	reg := generate.NewRegistry()
	reg.SetDefault(generate.NewFakeLooping(generate.GenResult{Bytes: []byte("IMG"), Provider: "img", Model: "i", ImageCount: 1}))
	reg.Register("fake", "fake-video-async", fakeAsync)

	w := New(Config{
		Pool: pool, Todos: todos.New(pool), Projects: project.New(pool), Events: events.New(pool),
		Asset:   studioagents.NewAssetAgent(prompt.NewBuilder(), generate.NewFakeLooping(generate.GenResult{Provider: "img"})),
		Storage: testStorage(), Assets: assets.New(pool), Cost: cost.New(pool),
		Models: ms, Registry: reg,
		WorkerID: "async", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
		PollBackoff: time.Millisecond, MaxPollBackoff: time.Millisecond, MaxPollAttempts: 60,
	})
	return w, pid, orgID, fakeAsync
}

func seedVideoAssetTodo(t *testing.T, pool *pgxpool.Pool, pid string) string {
	t.Helper()
	todoID := newID()
	// Seed as claim() would leave it: running + locked_by the async worker. The
	// reschedule UPDATEs guard on locked_by=$worker AND status='running', so the
	// lease owner must match (real dispatches always arrive post-claim).
	_, _ = pool.Exec(context.Background(),
		`INSERT INTO todos (id,project_id,plan_id,type,status,locked_by,locked_until,input_json)
		 VALUES ($1,$2,'plan','asset','running','async',now()+interval '1 minute',$3)`,
		todoID, pid, `{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)
	return todoID
}

func TestRunAssetSubmitReschedulesThenPollsToDone(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, _ := asyncWorkerSetup(t, pool, 2)
	todoID := seedVideoAssetTodo(t, pool, pid)

	// Dispatch 1 = submit → self-reschedules (errRescheduled), asset submitted.
	_, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)})
	if !errorsIsRescheduled(err) {
		t.Fatalf("submit dispatch must return errRescheduled, got %v", err)
	}
	var status, extJob string
	_ = pool.QueryRow(ctx, `SELECT status, external_job_id FROM assets WHERE project_id=$1`, pid).Scan(&status, &extJob)
	if status != "submitted" || extJob == "" {
		t.Fatalf("after submit: status=%q extJob=%q, want submitted + non-empty", status, extJob)
	}
	// A submit-time generations row pre-registers the estimated cost (I2).
	var preMicros int64
	_ = pool.QueryRow(ctx, `SELECT cost_micros FROM generations WHERE project_id=$1`, pid).Scan(&preMicros)
	if preMicros != 6*500000 {
		t.Fatalf("submit pre-register cost = %d, want %d", preMicros, 6*500000)
	}
	// Re-fetch the (rescheduled, ready) todo's claimed shape for poll dispatches.
	reclaim := func() claimed {
		return claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
			input: []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)}
	}
	// Mark the todo running again (claim would do this) for the poll dispatch.
	_, _ = pool.Exec(ctx, `UPDATE todos SET status='running', locked_by='async' WHERE id=$1`, todoID)
	// Dispatch 2 = poll → pending (pollsToDone=2) → reschedule again.
	if _, err := w.runAsset(ctx, reclaim()); !errorsIsRescheduled(err) {
		t.Fatalf("poll#1 dispatch must reschedule (pending), got %v", err)
	}
	_, _ = pool.Exec(ctx, `UPDATE todos SET status='running', locked_by='async' WHERE id=$1`, todoID)
	// Dispatch 3 = poll → done → pull bytes → pending_acceptance.
	ref, err := w.runAsset(ctx, reclaim())
	if err != nil {
		t.Fatalf("poll#2 dispatch (done) errored: %v", err)
	}
	if ref == "" {
		t.Fatalf("poll-done must return asset:<id> output ref")
	}
	_ = pool.QueryRow(ctx, `SELECT status FROM assets WHERE project_id=$1`, pid).Scan(&status)
	if status != "pending_acceptance" {
		t.Fatalf("after poll-done: %q, want pending_acceptance", status)
	}
	// Ledger backfilled in place (still ONE row, real seconds = 6).
	var nRows, sec int
	_ = pool.QueryRow(ctx, `SELECT count(*), max(video_seconds) FROM generations WHERE project_id=$1`, pid).Scan(&nRows, &sec)
	if nRows != 1 || sec != 6 {
		t.Fatalf("ledger = %d rows / %ds, want 1 row / 6s (I5 in-place backfill)", nRows, sec)
	}
}

func TestProcessReschedulePreservesHealthyAsset(t *testing.T) {
	// I1: process must NOT discardCanceledAsset a healthy submitted asset when
	// runAsset self-reschedules (MarkDone would see done=false).
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, _ := asyncWorkerSetup(t, pool, 3)
	todoID := seedVideoAssetTodo(t, pool, pid)
	w.process(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)})
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM assets WHERE project_id=$1`, pid).Scan(&status)
	if status == "canceled" {
		t.Fatalf("healthy submitted asset was wrongly discarded as canceled (I1 regression)")
	}
	if status != "submitted" {
		t.Fatalf("after submit dispatch: %q, want submitted", status)
	}
	// And the todo is rescheduled ready with attempts reset to 0 (I6).
	var tStatus string
	var attempts, pollAttempts int
	_ = pool.QueryRow(ctx, `SELECT status, attempts, poll_attempts FROM todos WHERE id=$1`, todoID).Scan(&tStatus, &attempts, &pollAttempts)
	if tStatus != "ready" || attempts != 0 {
		t.Fatalf("todo after reschedule: status=%q attempts=%d, want ready + attempts 0 (I6)", tStatus, attempts)
	}
}

func TestSubmitAdmissionCapBlocksNewSubmitButNotPoll(t *testing.T) {
	// B2: when CountInFlightByKind('video') >= cap, a NEW submit is held back
	// (errRescheduled, no attempts spent); but a poll re-claim of an already-
	// submitted asset is NOT blocked (else drain deadlocks).
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, _ := asyncWorkerSetup(t, pool, 5)
	w.cfg.MaxConcurrentVideo = 1
	// Pre-load one in-flight submitted video (occupies the single slot).
	_, _ = pool.Exec(ctx, `INSERT INTO assets (id,project_id,todo_id,type,status,submitted_at) VALUES (md5(random()::text),$1,'occupy','video','submitted',now())`, pid)
	todoID := seedVideoAssetTodo(t, pool, pid)
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)}); !errorsIsRescheduled(err) {
		t.Fatalf("over-cap submit must be held (errRescheduled), got %v", err)
	}
	// The held asset must NOT have been submitted (still generating or absent).
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE todo_id=$1 AND status='submitted'`, todoID).Scan(&n)
	if n != 0 {
		t.Fatalf("over-cap todo must not submit, got %d submitted", n)
	}
}

// TestPerOrgSubmitAdmissionCapHolds proves the issue #21 per-org layer: with the
// GLOBAL cap unlimited (0) but the per-org cap at 1, an org already at 1 in-flight
// video has its NEW submit held (errRescheduled, no attempts spent) — same hold
// semantics as the global cap, gated on the per-org count instead.
func TestPerOrgSubmitAdmissionCapHolds(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, _ := asyncWorkerSetup(t, pool, 5)
	w.cfg.MaxConcurrentVideo = 0       // global unlimited — only the per-org layer can hold
	w.cfg.MaxConcurrentVideoPerOrg = 1 // this org may have at most 1 video in flight
	// Pre-load one in-flight submitted video for this org's project (occupies the
	// slot). Random todo_id avoids the assets_todo_uniq collision across reruns of a
	// reused test DB (a fixed literal would fail the insert on the 2nd run).
	if _, err := pool.Exec(ctx, `INSERT INTO assets (id,project_id,todo_id,type,status,submitted_at) VALUES (md5(random()::text),$1,md5(random()::text),'video','submitted',now())`, pid); err != nil {
		t.Fatalf("seed occupy asset: %v", err)
	}
	todoID := seedVideoAssetTodo(t, pool, pid)
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)}); !errorsIsRescheduled(err) {
		t.Fatalf("over-per-org-cap submit must be held (errRescheduled), got %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE todo_id=$1 AND status='submitted'`, todoID).Scan(&n)
	if n != 0 {
		t.Fatalf("over-per-org-cap todo must not submit, got %d submitted", n)
	}
}

// TestDiscardCanceledAssetSweepsSubmitted proves F2: the cancel-race discard
// from-list must include 'submitted' (async in-flight) — else a submitted asset
// caught in the MarkDone-no-op race falls through and strands.
func TestDiscardCanceledAssetSweepsSubmitted(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, _ := asyncWorkerSetup(t, pool, 2)
	// An in-flight async asset (submitted) that the cancel-race must terminal-state.
	var assetID string
	_ = pool.QueryRow(ctx, `INSERT INTO assets (id,project_id,type,status,submitted_at)
		VALUES (md5(random()::text),$1,'video','submitted',now()) RETURNING id`, pid).Scan(&assetID)
	w.discardCanceledAsset(ctx, claimed{projectID: pid, typ: "asset"}, "asset:"+assetID)
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM assets WHERE id=$1`, assetID).Scan(&status)
	if status != "canceled" {
		t.Fatalf("submitted asset must be discarded to canceled, got %q (F2: 'submitted' missing from discard from-list)", status)
	}
}

// TestPollDonePricesWhenPollOmitsProviderModel proves F3: when a real provider's
// Poll returns only status+URL (empty Provider/Model), the poll-done cost
// backfill must still price from the provider/model stashed on the asset row at
// submit — not overwrite the pre-registered estimate with cost_micros=0.
func TestPollDonePricesWhenPollOmitsProviderModel(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, fakeAsync := asyncWorkerSetup(t, pool, 2)
	fakeAsync.PollOmitsProviderModel = true // poll Done carries NO provider/model
	todoID := seedVideoAssetTodo(t, pool, pid)
	in := []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)

	// Submit dispatch.
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1, input: in}); !errorsIsRescheduled(err) {
		t.Fatalf("submit must reschedule, got %v", err)
	}
	// Poll #1 (pending).
	_, _ = pool.Exec(ctx, `UPDATE todos SET status='running', locked_by='async' WHERE id=$1`, todoID)
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1, input: in}); !errorsIsRescheduled(err) {
		t.Fatalf("poll#1 must reschedule, got %v", err)
	}
	// Poll #2 (done) — provider/model omitted by the poll payload.
	_, _ = pool.Exec(ctx, `UPDATE todos SET status='running', locked_by='async' WHERE id=$1`, todoID)
	if _, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1, input: in}); err != nil {
		t.Fatalf("poll#2 (done) errored: %v", err)
	}
	var micros int64
	var provider string
	_ = pool.QueryRow(ctx, `SELECT cost_micros, provider FROM generations WHERE project_id=$1`, pid).Scan(&micros, &provider)
	if micros != 6*500000 {
		t.Fatalf("poll-done cost = %d, want %d (F3: provider/model fell back to asset row)", micros, 6*500000)
	}
	if provider == "" {
		t.Fatalf("F3: ledger provider must not be zeroed by an omit-provider poll")
	}
}

// TestPollReclaimedByOtherWorkerDoesNotCancel proves F4: when this worker's poll
// guard matches 0 rows because a DIFFERENT worker stuck-reclaimed the (healthy,
// externally-running, PAID) todo, the asset must NOT be canceled — the new owner
// keeps driving it. The stale worker stops benignly (no fail, no discard).
func TestPollReclaimedByOtherWorkerDoesNotCancel(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, _ := asyncWorkerSetup(t, pool, 5) // WorkerID="async"
	in := []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)

	// Seed a submitted asset + a todo whose lease is held by a DIFFERENT worker
	// ("other"), simulating a stuck-reclaim after our lease expired. The asset is
	// healthy and submitted (external job running for real money).
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,locked_by,locked_until,poll_attempts,input_json)
		 VALUES ($1,$2,'plan','asset','running','other',now()+interval '1 minute',1,$3)`,
		todoID, pid, string(in))
	var assetID string
	_ = pool.QueryRow(ctx,
		`INSERT INTO assets (id,project_id,todo_id,type,status,external_job_id,provider,model,submitted_at)
		 VALUES (md5(random()::text),$1,$2,'video','submitted','job1','fake','fake-video-async',now()) RETURNING id`,
		pid, todoID).Scan(&assetID)

	// Drive a poll dispatch as the stale "async" worker. FakeAsync(pollsToDone=5)
	// returns Pending, so rescheduleOrCancel runs — its guard (locked_by='async')
	// matches 0 rows because the row is locked_by='other'. F4: this must be a
	// benign lost-lease stop, NOT a cancel/discard of the healthy paid asset.
	c := claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1, input: in}
	w.process(ctx, c)

	var assetStatus, todoStatus, lockedBy string
	_ = pool.QueryRow(ctx, `SELECT status FROM assets WHERE id=$1`, assetID).Scan(&assetStatus)
	_ = pool.QueryRow(ctx, `SELECT status, locked_by FROM todos WHERE id=$1`, todoID).Scan(&todoStatus, &lockedBy)
	if assetStatus != "submitted" {
		t.Fatalf("reclaimed healthy asset must stay submitted, got %q (F4: stale worker mis-canceled a paid asset)", assetStatus)
	}
	// The other worker's lease must be untouched — our 0-row guarded reschedule
	// must not have stolen/cleared it.
	if todoStatus != "running" || lockedBy != "other" {
		t.Fatalf("other worker's lease must survive: todo status=%q locked_by=%q, want running/other", todoStatus, lockedBy)
	}
}

// errorsIsRescheduled is a test shim around the worker's internal sentinel.
func errorsIsRescheduled(err error) bool {
	return err != nil && err.Error() == "worker: todo rescheduled"
}

// errorsIsLostLease is a test shim around the worker's internal lost-lease sentinel.
func errorsIsLostLease(err error) bool {
	return err != nil && err.Error() == "worker: poll lease lost to another worker"
}

// TestRunAssetAsyncRefusesSubmitForNonGeneratingAsset closes 审计观察 #4: when
// a regenerate dispatch lands on a v2 asset that is NOT in 'generating' (most
// likely 'failed' from a prior async terminalFail, or a partial 'submitted'
// with empty external_job_id), the SUBMIT path must precondition-fail BEFORE
// calling the provider. Without this guard, submitTx's
// `UPDATE assets ... WHERE status='generating'` 0-rows silently, the tx still
// commits (ledger ON CONFLICT no-ops, todo reschedule resets attempts=0/
// poll_attempts=0 every cycle) — so every dispatch wastes a provider Submit
// call AND the budget counters never exhaust, leaving the todo in an infinite
// re-submit loop.
func TestRunAssetAsyncRefusesSubmitForNonGeneratingAsset(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, _ := asyncWorkerSetup(t, pool, 2)
	todoID := seedVideoAssetTodo(t, pool, pid)

	// Seed a v2 asset row already in 'failed' state (simulates prior async
	// terminalFail). Regenerate writes todo_id='' (assets/store.go ~L314: the
	// regenerate path carries empty todo_id to avoid the assets_todo_uniq
	// partial index colliding with the prior version's row).
	failedAssetID := "regen-failed-" + randHex3()
	if _, err := pool.Exec(ctx,
		`INSERT INTO assets (id,project_id,todo_id,type,status,prompt) VALUES ($1,$2,'','video','failed','old')`,
		failedAssetID, pid); err != nil {
		t.Fatalf("seed failed asset: %v", err)
	}

	// Snapshot the 'submitted' asset count before dispatch — fail-fast must NOT
	// add any new submitted rows.
	var beforeSubmitted int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='submitted'`, pid).Scan(&beforeSubmitted)

	_, err := w.runAsset(ctx, claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1,
		input: []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6,"assetId":"` + failedAssetID + `"}`)})

	// Must NOT return errRescheduled (that would mean submitTx ran and pushed
	// the todo back into the re-submit loop). Must NOT return "asset:<id>" (no
	// successful generation either). Must return the precondition error so
	// worker.fail() retires the todo via MaxAttempts.
	if err == nil {
		t.Fatalf("non-'generating' regen asset must precondition-fail, got nil error")
	}
	if errorsIsRescheduled(err) {
		t.Fatalf("non-'generating' regen asset must NOT reschedule (precondition fail-fast), got %v", err)
	}
	if !strings.Contains(err.Error(), "precondition violated") {
		t.Fatalf("expected precondition error, got %v", err)
	}

	// The failed asset row stays untouched (no status flip, no external_job_id).
	var status, extJob string
	_ = pool.QueryRow(ctx, `SELECT status, external_job_id FROM assets WHERE id=$1`, failedAssetID).Scan(&status, &extJob)
	if status != "failed" {
		t.Fatalf("asset status after fail-fast = %q, want unchanged 'failed'", status)
	}
	if extJob != "" {
		t.Fatalf("asset external_job_id after fail-fast = %q, want empty (Submit must not have fired)", extJob)
	}

	// No new 'submitted' asset rows exist (Submit + submitTx skipped end-to-end).
	var afterSubmitted int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='submitted'`, pid).Scan(&afterSubmitted)
	if afterSubmitted != beforeSubmitted {
		t.Fatalf("submitted-asset count changed from %d to %d — Submit must not have fired",
			beforeSubmitted, afterSubmitted)
	}
}

// TestPollDoneDoubleCompleteDoesNotCancelOrDoubleEmit proves F-INT-1: under a
// cross-worker reclaim where BOTH in-flight Polls return Done, the winning
// completeAsync (submitted→pending_acceptance) emits asset_generated once; the
// LOSING completeAsync for the SAME asset (already pending_acceptance) must bow
// out via errLostLease — NO duplicate asset_generated, and process must NOT
// cancel the completed, PAID asset. The SetBlob transition (rowsAffected) is the
// won/lost arbiter. (The existing F4 test only covers the poll-Pending path.)
func TestPollDoneDoubleCompleteDoesNotCancelOrDoubleEmit(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	w, pid, _, _ := asyncWorkerSetup(t, pool, 1)
	in := []byte(`{"shotId":"s1","shotPrompt":"a city","style":"","kind":"video","duration":6}`)

	// Seed a submitted async asset + its running todo, as both racing workers see it.
	todoID := newID()
	_, _ = pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,locked_by,locked_until,poll_attempts,input_json)
		 VALUES ($1,$2,'plan','asset','running','async',now()+interval '1 minute',1,$3)`,
		todoID, pid, string(in))
	var assetID string
	_ = pool.QueryRow(ctx,
		`INSERT INTO assets (id,project_id,todo_id,type,status,external_job_id,provider,model,submitted_at)
		 VALUES (md5(random()::text),$1,$2,'video','submitted','job1','fake','fake-video-async',now()) RETURNING id`,
		pid, todoID).Scan(&assetID)
	asset, err := w.cfg.Assets.Get(ctx, assetID)
	if err != nil {
		t.Fatalf("load seeded asset: %v", err)
	}
	res := generate.GenResult{Bytes: []byte("VIDEO"), MimeType: "video/mp4", Provider: "fake", Model: "fake-video-async"}
	c := claimed{todoID: todoID, projectID: pid, typ: "asset", attempts: 1, input: in}

	// Winner: completeAsync flips submitted→pending_acceptance + emits asset_generated.
	ref, werr := w.completeAsync(ctx, c, asset, 6, res)
	if werr != nil {
		t.Fatalf("winner completeAsync errored: %v", werr)
	}
	if ref != "asset:"+assetID {
		t.Fatalf("winner ref = %q, want asset:%s", ref, assetID)
	}

	// Loser: a lease-lost worker drives completeAsync for the SAME asset, which is
	// now already pending_acceptance. It must bow out via errLostLease WITHOUT
	// emitting a second asset_generated.
	_, lerr := w.completeAsync(ctx, c, asset, 6, res)
	if !errorsIsLostLease(lerr) {
		t.Fatalf("loser completeAsync must return errLostLease, got %v", lerr)
	}

	// (b) The completed PAID asset must stay pending_acceptance, NOT canceled.
	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM assets WHERE id=$1`, assetID).Scan(&status)
	if status != "pending_acceptance" {
		t.Fatalf("completed paid asset = %q, want pending_acceptance (F-INT-1: loser canceled a live paid asset)", status)
	}

	// (c) Exactly ONE asset_generated event was emitted (no duplicate SSE).
	var nGen int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE project_id=$1 AND kind='asset_generated'`, pid).Scan(&nGen)
	if nGen != 1 {
		t.Fatalf("asset_generated events = %d, want exactly 1 (F-INT-1: loser double-emitted)", nGen)
	}
}
