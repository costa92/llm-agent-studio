package worker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
)

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
// a valid sourceField, {{name}} resolves to that FIELD's value (not the whole
// output); a sourceField that the upstream item JSON lacks fail-closes (eval.go
// "field not found") — a loud error, never a silent empty string. DB-backed.
func TestResolveVariablesExpr_FieldAccessor(t *testing.T) {
	if os.Getenv("LLM_AGENT_STUDIO_PG_URL") == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run worker field-varbinding DB tests")
	}
	ctx := context.Background()
	w := customTestWorker(t, llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "x"})))
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
