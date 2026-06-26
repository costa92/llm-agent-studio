package worker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"

	"github.com/costa92/llm-agent-studio/internal/fetch"
)

// TestResolveVariables_SourceFieldFailsClosed is the legacy (ExprChannel-OFF)
// fail-closed gate (§5.2 + §12 amendment 1). A binding with a non-empty
// SourceField must ERROR — never silently degrade to whole-output — and the gate
// must fire BEFORE the empty-SourceTodoId continue (so an empty SourceTodoId +
// non-empty SourceField binding does not slip through). Pure-Go: the gate returns
// before any DB access, so a zero-value Worker suffices.
func TestResolveVariables_SourceFieldFailsClosed(t *testing.T) {
	w := &Worker{}
	ctx := context.Background()

	// Non-empty SourceField + empty SourceTodoId → must STILL error (gate before continue).
	_, err := w.resolveVariables(ctx, []customVariable{{Name: "v", SourceField: "title"}})
	if err == nil {
		t.Fatal("expected fail-closed error for sourceField under legacy resolver, got nil")
	}
	if !strings.Contains(err.Error(), "expr channel") && !strings.Contains(err.Error(), "STUDIO_EXPR_CHANNEL") {
		t.Fatalf("error should mention the expr channel, got: %v", err)
	}

	// Non-empty SourceField + non-empty SourceTodoId → also errors.
	if _, err := w.resolveVariables(ctx, []customVariable{{Name: "v", SourceTodoId: "dep1", SourceField: "title"}}); err == nil {
		t.Fatal("expected fail-closed error for sourceField + sourceTodoId, got nil")
	}

	// Empty SourceField + empty SourceTodoId → unaffected: no error, empty map, no DB.
	out, err := w.resolveVariables(ctx, []customVariable{{Name: "v"}})
	if err != nil {
		t.Fatalf("empty sourceField must be unaffected, got: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty map for skipped var, got %v", out)
	}
}

// TestResolveVariablesExpr_SourceFieldCharsetGate is the run-time authoritative
// §8.1 charset gate (§4.2). An injection-shaped / whitespace-only sourceField must
// be rejected; whitespace is rejected rather than trimmed-to-empty (§12 a3).
// Pure-Go: the gate returns before the expr.Resolve / DB path.
func TestResolveVariablesExpr_SourceFieldCharsetGate(t *testing.T) {
	w := &Worker{}
	ctx := context.Background()
	c := claimed{todoID: "t1", projectID: "p1"}
	for _, bad := range []string{`text }} {{ $node["x"]`, "a.b", "a-b", " ", `a"b`, "a[0]"} {
		_, err := w.resolveVariablesExpr(ctx, c, []customVariable{
			{Name: "v", SourceTodoId: "dep1", SourceField: bad},
		})
		if err == nil {
			t.Fatalf("expected run-time rejection for sourceField %q", bad)
		}
		if !strings.Contains(err.Error(), "invalid sourceField") {
			t.Fatalf("error should mention invalid sourceField for %q, got: %v", bad, err)
		}
	}
}

