package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/assets"
	"github.com/costa92/llm-agent-studio/internal/blob"
	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/generate"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/prompt"
	"github.com/costa92/llm-agent-studio/internal/storage"
	"github.com/costa92/llm-agent-studio/internal/storagerouter"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// testStorage 是 worker 测试用的对象存储路由 shim：以一个内存 fake blob 作为 Default，
// Configs/Build 留空 → BlobStoreFor 始终回落 Default (等价于旧的单一 Blob 依赖)。
func testStorage() *storagerouter.Router {
	return storagerouter.New(storagerouter.Config{Default: blob.NewFake()})
}

func TestWorkerRunsScriptThenStoryboard(t *testing.T) {
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
	projID := "wk_" + randHex3()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	todoStore := todos.New(pool)
	ids, err := todoStore.CreateGraph(ctx, projID, "pl1", []todos.NodeSpec{
		{LocalID: "s", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"coffee ad","style":"realistic"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"s"}, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	// Script model: one JSON script. Storyboard model: one JSON shot list.
	scriptModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"Coffee","logline":"a cup","scenes":[{"heading":"INT. CAFE","description":"steam","dialogue":"hi"}]}`,
	}))
	storyboardModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"shots":[{"shotNo":1,"camera":"wide","scene":"cafe","action":"open","prompt":"cafe","duration":3}]}`,
	}))
	// M2: runStoryboard fans out one asset todo per shot, so the worker needs
	// the asset-pipeline deps (fake generator + in-memory blob + stores) or it
	// nil-panics when it claims the fanned-out asset todo.
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("FAKEPNG"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1,
	})
	w := New(Config{
		Pool:       pool,
		Todos:      todoStore,
		Projects:   project.New(pool),
		Events:     events.New(assetTestGorm(t)),
		Script:     studioagents.NewScriptAgent(scriptModel),
		Storyboard: studioagents.NewStoryboardAgent(storyboardModel),
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fakeGen),
		Storage:    testStorage(),
		Assets:     assets.New(assetTestGorm(t)),
		Cost:       cost.New(assetTestGorm(t)),
		WorkerID:   "test-0",
	})
	// Drain the queue deterministically (no sleeps).
	for i := 0; i < 10; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}
	// Assert artifacts.
	var nScripts, nShots int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM scripts WHERE project_id=$1`, projID).Scan(&nScripts)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM shots WHERE project_id=$1`, projID).Scan(&nShots)
	if nScripts != 1 {
		t.Fatalf("want 1 script, got %d", nScripts)
	}
	if nShots != 1 {
		t.Fatalf("want 1 shot, got %d", nShots)
	}
	// Both todos done.
	for _, id := range []string{ids["s"], ids["b"]} {
		var status string
		_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, id).Scan(&status)
		if status != "done" {
			t.Fatalf("todo %s status=%q want done", id, status)
		}
	}
	// run_events include todo_finished for script + storyboard + the fanned-out
	// asset todo (M2: runStoryboard creates one asset todo per shot).
	var nFinished int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE project_id=$1 AND kind='todo_finished'`, projID).Scan(&nFinished)
	if nFinished != 3 {
		t.Fatalf("want 3 todo_finished events (script+storyboard+asset), got %d", nFinished)
	}
	// The fanned-out asset reached pending_acceptance via the fake generator.
	var nPending int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE project_id=$1 AND status='pending_acceptance'`, projID).Scan(&nPending)
	if nPending != 1 {
		t.Fatalf("want 1 pending_acceptance asset, got %d", nPending)
	}
}

