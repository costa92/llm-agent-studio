package worker

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"
	"gorm.io/gorm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// itemsTestWorker builds a Worker for the built-in script/storyboard agent paths
// (P2a node_outputs.items dual-write). It binds the ScriptAgent/StoryboardAgent
// over a scripted model and sets NO Router, so routedChatModel() returns false
// and the worker calls cfg.Script.Run / cfg.Storyboard.Run (the bound model).
func itemsTestWorker(t *testing.T, db *gorm.DB, model llm.ChatModel) *Worker {
	t.Helper()
	return New(Config{
		DB:         db,
		Todos:      todos.New(db),
		Projects:   project.New(db),
		Events:     events.New(db),
		Script:     studioagents.NewScriptAgent(model),
		Storyboard: studioagents.NewStoryboardAgent(model),
		WorkerID:   "items-test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
}

// seedItemsProject inserts a bare project and returns its id.
func seedItemsProject(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var projID string
	if err := db.WithContext(context.Background()).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_items','p','u') RETURNING id`,
	).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projID
}

// countNodeOutputItems returns the number of node_outputs rows for todoID plus
// the items[] of the first such row (decoded).
func loadNodeOutputItems(t *testing.T, db *gorm.DB, todoID string) (int, []Item) {
	t.Helper()
	ctx := context.Background()
	var count int
	if err := db.WithContext(ctx).Raw(
		`SELECT count(*) FROM node_outputs WHERE todo_id=$1`, todoID).Row().Scan(&count); err != nil {
		t.Fatalf("count node_outputs: %v", err)
	}
	if count == 0 {
		return 0, nil
	}
	var raw []byte
	if err := db.WithContext(ctx).Raw(
		`SELECT items FROM node_outputs WHERE todo_id=$1 ORDER BY created_at LIMIT 1`, todoID).Row().Scan(&raw); err != nil {
		t.Fatalf("load items: %v", err)
	}
	var items []Item
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("unmarshal items: %q (%v)", raw, err)
	}
	return count, items
}

