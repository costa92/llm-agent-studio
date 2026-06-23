package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/events"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/todos"
	"gorm.io/gorm"
)

// scriptTestWorker builds a Worker for the "script" path: real DB + Todos +
// Projects + Events. The script kind needs no Router, Secrets, or HTTPFetcher
// (sandboxed Starlark has no I/O), so the wiring is minimal.
func scriptTestWorker(t *testing.T, db *gorm.DB) *Worker {
	t.Helper()
	return New(Config{
		DB:       db,
		Todos:    todos.New(db),
		Projects: project.New(db),
		Events:   events.New(db),
		WorkerID: "script-test", Lease: time.Minute, MaxAttempts: 3, BaseBackoff: time.Millisecond,
	})
}

// seedScriptUpstream inserts a project (under orgID), an upstream custom node
// whose node_outputs.content is upstreamText, and a done upstream todo pointing
// at it. Returns projID + the upstream todo id (the variable's sourceTodoId).
func seedScriptUpstream(t *testing.T, db *gorm.DB, orgID, upstreamText string) (string, string) {
	t.Helper()
	ctx := context.Background()
	var projID string
	if err := db.WithContext(ctx).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID,
	).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	upOutID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
		 VALUES ($1,$2,'t-up','custom:up',$3,'text')`,
		upOutID, projID, upstreamText).Error; err != nil {
		t.Fatalf("seed upstream node_output: %v", err)
	}
	upTodoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:up','done',$3,'{}')`,
		upTodoID, projID, "custom:"+upOutID).Error; err != nil {
		t.Fatalf("seed upstream todo: %v", err)
	}
	return projID, upTodoID
}

// seedScriptTodo inserts a script custom todo with the given input_json and
// returns its claimed.
func seedScriptTodo(t *testing.T, db *gorm.DB, projID string, input []byte) claimed {
	t.Helper()
	ctx := context.Background()
	todoID := newID()
	if err := db.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, input_json)
		 VALUES ($1,$2,'plan-x','custom:script','running',$3)`,
		todoID, projID, input).Error; err != nil {
		t.Fatalf("seed script todo: %v", err)
	}
	return claimed{todoID: todoID, projectID: projID, typ: "custom:script", attempts: 1, input: input}
}

func scriptInput(t *testing.T, code, outputFormat, varName, sourceTodoID string) []byte {
	t.Helper()
	params := map[string]any{
		"code":         code,
		"outputFormat": outputFormat,
	}
	if varName != "" {
		params["variables"] = []map[string]any{{"name": varName, "sourceTodoId": sourceTodoID}}
	}
	b, _ := json.Marshal(map[string]any{"kind": "script", "params": params})
	return b
}

func readNodeOutput(t *testing.T, db *gorm.DB, ref string) (string, string) {
	t.Helper()
	outID := strings.TrimPrefix(ref, "custom:")
	var format, content string
	if err := db.WithContext(context.Background()).Raw(
		`SELECT format, content FROM node_outputs WHERE id=$1`, outID).Row().Scan(&format, &content); err != nil {
		t.Fatalf("load node_output: %v", err)
	}
	return format, content
}

// TestRunCustomScript exercises the "script" (Starlark) kind end-to-end through
// runCustom: text output, json output, opaque failure errors (no source/var
// leakage), and the D1 {{secret:}} prohibition.
func TestRunCustomScript(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker custom tests")
	}
	ctx := context.Background()
	db := assetTestGorm(t)
	orgID := "org_script_" + randHex3()
	projID, upTodoID := seedScriptUpstream(t, db, orgID, "hello")
	w := scriptTestWorker(t, db)

	// 1. text output: upstream "hello" → output = up.upper() → "HELLO".
	t.Run("text output uppercases upstream variable", func(t *testing.T) {
		in := scriptInput(t, `output = up.upper()`, "text", "up", upTodoID)
		c := seedScriptTodo(t, db, projID, in)
		ref, err := w.runCustom(ctx, c)
		if err != nil {
			t.Fatalf("runCustom script: %v", err)
		}
		format, content := readNodeOutput(t, db, ref)
		if format != "text" {
			t.Fatalf("want format=text, got %q", format)
		}
		if content != "HELLO" {
			t.Fatalf("want content=HELLO, got %q", content)
		}
	})

	// 2. json output: json.encode lands format=json with the encoded value.
	t.Run("json output lands format=json", func(t *testing.T) {
		in := scriptInput(t, `output = json.encode({"k": 1})`, "json", "", "")
		c := seedScriptTodo(t, db, projID, in)
		ref, err := w.runCustom(ctx, c)
		if err != nil {
			t.Fatalf("runCustom script json: %v", err)
		}
		format, content := readNodeOutput(t, db, ref)
		if format != "json" {
			t.Fatalf("want format=json, got %q", format)
		}
		// Compare structurally — json.encode key order / spacing is engine-defined.
		var got map[string]any
		if err := json.Unmarshal([]byte(content), &got); err != nil {
			t.Fatalf("output not valid json: %q (%v)", content, err)
		}
		if v, ok := got["k"]; !ok || v != float64(1) {
			t.Fatalf("want {\"k\":1}, got %q", content)
		}
	})

	// 3. failing script: error is a bare script_* enum with NO source/var leakage.
	t.Run("failing script returns opaque enum, no leakage", func(t *testing.T) {
		// output assigned a non-string (5) → scriptengine.ErrFailed.
		const secretSrc = `output = 5  # LEAK_SENTINEL_zzz`
		in := scriptInput(t, secretSrc, "text", "up", upTodoID)
		c := seedScriptTodo(t, db, projID, in)
		_, err := w.runCustom(ctx, c)
		if err == nil {
			t.Fatalf("expected error for non-string output")
		}
		switch err.Error() {
		case "script_failed", "script_timeout", "script_output_missing", "script_output_too_large":
		default:
			t.Fatalf("non-opaque error: %q", err.Error())
		}
		if strings.Contains(err.Error(), "LEAK_SENTINEL") || strings.Contains(err.Error(), "output = 5") {
			t.Fatalf("source text leaked into error: %q", err.Error())
		}
		// Upstream variable value ("hello") must also not appear in the error.
		if strings.Contains(err.Error(), "hello") {
			t.Fatalf("variable value leaked into error: %q", err.Error())
		}
	})

	// 4. D1: code containing {{secret:K}} → errScriptFailed, no execution.
	t.Run("secret ref in code rejected as script_failed", func(t *testing.T) {
		in := scriptInput(t, `output = "{{secret:K}}"`, "text", "", "")
		c := seedScriptTodo(t, db, projID, in)
		_, err := w.runCustom(ctx, c)
		if err == nil {
			t.Fatalf("expected error for {{secret:}} in code")
		}
		if err.Error() != "script_failed" {
			t.Fatalf("want script_failed, got %q", err.Error())
		}
	})
}