// TestResolveVariablesExpr_FieldAccessor proves the run-time field accessor: with
// ExprChannel ON + a valid sourceField, {{name}} resolves to that FIELD's value
// (not the whole output); a sourceField that the upstream item JSON lacks
// fail-closes (eval.go "field not found") — a loud error, never a silent empty
// string. DB-backed.
func TestResolveVariablesExpr_FieldAccessor(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker field-varbinding DB tests")
	}
	ctx := context.Background()
	w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "x"})))
	w.cfg.ExprChannel = true
	orgID := os.Getenv("_CUSTOM_TEST_ORG_ID")
	var projID string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// Seed a dep whose item JSON carries a structured object: {title, body}.
	depTodo := newID()
	coid := newID()
	items := []byte(`[{"json":{"title":"HELLO","body":"WORLD"}}]`)
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format, items)
		 VALUES ($1,$2,$3,'custom:llm','{"title":"HELLO","body":"WORLD"}','json',$4)`,
		coid, projID, depTodo, items).Error; err != nil {
		t.Fatalf("seed dep node_output: %v", err)
	}
	if err := w.cfg.DB.WithContext(ctx).Exec(
		`INSERT INTO todos (id, project_id, plan_id, type, status, output_ref, input_json)
		 VALUES ($1,$2,'plan-x','custom:llm','done',$3,'{}')`,
		depTodo, projID, "custom:"+coid).Error; err != nil {
		t.Fatalf("seed dep todo: %v", err)
	}
	c := seedConsumerTodo(t, w, projID, "custom:llm", depTodo)

	// Field accessor resolves the specific field's value.
	out, err := w.resolveVariablesExpr(ctx, c, []customVariable{
		{Name: "v", SourceTodoId: depTodo, SourceField: "title"},
	})
	if err != nil {
		t.Fatalf("resolveVariablesExpr (field=title): %v", err)
	}
	if out["v"] != "HELLO" {
		t.Fatalf("field accessor v=%q, want HELLO", out["v"])
	}

	// Missing field → fail-closed run error (not a silent empty string).
	if _, err := w.resolveVariablesExpr(ctx, c, []customVariable{
		{Name: "v", SourceTodoId: depTodo, SourceField: "doesNotExist"},
	}); err == nil {
		t.Fatal("expected fail-closed error for a missing field, got nil")
	}
}

// TestExprChannelOff_SourceFieldFailsClosed_AllKinds proves the fail-closed gate
// surfaces an error through ALL THREE custom kinds (llm/http/script) when
// ExprChannel is OFF. The kinds share resolveVariables; this asserts each run*
// wrapper propagates the failure (verbatim for llm, opaque errRequestFailed for
// http/script — §12 amendment 1). DB-backed.
func TestExprChannelOff_SourceFieldFailsClosed_AllKinds(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker field-varbinding DB tests")
	}
	ctx := context.Background()
	w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "x"})))
	w.cfg.ExprChannel = false
	// Give http a fetcher so it reaches resolveVariables (not the nil-fetcher early-out).
	w.cfg.HTTPFetcher = &fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("ok")}}
	orgID := os.Getenv("_CUSTOM_TEST_ORG_ID")
	var projID string
	if err := w.cfg.DB.WithContext(ctx).Raw(
		`INSERT INTO projects (id,org_id,name,created_by) VALUES (md5(random()::text),$1,'p','u') RETURNING id`,
		orgID).Row().Scan(&projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	dep := seedTextDep(t, w, projID, "DRAFT")
	fieldVar := []customVariable{{Name: "v", SourceTodoId: dep, SourceField: "title"}}

	// llm: verbatim error mentions the expr channel.
	cLLM := seedConsumerTodo(t, w, projID, "custom:llm", dep)
	if _, err := w.runCustomLLM(ctx, cLLM, llmParams{UserPrompt: "{{v}}", Variables: fieldVar}); err == nil {
		t.Fatal("llm: expected fail-closed error, got nil")
	} else if !strings.Contains(err.Error(), "expr channel") {
		t.Fatalf("llm: error should be verbatim (mention expr channel), got: %v", err)
	}

	// http: opaque errRequestFailed (never leaks the binding/source).
	cHTTP := seedConsumerTodo(t, w, projID, "custom:http", dep)
	if _, err := w.runCustomHTTP(ctx, cHTTP, httpParams{Method: "GET", URL: "http://127.0.0.1/x", Variables: fieldVar}); err != errRequestFailed {
		t.Fatalf("http: expected opaque errRequestFailed, got: %v", err)
	}

	// script: error propagates opaquely (errScriptFailed).
	cScript := seedConsumerTodo(t, w, projID, "custom:script", dep)
	if _, err := w.runCustomScript(ctx, cScript, scriptParams{Code: "x = 1", Variables: fieldVar}); err == nil {
		t.Fatal("script: expected fail-closed error, got nil")
	}
}