func TestRunScriptEmitsTypedItems(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker items tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	projID := seedItemsProject(t, db)

	// The scripted model must return exactly the JSON the ScriptAgent parser
	// accepts: {"title","logline","characterSheet","scenes":[...]} with a
	// non-empty title + at least one scene.
	const modelJSON = `{"title":"T","logline":"L","characterSheet":"a teal fox in a red scarf","scenes":[{"heading":"H1","description":"D1","dialogue":"hi"}]}`
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: modelJSON}))
	w := itemsTestWorker(t, db, model)

	todoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, input_json)
		 VALUES ($1,$2,'plan-x','script','running','{"brief":"b"}')`,
		todoID, projID).Error; err != nil {
		t.Fatalf("seed script todo: %v", err)
	}
	c := claimed{todoID: todoID, projectID: projID, typ: "script", attempts: 1, input: []byte(`{"brief":"b"}`)}

	ref, err := w.runScript(ctx, c)
	if err != nil {
		t.Fatalf("runScript: %v", err)
	}

	// (a) legacy scripts row still written.
	var scriptCount int
	if err := db.WithContext(ctx).Raw(
		`SELECT count(*) FROM scripts WHERE todo_id=$1`, todoID).Row().Scan(&scriptCount); err != nil {
		t.Fatalf("count scripts: %v", err)
	}
	if scriptCount != 1 {
		t.Fatalf("want 1 legacy scripts row, got %d", scriptCount)
	}
	if got := ref; got == "" || got[:7] != "script:" {
		t.Fatalf("want output_ref script:<id>, got %q", got)
	}

	// (b) exactly one node_outputs items row; items[0].json → ScriptOutput.
	count, items := loadNodeOutputItems(t, db, todoID)
	if count != 1 {
		t.Fatalf("want 1 node_outputs row, got %d", count)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	var out studioagents.ScriptOutput
	if err := json.Unmarshal(items[0].JSON, &out); err != nil {
		t.Fatalf("item[0].json is not a ScriptOutput: %q (%v)", items[0].JSON, err)
	}
	if out.CharacterSheet != "a teal fox in a red scarf" {
		t.Fatalf("want characterSheet round-tripped, got %q", out.CharacterSheet)
	}
}

func TestRunStoryboardEmitsItemPerShot(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker items tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	projID := seedItemsProject(t, db)

	// Seed a done script parent todo + its scripts row. runStoryboard resolves the
	// upstream script via the storyboard todo's depends_on parent (output_ref
	// 'script:<id>'), then loads scripts.content_json.
	scriptID := newID()
	scriptTodoID := newID()
	const scriptContent = `{"title":"T","logline":"L","scenes":[{"heading":"H1","description":"D1","dialogue":"hi"}]}`
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,$3,$4,1)`,
		scriptID, projID, scriptTodoID, scriptContent).Error; err != nil {
		t.Fatalf("seed scripts row: %v", err)
	}
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','script','done',$3,'{}')`,
		scriptTodoID, projID, "script:"+scriptID).Error; err != nil {
		t.Fatalf("seed script todo: %v", err)
	}
	// Storyboard todo depending on the script todo.
	sbTodoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
		 VALUES ($1,$2,'plan-x','storyboard','running',ARRAY[$3]::text[],'{}')`,
		sbTodoID, projID, scriptTodoID).Error; err != nil {
		t.Fatalf("seed storyboard todo: %v", err)
	}

	// StoryboardAgent parser accepts {"shots":[{...}]}; emit 2 shots.
	const modelJSON = `{"shots":[` +
		`{"shotNo":1,"camera":"wide","scene":"s1","action":"a1","prompt":"p1","duration":3},` +
		`{"shotNo":2,"camera":"close","scene":"s2","action":"a2","prompt":"p2","duration":4}]}`
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: modelJSON}))
	w := itemsTestWorker(t, db, model)

	c := claimed{todoID: sbTodoID, projectID: projID, typ: "storyboard", attempts: 1, input: []byte(`{}`)}
	if _, err := w.runStoryboard(ctx, c); err != nil {
		t.Fatalf("runStoryboard: %v", err)
	}

	// (a) 2 legacy shots rows.
	var shotCount int
	if err := db.WithContext(ctx).Raw(
		`SELECT count(*) FROM shots WHERE todo_id=$1`, sbTodoID).Row().Scan(&shotCount); err != nil {
		t.Fatalf("count shots: %v", err)
	}
	if shotCount != 2 {
		t.Fatalf("want 2 legacy shots rows, got %d", shotCount)
	}

	// (b) one node_outputs row carrying 2 items; item[0].json → Shot ShotNo 1.
	count, items := loadNodeOutputItems(t, db, sbTodoID)
	if count != 1 {
		t.Fatalf("want 1 node_outputs row, got %d", count)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	var sh studioagents.Shot
	if err := json.Unmarshal(items[0].JSON, &sh); err != nil {
		t.Fatalf("item[0].json is not a Shot: %q (%v)", items[0].JSON, err)
	}
	if sh.ShotNo != 1 {
		t.Fatalf("want item[0] ShotNo 1, got %d", sh.ShotNo)
	}
}

// TestLoadInputsReadsItems verifies loadInputs reads a dependency's newest
// node_outputs.items as the canonical inter-node channel (the happy path: the
// dep ran under P2a code and emitted items).
func TestLoadInputsReadsItems(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker items tests")
	}
	ctx := context.Background()
	w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{}`})))
	db := w.cfg.DB
	projID := seedItemsProject(t, db)

	// Dep todo that emitted a node_outputs.items row under P2a code.
	depTodo := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:llm','done',$3,'{}')`,
		depTodo, projID, "custom:o1").Error; err != nil {
		t.Fatalf("seed dep todo: %v", err)
	}
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,'custom:llm','upstream','text',$4)`,
		newID(), projID, depTodo, []byte(`[{"json":{"text":"upstream"}}]`)).Error; err != nil {
		t.Fatalf("seed dep node_output: %v", err)
	}

	// Consumer todo depending on depTodo.
	consumer := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
		 VALUES ($1,$2,'plan-x','custom:next','running',ARRAY[$3]::text[],'{}')`,
		consumer, projID, depTodo).Error; err != nil {
		t.Fatalf("seed consumer todo: %v", err)
	}

	items, err := w.loadInputs(ctx, consumer)
	if err != nil {
		t.Fatalf("loadInputs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	var wrap struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(items[0].JSON, &wrap); err != nil {
		t.Fatalf("item[0].json not a {text} wrapper: %q (%v)", items[0].JSON, err)
	}
	if wrap.Text != "upstream" {
		t.Fatalf("want items[0].json.text=upstream, got %q", wrap.Text)
	}
}