func TestWorkerFailsTodoOnAgentError(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker tests")
	}
	ctx := context.Background()
	st, _ := storage.Open(ctx, storage.Config{PGURL: dsn})
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := st.Pool()
	projID := "wf_" + randHex3()
	_, _ = pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID)
	todoStore := todos.New(pool)
	projStore := project.New(pool)
	// Script succeeds, storyboard (the LAST todo) fails terminally: this exercises
	// both the cancel-dependents path AND the run_done-on-terminal-failure path
	// (done>0 because the script finished, so allDone is satisfied once the
	// storyboard exhausts attempts).
	// RefreshStatus scopes to latest plan — 模拟真实 planner 在 create graph 之前
	// 必先 INSERT plans 行（planner.go:106），所以测试也要先建 plan。plan id 用
	// projID 派生避免共享测试池里硬编码 "pl1" 撞唯一约束。
	planID := "pl_" + projID[len("wf_"):]
	if _, err := pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, valid, fallback_used) VALUES ($1, $2, 'created', false, false)`, planID, projID); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	ids, _ := todoStore.CreateGraph(ctx, projID, planID, []todos.NodeSpec{
		{LocalID: "s", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"x"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"s"}, InputJSON: []byte(`{}`)},
	})
	// Script model returns valid JSON; storyboard model returns garbage every
	// attempt → agent error → terminal failure after attempts exhausted.
	scriptModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"Coffee","logline":"a cup","scenes":[{"heading":"INT. CAFE","description":"steam","dialogue":"hi"}]}`,
	}))
	bad := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: "no json"}, llm.Response{Text: "no json"},
		llm.Response{Text: "no json"}, llm.Response{Text: "no json"},
	))
	w := New(Config{
		Pool: pool, Todos: todoStore, Projects: projStore, Events: events.New(assetTestGorm(t)),
		Script: studioagents.NewScriptAgent(scriptModel), Storyboard: studioagents.NewStoryboardAgent(bad),
		WorkerID: "test-1", MaxAttempts: 2, BaseBackoff: 0,
	})
	for i := 0; i < 20; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}
	// Script done, storyboard failed terminally.
	var scriptStatus, status string
	_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, ids["s"]).Scan(&scriptStatus)
	if scriptStatus != "done" {
		t.Fatalf("script todo status=%q want done", scriptStatus)
	}
	_ = pool.QueryRow(ctx, `SELECT status FROM todos WHERE id=$1`, ids["b"]).Scan(&status)
	if status != "failed" {
		t.Fatalf("storyboard todo status=%q want failed", status)
	}
	// The terminal failure is the last todo to reach a terminal state, so the
	// worker must emit run_done (else the SSE timeline hangs).
	var nRunDone int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM run_events WHERE project_id=$1 AND kind='run_done'`, projID).Scan(&nRunDone)
	if nRunDone != 1 {
		t.Fatalf("want 1 run_done event after terminal failure, got %d", nRunDone)
	}
	// todo_failed payload must carry both "type" and "error" (mirrors
	// todo_ready/started/finished — frontend uses payload.type for stage
	// coloring).
	var failPayload []byte
	_ = pool.QueryRow(ctx,
		`SELECT payload FROM run_events WHERE project_id=$1 AND kind='todo_failed' LIMIT 1`,
		projID).Scan(&failPayload)
	if len(failPayload) == 0 {
		t.Fatal("no todo_failed event found")
	}
	var m map[string]any
	if err := json.Unmarshal(failPayload, &m); err != nil {
		t.Fatalf("unmarshal todo_failed payload: %v", err)
	}
	if _, ok := m["type"]; !ok {
		t.Fatalf("todo_failed payload missing 'type' field: %s", failPayload)
	}
	if m["type"] != "storyboard" {
		t.Fatalf("todo_failed payload type=%q want storyboard", m["type"])
	}
	if _, ok := m["error"]; !ok {
		t.Fatalf("todo_failed payload missing 'error' field: %s", failPayload)
	}
	// With a terminal failure present, the project status resolves to 'failed'
	// rather than wedging in 'running'.
	projStatus, err := projStore.RefreshStatus(ctx, projID)
	if err != nil {
		t.Fatalf("refresh status: %v", err)
	}
	if projStatus != "failed" {
		t.Fatalf("project status=%q want failed", projStatus)
	}
}

func TestWorkerCustomExecutor(t *testing.T) {
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
	projID := "wce_" + randHex3()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// 1. Register the custom type to planner
	planner.RegisterType("translate")

	todoStore := todos.New(pool)
	ids, err := todoStore.CreateGraph(ctx, projID, "pl_wce", []todos.NodeSpec{
		{LocalID: "t", Type: "translate", DependsOn: nil, InputJSON: []byte(`{"text":"hello"}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}

	// 2. Setup custom executor
	executed := false
	var receivedInput []byte
	customExecutors := map[string]TaskExecutor{
		"translate": func(ctx context.Context, todo ClaimedTodo) (string, error) {
			executed = true
			receivedInput = todo.Input
			return "translated:content_id_123", nil
		},
	}

	w := New(Config{
		Pool:            pool,
		Todos:           todoStore,
		Projects:        project.New(pool),
		Events:          events.New(assetTestGorm(t)),
		Storage:         testStorage(),
		CustomExecutors: customExecutors,
		WorkerID:        "test-wce",
	})

	ran, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !ran {
		t.Fatalf("worker did not run any task")
	}

	if !executed {
		t.Fatalf("custom executor was not executed")
	}
	var parsed map[string]string
	if err := json.Unmarshal(receivedInput, &parsed); err != nil {
		t.Fatalf("failed to unmarshal input: %v", err)
	}
	if parsed["text"] != "hello" {
		t.Fatalf("expected input text to be 'hello', got %q", parsed["text"])
	}

	// 3. Assert task status and outputRef in database
	var status string
	var outputRef string
	err = pool.QueryRow(ctx, `SELECT status, output_ref FROM todos WHERE id=$1`, ids["t"]).Scan(&status, &outputRef)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "done" {
		t.Fatalf("expected status 'done', got %q", status)
	}
	if outputRef != "translated:content_id_123" {
		t.Fatalf("expected output_ref 'translated:content_id_123', got %q", outputRef)
	}
}

