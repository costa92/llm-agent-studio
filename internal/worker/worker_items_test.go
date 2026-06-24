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