// TestLoadInputsFallsBackToScriptProjection covers the straddling-deploy case
// (★M-4): a dep todo that completed under old code has NO node_outputs.items, so
// loadInputs falls back to projecting its scripts row (output_ref 'script:<id>')
// into an equivalent item — in-flight runs are not stranded.
func TestLoadInputsFallsBackToScriptProjection(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker items tests")
	}
	ctx := context.Background()
	w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: `{}`})))
	db := w.cfg.DB
	projID := seedItemsProject(t, db)

	// scripts row with a characterSheet; NO node_outputs row for this dep.
	scriptID := newID()
	const scriptContent = `{"title":"T","logline":"L","characterSheet":"a teal fox in a red scarf","scenes":[{"heading":"H1","description":"D1","dialogue":"hi"}]}`
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,$3,$4,1)`,
		scriptID, projID, newID(), scriptContent).Error; err != nil {
		t.Fatalf("seed scripts row: %v", err)
	}
	depTodo := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','script','done',$3,'{}')`,
		depTodo, projID, "script:"+scriptID).Error; err != nil {
		t.Fatalf("seed dep script todo: %v", err)
	}

	consumer := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
		 VALUES ($1,$2,'plan-x','storyboard','running',ARRAY[$3]::text[],'{}')`,
		consumer, projID, depTodo).Error; err != nil {
		t.Fatalf("seed consumer todo: %v", err)
	}

	items, err := w.loadInputs(ctx, consumer)
	if err != nil {
		t.Fatalf("loadInputs: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 fallback item, got %d", len(items))
	}
	var out studioagents.ScriptOutput
	if err := json.Unmarshal(items[0].JSON, &out); err != nil {
		t.Fatalf("item[0].json is not a ScriptOutput: %q (%v)", items[0].JSON, err)
	}
	if out.CharacterSheet != "a teal fox in a red scarf" {
		t.Fatalf("want characterSheet round-tripped via fallback, got %q", out.CharacterSheet)
	}
}

// TestRunPrescreenDualWritesItems verifies runPrescreen writes the typed
// ReviewOutput verdict to node_outputs.items in the SAME INSERT that lands the
// legacy content/format='json' row (★B2/D-6 dual-write).
func TestRunPrescreenDualWritesItems(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker items tests")
	}
	ctx := context.Background()
	reviewModel := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: `{"score":72,"flags":["x"],"note":"ok"}`},
	))
	w := newPrescreenWorker(t, studioagents.NewReviewAgent(reviewModel))
	db := w.cfg.DB

	projID := seedItemsProject(t, db)

	// Upstream text custom node: a node_outputs(custom:llm) text row + a 'custom'
	// todo (status=done) whose output_ref points at it via "custom:<id>". The
	// prescreen todo depends_on that upstream todo.
	upstreamOutID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
		 VALUES ($1,$2,$3,'custom:llm','some draft text','text')`,
		upstreamOutID, projID, newID()).Error; err != nil {
		t.Fatalf("seed upstream node_output: %v", err)
	}
	upstreamTodo := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id,project_id,plan_id,type,status,output_ref,input_json)
		 VALUES ($1,$2,'plan','custom:llm','done',$3,'{}')`,
		upstreamTodo, projID, "custom:"+upstreamOutID).Error; err != nil {
		t.Fatalf("seed upstream todo: %v", err)
	}
	prescreenTodo := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id,project_id,plan_id,type,status,depends_on,input_json)
		 VALUES ($1,$2,'plan','prescreen','running',ARRAY[$3]::text[],'{}')`,
		prescreenTodo, projID, upstreamTodo).Error; err != nil {
		t.Fatalf("seed prescreen todo: %v", err)
	}

	if _, err := w.runPrescreen(ctx, claimed{
		todoID: prescreenTodo, projectID: projID, typ: "prescreen", attempts: 1, input: []byte("{}"),
	}); err != nil {
		t.Fatalf("runPrescreen: %v", err)
	}

	// (a) legacy format='json' row still present (content/format unchanged).
	var legacy int
	if err := db.WithContext(ctx).Raw(
		`SELECT count(*) FROM node_outputs WHERE todo_id=$1 AND format='json'`, prescreenTodo).Row().Scan(&legacy); err != nil {
		t.Fatalf("count legacy json row: %v", err)
	}
	if legacy != 1 {
		t.Fatalf("want 1 legacy format='json' row, got %d", legacy)
	}

	// (b) one items row; items[0].json → ReviewOutput with Score==72.
	count, items := loadNodeOutputItems(t, db, prescreenTodo)
	if count != 1 {
		t.Fatalf("want 1 node_outputs row, got %d", count)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	var verdict studioagents.ReviewOutput
	if err := json.Unmarshal(items[0].JSON, &verdict); err != nil {
		t.Fatalf("item[0].json is not a ReviewOutput: %q (%v)", items[0].JSON, err)
	}
	if verdict.Score != 72 {
		t.Fatalf("want items[0].json ReviewOutput Score 72, got %d", verdict.Score)
	}
}