// TestRunStoryboard_PictureBookFansOutImageAndAudio: a kind='picturebook' project
// fans out one image asset todo per page AND one audio asset todo per page that
// HAS narration (Action 非空). The封面 page (Action="") is image-only. The audio
// todo's input must carry kind='audio', shotPrompt=该页旁白, voice=项目 voice.
func TestRunStoryboard_PictureBookFansOutImageAndAudio(t *testing.T) {
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
	projID := "pb_" + randHex3()
	// kind='picturebook' + a config carrying a voice. ParsePictureBookConfig fills
	// age-band defaults (ageBand 3-6 → MaxWordsPerSpread 50); the voice feeds the
	// audio fan-out.
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, kind, picturebook_config)
		 VALUES ($1,'o','n','u','picturebook',$2)`,
		projID, `{"ageBand":"3-6","illustrationStyle":"watercolor","voice":"warm","themes":["友谊"]}`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	todoStore := todos.New(pool)
	ids, err := todoStore.CreateGraph(ctx, projID, "pl_pb_"+projID[3:], []todos.NodeSpec{
		{LocalID: "s", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"小白兔的故事"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"s"}, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	// Script returns a绘本 ScriptOutput with a characterSheet so the回灌 path has
	// something to carry into the storyboard.
	scriptModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"小白兔","logline":"勇敢的小白兔","scenes":[{"heading":"森林","description":"清晨","dialogue":""}],"characterSheet":"小白兔,蓝背带裤,长耳"}`,
	}))
	// Storyboard: 1 cover (Action="") + 2 content pages (Action set) → 3 images, 2 audio.
	storyboard := newPictureBookStoryboardAgent(t, 2)
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("FAKE"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1,
	})
	w := New(Config{
		Pool:       pool,
		Todos:      todoStore,
		Projects:   project.New(pool),
		Events:     events.New(assetTestGorm(t)),
		Script:     studioagents.NewScriptAgent(scriptModel),
		Storyboard: storyboard,
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fakeGen),
		Storage:    testStorage(),
		Assets:     assets.New(assetTestGorm(t)),
		Cost:       cost.New(assetTestGorm(t)),
		WorkerID:   "test-pb",
	})
	for i := 0; i < 20; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}
	// 3 shots inserted.
	var nShots int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM shots WHERE project_id=$1`, projID).Scan(&nShots)
	if nShots != 3 {
		t.Fatalf("want 3 shots, got %d", nShots)
	}
	// Fan-out: 3 image asset todos + 2 audio asset todos, all depending on the
	// storyboard todo. Count by the kind embedded in each asset todo's input_json.
	var nImage, nAudio int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE input_json->>'kind'='image'),
		        count(*) FILTER (WHERE input_json->>'kind'='audio')
		 FROM todos WHERE project_id=$1 AND type='asset' AND $2 = ANY(depends_on)`,
		projID, ids["b"]).Scan(&nImage, &nAudio); err != nil {
		t.Fatalf("count asset todos: %v", err)
	}
	if nImage != 3 {
		t.Fatalf("want 3 image asset todos, got %d", nImage)
	}
	if nAudio != 2 {
		t.Fatalf("want 2 audio asset todos, got %d", nAudio)
	}
	// Each audio todo's input: kind=audio, shotPrompt=该页旁白, voice=项目 voice.
	rows, err := pool.Query(ctx,
		`SELECT input_json FROM todos
		 WHERE project_id=$1 AND type='asset' AND input_json->>'kind'='audio' AND $2 = ANY(depends_on)
		 ORDER BY input_json->>'shotPrompt'`, projID, ids["b"])
	if err != nil {
		t.Fatalf("query audio todos: %v", err)
	}
	defer rows.Close()
	var gotPrompts []string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan audio input: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal audio input: %v", err)
		}
		if m["kind"] != "audio" {
			t.Fatalf("audio todo kind=%v want audio", m["kind"])
		}
		if m["voice"] != "warm" {
			t.Fatalf("audio todo voice=%v want warm", m["voice"])
		}
		if sp, _ := m["shotPrompt"].(string); sp == "" {
			t.Fatalf("audio todo shotPrompt empty: %s", raw)
		} else {
			gotPrompts = append(gotPrompts, sp)
		}
	}
	// shotPrompt must be the page's narration (Action), not the illustration prompt.
	want := []string{"第1页旁白", "第2页旁白"}
	if len(gotPrompts) != 2 || gotPrompts[0] != want[0] || gotPrompts[1] != want[1] {
		t.Fatalf("audio shotPrompts=%v want %v", gotPrompts, want)
	}

	// Idempotency: re-running the (already-done) storyboard todo must not fan out a
	// second batch. Reset it to ready and run once more; counts must be unchanged.
	if _, err := pool.Exec(ctx,
		`UPDATE todos SET status='ready', output_ref='', locked_by='', locked_until=NULL, next_run_at=now() WHERE id=$1`,
		ids["b"]); err != nil {
		t.Fatalf("reset storyboard todo: %v", err)
	}
	for i := 0; i < 20; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once (rerun): %v", err)
		}
		if !ran {
			break
		}
	}
	var nAssetAfter, nShotsAfter int
	_ = pool.QueryRow(ctx,
		`SELECT count(*) FROM todos WHERE project_id=$1 AND type='asset' AND $2 = ANY(depends_on)`,
		projID, ids["b"]).Scan(&nAssetAfter)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM shots WHERE project_id=$1`, projID).Scan(&nShotsAfter)
	if nAssetAfter != 5 {
		t.Fatalf("idempotency: want 5 asset todos after re-run, got %d", nAssetAfter)
	}
	if nShotsAfter != 3 {
		t.Fatalf("idempotency: want 3 shots after re-run, got %d", nShotsAfter)
	}
}

// narrationSafetyStub is a NarrationSafety-backing model that judges a narration
// unsafe iff its text contains a marker substring; everything else is safe. Lets
// the test deterministically block exactly one page regardless of fan-out order.
type narrationSafetyStub struct {
	llm.ScriptedLLM
	unsafeMarker string
}

func (m *narrationSafetyStub) Generate(_ context.Context, req llm.Request) (llm.Response, error) {
	var user string
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			user = msg.Content
		}
	}
	if strings.Contains(user, m.unsafeMarker) {
		return llm.Response{Text: `{"safe":false,"reason":"暴力"}`}, nil
	}
	return llm.Response{Text: `{"safe":true}`}, nil
}

// TestRunStoryboard_UnsafeNarrationSkipsAudio: when the旁白 safety check judges
// one content page unsafe, that page must lose its audio todo (image still出),
// while the other content page keeps both image + audio.
func TestRunStoryboard_UnsafeNarrationSkipsAudio(t *testing.T) {
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
	projID := "pbu_" + randHex3()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, kind, picturebook_config)
		 VALUES ($1,'o','n','u','picturebook',$2)`,
		projID, `{"ageBand":"3-6","illustrationStyle":"watercolor","voice":"warm","themes":["友谊"]}`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	todoStore := todos.New(pool)
	ids, err := todoStore.CreateGraph(ctx, projID, "pl_pbu_"+projID[4:], []todos.NodeSpec{
		{LocalID: "s", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"小白兔的故事"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"s"}, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	scriptModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"小白兔","logline":"勇敢的小白兔","scenes":[{"heading":"森林","description":"清晨","dialogue":""}],"characterSheet":"小白兔,蓝背带裤,长耳"}`,
	}))
	// 1 cover + 2 content pages ("第1页旁白"/"第2页旁白"). Mark page 1 unsafe.
	storyboard := newPictureBookStoryboardAgent(t, 2)
	narration := studioagents.NewNarrationSafety(&narrationSafetyStub{unsafeMarker: "第1页旁白"})
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("FAKE"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1,
	})
	w := New(Config{
		Pool:       pool,
		Todos:      todoStore,
		Projects:   project.New(pool),
		Events:     events.New(assetTestGorm(t)),
		Script:     studioagents.NewScriptAgent(scriptModel),
		Storyboard: storyboard,
		Narration:  narration,
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fakeGen),
		Storage:    testStorage(),
		Assets:     assets.New(assetTestGorm(t)),
		Cost:       cost.New(assetTestGorm(t)),
		WorkerID:   "test-pbu",
	})
	for i := 0; i < 20; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}
	// 3 images (cover + 2 content), 1 audio (only the safe content page).
	var nImage, nAudio int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE input_json->>'kind'='image'),
		        count(*) FILTER (WHERE input_json->>'kind'='audio')
		 FROM todos WHERE project_id=$1 AND type='asset' AND $2 = ANY(depends_on)`,
		projID, ids["b"]).Scan(&nImage, &nAudio); err != nil {
		t.Fatalf("count asset todos: %v", err)
	}
	if nImage != 3 {
		t.Fatalf("want 3 image asset todos (both pages keep image), got %d", nImage)
	}
	if nAudio != 1 {
		t.Fatalf("want 1 audio asset todo (unsafe page skipped), got %d", nAudio)
	}
	// The single audio belongs to the SAFE page (第2页旁白), not the blocked one.
	var audioPrompt string
	if err := pool.QueryRow(ctx,
		`SELECT input_json->>'shotPrompt' FROM todos
		 WHERE project_id=$1 AND type='asset' AND input_json->>'kind'='audio' AND $2 = ANY(depends_on)`,
		projID, ids["b"]).Scan(&audioPrompt); err != nil {
		t.Fatalf("query audio prompt: %v", err)
	}
	if audioPrompt != "第2页旁白" {
		t.Fatalf("audio kept for wrong page: %q (want 第2页旁白)", audioPrompt)
	}
}

