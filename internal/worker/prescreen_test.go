package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"

	studioagents "github.com/costa92/llm-agent-studio/internal/agents"
	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

// newPrescreenWorker builds a worker wired only with what runPrescreen needs.
// review==nil disables prescreen (scenario 2).
func newPrescreenWorker(t *testing.T, review *studioagents.ReviewAgent) *Worker {
	t.Helper()
	return New(Config{
		DB:       assetTestGorm(t),
		Todos:    todos.New(assetTestGorm(t)),
		Projects: project.New(assetTestGorm(t)),
		Events:   events.New(assetTestGorm(t)),
		Review:   review,
		WorkerID: "prescreen", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
}

func TestRunPrescreenHappyPath(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_ps','p','u') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// Upstream script node: a scripts row + a 'script' todo whose output_ref points at it.
	scriptID := newID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO scripts (id, project_id, todo_id, content_json) VALUES ($1,$2,'t1',$3)`,
		scriptID, pid, `"some text"`); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	upstreamTodo := newID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,output_ref) VALUES ($1,$2,'plan','script','done',$3)`,
		upstreamTodo, pid, "script:"+scriptID); err != nil {
		t.Fatalf("seed upstream todo: %v", err)
	}
	// The prescreen todo depends_on the upstream script todo.
	prescreenTodo := newID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,depends_on,input_json) VALUES ($1,$2,'plan','prescreen','running',$3,'{}')`,
		prescreenTodo, pid, []string{upstreamTodo}); err != nil {
		t.Fatalf("seed prescreen todo: %v", err)
	}

	reviewModel := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: `{"score":77,"flags":["check_copyright"],"note":"ok"}`},
	))
	w := newPrescreenWorker(t, studioagents.NewReviewAgent(reviewModel))

	ref, err := w.runPrescreen(ctx, claimed{todoID: prescreenTodo, projectID: pid, typ: "prescreen", attempts: 1, input: []byte("{}")})
	if err != nil {
		t.Fatalf("runPrescreen: %v", err)
	}
	if !strings.HasPrefix(ref, "custom:") {
		t.Fatalf("ref = %q, want custom:<id> prefix", ref)
	}
	outID := strings.TrimPrefix(ref, "custom:")

	var format, content string
	if err := pool.QueryRow(ctx,
		`SELECT format, content FROM node_outputs WHERE id=$1`, outID).Scan(&format, &content); err != nil {
		t.Fatalf("load node_output: %v", err)
	}
	if format != "json" {
		t.Fatalf("format = %q, want json", format)
	}
	var verdict studioagents.ReviewOutput
	if err := json.Unmarshal([]byte(content), &verdict); err != nil {
		t.Fatalf("unmarshal verdict %q: %v", content, err)
	}
	if verdict.Score != 77 || verdict.Note != "ok" ||
		len(verdict.Flags) != 1 || verdict.Flags[0] != "check_copyright" {
		t.Fatalf("verdict = %+v, want {77 [check_copyright] ok}", verdict)
	}
	// Downstream readability: itemsForDep (the canonical inter-node channel) on
	// the prescreen todo yields one item carrying the verdict JSON.
	items, err := w.itemsForDep(ctx, prescreenTodo, pid)
	if err != nil {
		t.Fatalf("itemsForDep: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("itemsForDep items = %d, want 1", len(items))
	}
	var itemVerdict studioagents.ReviewOutput
	if err := json.Unmarshal(items[0].JSON, &itemVerdict); err != nil {
		t.Fatalf("unmarshal item verdict %q: %v", items[0].JSON, err)
	}
	if itemVerdict.Score != 77 || itemVerdict.Note != "ok" {
		t.Fatalf("item verdict = %+v, want {77 ... ok}", itemVerdict)
	}
}

func TestRunPrescreenDisabledWithoutReviewAgent(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_ps','p','u') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	prescreenTodo := newID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','prescreen','running','{}')`,
		prescreenTodo, pid); err != nil {
		t.Fatalf("seed prescreen todo: %v", err)
	}
	w := newPrescreenWorker(t, nil)
	_, err := w.runPrescreen(ctx, claimed{todoID: prescreenTodo, projectID: pid, typ: "prescreen", attempts: 1, input: []byte("{}")})
	if err == nil || !strings.Contains(err.Error(), "prescreen disabled") {
		t.Fatalf("err = %v, want one containing 'prescreen disabled'", err)
	}
}

func TestRunPrescreenNoUpstream(t *testing.T) {
	pool := assetTestPool(t)
	ctx := context.Background()
	var pid string
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),'org_ps','p','u') RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// A prescreen todo with no depends_on edge → no resolvable upstream text node.
	prescreenTodo := newID()
	if _, err := pool.Exec(ctx,
		`INSERT INTO todos (id,project_id,plan_id,type,status,input_json) VALUES ($1,$2,'plan','prescreen','running','{}')`,
		prescreenTodo, pid); err != nil {
		t.Fatalf("seed prescreen todo: %v", err)
	}
	reviewModel := llm.NewScriptedLLM(llm.WithResponses(
		llm.Response{Text: `{"score":77,"flags":[],"note":"ok"}`},
	))
	w := newPrescreenWorker(t, studioagents.NewReviewAgent(reviewModel))
	_, err := w.runPrescreen(ctx, claimed{todoID: prescreenTodo, projectID: pid, typ: "prescreen", attempts: 1, input: []byte("{}")})
	if err == nil {
		t.Fatalf("err = nil, want a 'no upstream text node' error")
	}
}