// TestRunCustomLLMDualWritesItems verifies runCustomLLM dual-writes typed items:
// a text response wraps as [{json:{text:<answer>}}] and a json response stores
// the parsed object as [{json:<object>}] — while legacy content/format stay
// unchanged (★B2/D-6).
func TestRunCustomLLMDualWritesItems(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker items tests")
	}
	ctx := context.Background()

	const textAnswer = "Here is the answer."
	const jsonAnswer = `{"verdict":"pass","score":91}`

	// Reuse the typed-custom todo seed pattern from TestRunCustomLLM_TextAndJSON:
	// a done script todo + scripts row provide the {{draft}} variable source.
	buildInput := func(outputFormat, scriptTodoID string) []byte {
		in := map[string]any{
			"kind": "llm",
			"params": map[string]any{
				"systemPrompt": "You are a translator.",
				"userPrompt":   "Translate: {{draft}}",
				"outputFormat": outputFormat,
				"variables": []map[string]any{
					{"name": "draft", "sourceTodoId": scriptTodoID},
				},
			},
		}
		b, _ := json.Marshal(in)
		return b
	}

	seedScript := func(t *testing.T, db *gorm.DB, projID string) string {
		t.Helper()
		scriptID := newID()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO scripts (id, project_id, todo_id, content_json, version) VALUES ($1,$2,'t-dummy-s',$3,1)`,
			scriptID, projID, []byte(`{"title":"Draft Title"}`)).Error; err != nil {
			t.Fatalf("seed script: %v", err)
		}
		scriptTodoID := newID()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
			 VALUES ($1,$2,'plan-x','script','done',$3,'{}')`,
			scriptTodoID, projID, "script:"+scriptID).Error; err != nil {
			t.Fatalf("seed script todo: %v", err)
		}
		return scriptTodoID
	}

	seedProject := func(t *testing.T, db *gorm.DB, orgID string) string {
		t.Helper()
		var projID string
		if err := db.WithContext(ctx).Raw(
			`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
			orgID).Row().Scan(&projID); err != nil {
			t.Fatalf("seed project: %v", err)
		}
		return projID
	}

	t.Run("text response wraps as {text:<answer>}", func(t *testing.T) {
		model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: textAnswer}))
		w := customTestWorker(t, model)
		db := w.cfg.DB
		projID := seedProject(t, db, os.Getenv("_CUSTOM_TEST_ORG_ID"))
		scriptTodoID := seedScript(t, db, projID)

		customTodoID := newID()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,'plan-x','custom:translate','running',ARRAY[$3]::text[],$4)`,
			customTodoID, projID, scriptTodoID, buildInput("text", scriptTodoID)).Error; err != nil {
			t.Fatalf("seed custom todo: %v", err)
		}
		if _, err := w.runCustom(ctx, claimed{
			todoID: customTodoID, projectID: projID, typ: "custom:translate",
			attempts: 1, input: buildInput("text", scriptTodoID),
		}); err != nil {
			t.Fatalf("runCustom text: %v", err)
		}

		// legacy content/format unchanged.
		var format, content string
		if err := db.WithContext(ctx).Raw(
			`SELECT format, content FROM node_outputs WHERE todo_id=$1`, customTodoID).Row().Scan(&format, &content); err != nil {
			t.Fatalf("load node_output: %v", err)
		}
		if format != "text" || content != textAnswer {
			t.Fatalf("legacy row = (%q,%q), want (text,%q)", format, content, textAnswer)
		}

		// items = [{json:{text:<answer>}}].
		count, items := loadNodeOutputItems(t, db, customTodoID)
		if count != 1 || len(items) != 1 {
			t.Fatalf("want 1 row / 1 item, got count=%d items=%d", count, len(items))
		}
		var wrap struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(items[0].JSON, &wrap); err != nil {
			t.Fatalf("item[0].json not a {text} wrapper: %q (%v)", items[0].JSON, err)
		}
		if wrap.Text != textAnswer {
			t.Fatalf("want items[0].json.text=%q, got %q", textAnswer, wrap.Text)
		}
	})

	t.Run("json response stores parsed object", func(t *testing.T) {
		model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: jsonAnswer}))
		w := customTestWorker(t, model)
		db := w.cfg.DB
		projID := seedProject(t, db, os.Getenv("_CUSTOM_TEST_ORG_ID"))
		scriptTodoID := seedScript(t, db, projID)

		customTodoID := newID()
		if err := db.WithContext(ctx).Exec(
			`INSERT INTO todos (id, project_id, plan_id, type, status, depends_on, input_json)
			 VALUES ($1,$2,'plan-x','custom:translate','running',ARRAY[$3]::text[],$4)`,
			customTodoID, projID, scriptTodoID, buildInput("json", scriptTodoID)).Error; err != nil {
			t.Fatalf("seed custom todo: %v", err)
		}
		if _, err := w.runCustom(ctx, claimed{
			todoID: customTodoID, projectID: projID, typ: "custom:translate",
			attempts: 1, input: buildInput("json", scriptTodoID),
		}); err != nil {
			t.Fatalf("runCustom json: %v", err)
		}

		// legacy content/format unchanged (format=json, content is the JSON string).
		var format, content string
		if err := db.WithContext(ctx).Raw(
			`SELECT format, content FROM node_outputs WHERE todo_id=$1`, customTodoID).Row().Scan(&format, &content); err != nil {
			t.Fatalf("load node_output: %v", err)
		}
		if format != "json" || content != jsonAnswer {
			t.Fatalf("legacy row = (%q,%q), want (json,%q)", format, content, jsonAnswer)
		}

		// items = [{json:<parsed object>}]; the object itself (NOT a {text} wrapper).
		count, items := loadNodeOutputItems(t, db, customTodoID)
		if count != 1 || len(items) != 1 {
			t.Fatalf("want 1 row / 1 item, got count=%d items=%d", count, len(items))
		}
		var obj struct {
			Verdict string `json:"verdict"`
			Score   int    `json:"score"`
		}
		if err := json.Unmarshal(items[0].JSON, &obj); err != nil {
			t.Fatalf("item[0].json not the parsed object: %q (%v)", items[0].JSON, err)
		}
		if obj.Verdict != "pass" || obj.Score != 91 {
			t.Fatalf("want items[0].json = {verdict:pass,score:91}, got %+v", obj)
		}
	})
}