// inconclusiveSafetyStub always returns safe=false WITHOUT a reason — the弱模型/
// 解析不稳的常见表现。worker 应 fail-open（放行 audio），只在「明确 unsafe 且带理由」时拦。
type inconclusiveSafetyStub struct{ llm.ScriptedLLM }

func (m *inconclusiveSafetyStub) Generate(_ context.Context, _ llm.Request) (llm.Response, error) {
	return llm.Response{Text: `{"safe":false}`}, nil
}

// TestRunStoryboard_InconclusiveNarrationAllowsAudio: safe=false 但无理由 → 不拦，
// 两个内容页都保留 audio（fail-open，避免弱模型把整本绘本判没声）。
func TestRunStoryboard_InconclusiveNarrationAllowsAudio(t *testing.T) {
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
	projID := "pbi_" + randHex3()
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by, kind, picturebook_config)
		 VALUES ($1,'o','n','u','picturebook',$2)`,
		projID, `{"ageBand":"3-6","illustrationStyle":"watercolor","voice":"warm"}`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	todoStore := todos.New(pool)
	ids, err := todoStore.CreateGraph(ctx, projID, "pl_pbi_"+projID[4:], []todos.NodeSpec{
		{LocalID: "s", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"小白兔的故事"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"s"}, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	scriptModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"小白兔","logline":"勇敢","scenes":[{"heading":"森林","description":"清晨","dialogue":""}],"characterSheet":"小白兔,长耳"}`,
	}))
	w := New(Config{
		Pool: pool, Todos: todoStore, Projects: project.New(pool), Events: events.New(assetTestGorm(t)),
		Script:     studioagents.NewScriptAgent(scriptModel),
		Storyboard: newPictureBookStoryboardAgent(t, 2),
		Narration:  studioagents.NewNarrationSafety(&inconclusiveSafetyStub{}),
		Asset: studioagents.NewAssetAgent(prompt.NewBuilder(), generate.NewFakeLooping(generate.GenResult{
			Bytes: []byte("FAKE"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1,
		})),
		Storage: testStorage(), Assets: assets.New(assetTestGorm(t)), Cost: cost.New(assetTestGorm(t)), WorkerID: "test-pbi",
	})
	for i := 0; i < 20; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}
	var nAudio int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE input_json->>'kind'='audio')
		 FROM todos WHERE project_id=$1 AND type='asset' AND $2 = ANY(depends_on)`,
		projID, ids["b"]).Scan(&nAudio); err != nil {
		t.Fatalf("count audio todos: %v", err)
	}
	if nAudio != 2 {
		t.Fatalf("fail-open: want 2 audio todos (both content pages), got %d", nAudio)
	}
}

// TestRunStoryboard_StandardOnlyImage: a standard (non-绘本) project fans out only
// image asset todos — no audio — proving the绘本 branch is fully gated on kind.
func TestRunStoryboard_StandardOnlyImage(t *testing.T) {
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
	projID := "std_" + randHex3()
	// Default kind='standard'; no picturebook_config.
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	todoStore := todos.New(pool)
	ids, err := todoStore.CreateGraph(ctx, projID, "pl_std_"+projID[4:], []todos.NodeSpec{
		{LocalID: "s", Type: "script", DependsOn: nil, InputJSON: []byte(`{"brief":"coffee ad"}`)},
		{LocalID: "b", Type: "storyboard", DependsOn: []string{"s"}, InputJSON: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("create graph: %v", err)
	}
	scriptModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"Coffee","logline":"a cup","scenes":[{"heading":"INT. CAFE","description":"steam","dialogue":"hi"}]}`,
	}))
	// 2 shots, both with Action set — but standard must STILL fan out image only.
	storyboard := newStoryboardAgentWithShots(t, 2)
	fakeGen := generate.NewFakeLooping(generate.GenResult{
		Bytes: []byte("FAKE"), MimeType: "image/png", Provider: "fake", Model: "fake-img", ImageCount: 1,
	})
	w := New(Config{
		Pool:       pool,
		Todos:      todoStore,
		Projects:   project.New(pool),
		Events:     events.New(assetTestGorm(t)),
		Script:     studioagents.NewScriptAgent(scriptModel),
		Storyboard: storyboard,
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fakeGen),
		Storage:    testStorage(),
		Assets:     assets.New(assetTestGorm(t)),
		Cost:       cost.New(assetTestGorm(t)),
		WorkerID:   "test-std",
	})
	for i := 0; i < 20; i++ {
		ran, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("run once: %v", err)
		}
		if !ran {
			break
		}
	}
	var nImage, nAudio int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE input_json->>'kind'='image'),
		        count(*) FILTER (WHERE input_json->>'kind'='audio')
		 FROM todos WHERE project_id=$1 AND type='asset' AND $2 = ANY(depends_on)`,
		projID, ids["b"]).Scan(&nImage, &nAudio); err != nil {
		t.Fatalf("count asset todos: %v", err)
	}
	if nImage != 2 {
		t.Fatalf("standard: want 2 image asset todos, got %d", nImage)
	}
	if nAudio != 0 {
		t.Fatalf("standard: want 0 audio asset todos, got %d", nAudio)
	}
}
