package worker

import (
	"context"
	"encoding/json"
	"os"
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

// stubAlerts records RunFailed calls (run 失败告警触发点断言用)。
type stubAlerts struct {
	calls []stubAlertCall
}

type stubAlertCall struct{ projectID, todoID, nodeType, errMsg string }

func (s *stubAlerts) RunFailed(projectID, todoID, nodeType, errMsg string) {
	s.calls = append(s.calls, stubAlertCall{projectID, todoID, nodeType, errMsg})
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
	todoStore := todos.New(assetTestGorm(t))
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
	alerts := &stubAlerts{}
	w := New(Config{
		DB:       assetTestGorm(t),
		Todos:      todoStore,
		Projects:   project.New(assetTestGorm(t)),
		Events:     events.New(assetTestGorm(t)),
		Script:     studioagents.NewScriptAgent(scriptModel),
		Storyboard: studioagents.NewStoryboardAgent(storyboardModel),
		Asset:      studioagents.NewAssetAgent(prompt.NewBuilder(), fakeGen),
		Storage:    testStorage(),
		Assets:     assets.New(assetTestGorm(t)),
		Cost:       cost.New(assetTestGorm(t)),
		Alerts:     alerts,
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
	// 成功的 run 绝不触发失败告警。
	if len(alerts.calls) != 0 {
		t.Fatalf("successful run must not notify, got %d RunFailed calls: %+v", len(alerts.calls), alerts.calls)
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
	todoStore := todos.New(assetTestGorm(t))
	projStore := project.New(assetTestGorm(t))
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
	// Neutralize leftover claimable todos from earlier tests in this shared
	// package DB (评审修复 M7): the global-claim drain below would otherwise
	// reclaim a foreign stuck asset todo (e.g. a 'running' row whose short lease
	// has since expired) and panic this Assets-store-less worker on the asset
	// path. Push everything outside this test's project out of the claim window;
	// our script/storyboard rows keep next_run_at = now() and are the only ones
	// claimed. Without this, the test passes or panics purely on timing margin.
	_, _ = pool.Exec(ctx, `
		UPDATE todos
		SET next_run_at = now() + interval '1 hour',
		    locked_until = CASE WHEN status='running' THEN now() + interval '1 hour' ELSE locked_until END
		WHERE project_id <> $1 AND status IN ('ready','running')`, projID)
	// Script model returns valid JSON; storyboard model returns garbage every
	// attempt → agent error → terminal failure after attempts exhausted.
	scriptModel := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"title":"Coffee","logline":"a cup","scenes":[{"heading":"INT. CAFE","description":"steam","dialogue":"hi"}]}`,
	}))
	bad := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: "no json"}, llm.Response{Text: "no json"},
		llm.Response{Text: "no json"}, llm.Response{Text: "no json"},
	))
	alerts := &stubAlerts{}
	w := New(Config{
		DB: assetTestGorm(t), Todos: todoStore, Projects: projStore, Events: events.New(assetTestGorm(t)),
		Script: studioagents.NewScriptAgent(scriptModel), Storyboard: studioagents.NewStoryboardAgent(bad),
		Alerts:   alerts,
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
	// 终态失败恰好触发一次 run 失败告警（重试中的失败不触发；一次 run 只发一封的
	// 去重在 alerts.Notifier 内按 planID 收口，这里断言 worker 侧的触发点语义）。
	if len(alerts.calls) != 1 {
		t.Fatalf("want exactly 1 RunFailed call on terminal failure, got %d: %+v", len(alerts.calls), alerts.calls)
	}
	if c := alerts.calls[0]; c.projectID != projID || c.todoID != ids["b"] || c.nodeType != "storyboard" || c.errMsg == "" {
		t.Fatalf("unexpected RunFailed call: %+v", c)
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

	todoStore := todos.New(assetTestGorm(t))
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
		DB:            assetTestGorm(t),
		Todos:           todoStore,
		Projects:        project.New(assetTestGorm(t)),
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

// TestRunStoryboard_StandardOnlyImage: the storyboard fan-out emits only image
// asset todos — no audio — even when every shot has an Action set. (The绘本 audio
// narration fan-out was removed; image-only is now the sole behavior.)
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
	todoStore := todos.New(assetTestGorm(t))
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
		DB:       assetTestGorm(t),
		Todos:      todoStore,
		Projects:   project.New(assetTestGorm(t)),
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
