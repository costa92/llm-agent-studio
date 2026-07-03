package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/storage"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

func newPlanner(t *testing.T) (*Planner, *storage.Storage, string) {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run planner tests")
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
	projID := "plp_" + randHex2()
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO projects (id, org_id, name, created_by) VALUES ($1,'o','n','u')`, projID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return New(todos.New(st.GORM()), st.GORM()), st, projID
}

func TestValidateCustomGraph(t *testing.T) {
	cases := []struct {
		name    string
		nodes   []WorkflowNode
		wantErr string
	}{
		{
			name:    "empty graph",
			nodes:   nil,
			wantErr: "custom workflow: empty graph",
		},
		{
			name: "empty node id",
			nodes: []WorkflowNode{
				{ID: "", Type: "script"},
			},
			wantErr: "custom workflow: node with empty id",
		},
		{
			name: "duplicate id",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "script"},
				{ID: "node1", Type: "storyboard"},
			},
			wantErr: "custom workflow: duplicate node id",
		},
		{
			name: "invalid type",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "invalid_type"},
			},
			wantErr: "custom workflow: node \"node1\" has non-whitelisted type \"invalid_type\"",
		},
		{
			name: "unknown dependency",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "script", DependsOn: []string{"unknown_node"}},
			},
			wantErr: "custom workflow: node \"node1\" depends on unknown node \"unknown_node\"",
		},
		{
			name: "dependency cycle",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "script", DependsOn: []string{"node2"}},
				{ID: "node2", Type: "storyboard", DependsOn: []string{"node1"}},
			},
			wantErr: "custom workflow: dependency cycle at",
		},
		{
			name: "valid graph",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "script"},
				{ID: "node2", Type: "storyboard", DependsOn: []string{"node1"}},
				{ID: "node3", Type: "asset", DependsOn: []string{"node2"}},
			},
			wantErr: "",
		},
		{
			name: "custom type accepted",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "script"},
				{ID: "node2", Type: "custom:translate", DependsOn: []string{"node1"}},
			},
			wantErr: "",
		},
		{
			name: "empty custom slug rejected",
			nodes: []WorkflowNode{
				{ID: "node1", Type: "custom:"},
			},
			wantErr: "custom workflow: node \"node1\" has non-whitelisted type \"custom:\"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCustomGraph(tc.nodes)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
				}
			}
		})
	}
}

// TestValidateCustomGraph_Exported verifies the exported ValidateCustomGraph is
// callable from outside the package and delegates correctly to the same logic.
func TestValidateCustomGraph_Exported(t *testing.T) {
	// Cyclic 2-node graph: A depends on B, B depends on A.
	cyclic := []WorkflowNode{
		{ID: "A", Type: "script", DependsOn: []string{"B"}},
		{ID: "B", Type: "storyboard", DependsOn: []string{"A"}},
	}
	err := ValidateCustomGraph(cyclic)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error should mention \"cycle\", got: %v", err)
	}

	// Valid linear graph: script → storyboard.
	linear := []WorkflowNode{
		{ID: "A", Type: "script"},
		{ID: "B", Type: "storyboard", DependsOn: []string{"A"}},
	}
	if err := ValidateCustomGraph(linear); err != nil {
		t.Fatalf("valid linear graph should return nil, got: %v", err)
	}
}

func TestPlanCustom(t *testing.T) {
	p, st, projID := newPlanner(t)
	ctx := context.Background()

	// 1. Seed a prompt in the prompts table
	promptID := "prompt_" + randHex2()
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO prompts (id, org_id, name, content) VALUES ($1,'o','custom_prompt','Custom Script Prompt Template')`, promptID); err != nil {
		t.Fatalf("seed prompt: %v", err)
	}

	nodes := []WorkflowNode{
		{ID: "node-script", Type: "script", PromptID: promptID},
		{ID: "node-storyboard", Type: "storyboard", DependsOn: []string{"node-script"}},
	}

	// Seed a workflow so the run can be tagged with its workflow_id.
	wfID := "wf_" + randHex2()
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO workflows (id, project_id, name, nodes) VALUES ($1,$2,'wf','[]')`, wfID, projID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	res, err := p.PlanCustom(ctx, projID, wfID, Brief{Brief: "custom brief", Style: "custom style"}, nodes, nil, nil)
	if err != nil {
		t.Fatalf("PlanCustom: %v", err)
	}

	if !res.Valid || res.FallbackUsed {
		t.Fatalf("want valid plan, got %+v", res)
	}

	// The plan row is tagged with the workflow id (run belongs to a workflow).
	var gotWF string
	if err := st.Pool().QueryRow(ctx, `SELECT COALESCE(workflow_id,'') FROM plans WHERE id=$1`, res.PlanID).Scan(&gotWF); err != nil {
		t.Fatalf("query plan workflow_id: %v", err)
	}
	if gotWF != wfID {
		t.Fatalf("plan workflow_id=%q want %q", gotWF, wfID)
	}

	// Verify todos created (this run only — scope by the plan id, since the
	// legacy-run assertion below adds more todos to the same project).
	var count int
	err = st.Pool().QueryRow(ctx, `SELECT count(*) FROM todos WHERE plan_id=$1`, res.PlanID).Scan(&count)
	if err != nil || count != 2 {
		t.Fatalf("expected 2 todos, got %d (err: %v)", count, err)
	}

	// Verify the script todo contains the brief and systemPrompt
	var inputJSON string
	err = st.Pool().QueryRow(ctx, `SELECT input_json FROM todos WHERE project_id=$1 AND type='script'`, projID).Scan(&inputJSON)
	if err != nil {
		t.Fatalf("query script input_json: %v", err)
	}

	if !strings.Contains(inputJSON, "Custom Script Prompt Template") || !strings.Contains(inputJSON, "custom brief") {
		t.Fatalf("expected inputJSON to contain prompt template and brief, got %q", inputJSON)
	}

	// An empty workflowID stores NULL (legacy project-level custom run).
	res2, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "legacy"}, nodes, nil, nil)
	if err != nil {
		t.Fatalf("PlanCustom legacy: %v", err)
	}
	var nullWF *string
	if err := st.Pool().QueryRow(ctx, `SELECT workflow_id FROM plans WHERE id=$1`, res2.PlanID).Scan(&nullWF); err != nil {
		t.Fatalf("query legacy plan workflow_id: %v", err)
	}
	if nullWF != nil {
		t.Fatalf("legacy run workflow_id should be NULL, got %q", *nullWF)
	}
}

// TestPlanCustomBuiltinPrompt: a node referencing a built-in preset id
// ("builtin:…") resolves its systemPrompt from code, with NO prompts table row.
func TestPlanCustomBuiltinPrompt(t *testing.T) {
	p, st, projID := newPlanner(t)
	ctx := context.Background()

	nodes := []WorkflowNode{{ID: "node-script", Type: "script", PromptID: "builtin:script-basic"}}
	res, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "b"}, nodes, nil, nil)
	if err != nil {
		t.Fatalf("PlanCustom: %v", err)
	}
	var inputJSON string
	if err := st.Pool().QueryRow(ctx,
		`SELECT input_json FROM todos WHERE plan_id=$1 AND type='script'`, res.PlanID).Scan(&inputJSON); err != nil {
		t.Fatalf("query script input_json: %v", err)
	}
	if !strings.Contains(inputJSON, "专业的短视频编剧") {
		t.Fatalf("built-in systemPrompt not resolved into input_json: %q", inputJSON)
	}
}

func TestHasUnboundCustomNode(t *testing.T) {
	annotated := []WorkflowNode{{ID: "a", Type: "custom:note"}}
	if !HasUnboundCustomNode(annotated) {
		t.Fatal("annotation custom node must be unbound")
	}
	typed := []WorkflowNode{{ID: "a", Type: "custom:llm", TypeId: "reg-1"}}
	if HasUnboundCustomNode(typed) {
		t.Fatal("typed custom node must NOT be unbound")
	}
}

// TestPlanCustomInlinePromptText: an inline PromptText on a node is used directly
// as systemPrompt and takes precedence over PromptID (no DB/builtin lookup).
func TestPlanCustomInlinePromptText(t *testing.T) {
	p, st, projID := newPlanner(t)
	ctx := context.Background()

	// PromptText set AND a (would-be-erroring) PromptID → PromptText wins, and the
	// bogus PromptID is never resolved (else this would error).
	nodes := []WorkflowNode{{
		ID: "node-script", Type: "script",
		PromptID:   "nonexistent-id",
		PromptText: "临时手写的系统提示词内容",
	}}
	res, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "b"}, nodes, nil, nil)
	if err != nil {
		t.Fatalf("PlanCustom: %v", err)
	}
	var inputJSON string
	if err := st.Pool().QueryRow(ctx,
		`SELECT input_json FROM todos WHERE plan_id=$1 AND type='script'`, res.PlanID).Scan(&inputJSON); err != nil {
		t.Fatalf("query script input_json: %v", err)
	}
	if !strings.Contains(inputJSON, "临时手写的系统提示词内容") {
		t.Fatalf("inline PromptText not used as systemPrompt: %q", inputJSON)
	}
}

// TestPlanCustom_TypedVariableRewrite: a typed custom node gets its variable
// bindings rewritten from local node ids to todo ids (two-pass) and the final
// input_json has sourceTodoId with NO sourceNodeId key.
func TestPlanCustom_TypedVariableRewrite(t *testing.T) {
	p, st, projID := newPlanner(t)
	ctx := context.Background()

	// Seed a workflow.
	wfID := "wf_" + randHex2()
	if _, err := st.Pool().Exec(ctx,
		`INSERT INTO workflows (id, project_id, name, nodes) VALUES ($1,$2,'wf','[]')`, wfID, projID); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	// script-1 → c1 (typed custom node referencing registry entry "reg-1").
	// VarBindings on the node bind {{draft}} to script-1 (workflow-local id).
	// Registry params do NOT carry variables (that would be wrong — they're org-level).
	regParams, _ := json.Marshal(map[string]interface{}{
		"systemPrompt": "s", "userPrompt": "{{draft}}", "outputFormat": "text",
	})
	nodes := []WorkflowNode{
		{ID: "script-1", Type: "script"},
		{
			ID: "c1", Type: "custom:llm", TypeId: "reg-1",
			DependsOn:   []string{"script-1"},
			VarBindings: []CustomVariable{{Name: "draft", SourceNodeId: "script-1"}},
		},
	}
	resolved := map[string]ResolvedType{
		"c1": {Kind: "llm", Params: regParams},
	}

	res, err := p.PlanCustom(ctx, projID, wfID, Brief{Brief: "b"}, nodes, resolved, nil)
	if err != nil {
		t.Fatalf("PlanCustom: %v", err)
	}

	// Determine the script-1 todo id from the idMap by querying the DB.
	var scriptTodoID string
	if err := st.Pool().QueryRow(ctx,
		`SELECT id FROM todos WHERE plan_id=$1 AND type='script'`, res.PlanID).Scan(&scriptTodoID); err != nil {
		t.Fatalf("query script todo id: %v", err)
	}

	// Read c1's input_json after pass 2.
	var rawInput string
	if err := st.Pool().QueryRow(ctx,
		`SELECT input_json FROM todos WHERE plan_id=$1 AND type='custom:llm'`, res.PlanID).Scan(&rawInput); err != nil {
		t.Fatalf("query c1 input_json: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(rawInput), &got); err != nil {
		t.Fatalf("unmarshal input_json: %v", err)
	}

	if got["kind"] != "llm" {
		t.Fatalf("expected kind==\"llm\", got %v", got["kind"])
	}

	params, ok := got["params"].(map[string]interface{})
	if !ok {
		t.Fatalf("params not a map: %T", got["params"])
	}

	vars, ok := params["variables"].([]interface{})
	if !ok || len(vars) != 1 {
		t.Fatalf("expected 1 variable, got %v", params["variables"])
	}

	v, ok := vars[0].(map[string]interface{})
	if !ok {
		t.Fatalf("variable not a map: %T", vars[0])
	}

	// Must have sourceTodoId == the script todo's id.
	if v["sourceTodoId"] != scriptTodoID {
		t.Fatalf("sourceTodoId=%q want %q", v["sourceTodoId"], scriptTodoID)
	}
	// Must NOT have sourceNodeId key (pass 2 drops the local key).
	if _, has := v["sourceNodeId"]; has {
		t.Fatalf("sourceNodeId should not be present after pass 2 rewrite, got input_json=%s", rawInput)
	}
}

// TestPlanCustom_SourceFieldPassthrough: a typed node varBinding carrying a
// sourceField survives BOTH planner passes into todos.input_json.params.variables
// (alongside the rewritten sourceTodoId), while a sibling binding WITHOUT a
// sourceField produces NO sourceField key (omitempty parity with today). DB-backed.
func TestPlanCustom_SourceFieldPassthrough(t *testing.T) {
	p, st, projID := newPlanner(t)
	ctx := context.Background()

	regParams, _ := json.Marshal(map[string]interface{}{
		"systemPrompt": "s", "userPrompt": "{{a}} {{b}}", "outputFormat": "text",
	})
	nodes := []WorkflowNode{
		{ID: "script-1", Type: "script"},
		{
			ID: "c1", Type: "custom:llm", TypeId: "reg-1",
			DependsOn: []string{"script-1"},
			VarBindings: []CustomVariable{
				{Name: "a", SourceNodeId: "script-1", SourceField: "characterSheet"},
				{Name: "b", SourceNodeId: "script-1"}, // no sourceField → whole output
			},
		},
	}
	resolved := map[string]ResolvedType{"c1": {Kind: "llm", Params: regParams}}

	res, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "b"}, nodes, resolved, nil)
	if err != nil {
		t.Fatalf("PlanCustom: %v", err)
	}

	var scriptTodoID string
	if err := st.Pool().QueryRow(ctx,
		`SELECT id FROM todos WHERE plan_id=$1 AND type='script'`, res.PlanID).Scan(&scriptTodoID); err != nil {
		t.Fatalf("query script todo id: %v", err)
	}
	var rawInput string
	if err := st.Pool().QueryRow(ctx,
		`SELECT input_json FROM todos WHERE plan_id=$1 AND type='custom:llm'`, res.PlanID).Scan(&rawInput); err != nil {
		t.Fatalf("query c1 input_json: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(rawInput), &got); err != nil {
		t.Fatalf("unmarshal input_json: %v", err)
	}
	params := got["params"].(map[string]interface{})
	vars := params["variables"].([]interface{})
	if len(vars) != 2 {
		t.Fatalf("expected 2 variables, got %v", vars)
	}
	byName := map[string]map[string]interface{}{}
	for _, raw := range vars {
		v := raw.(map[string]interface{})
		byName[v["name"].(string)] = v
	}
	// a: sourceField survived + sourceTodoId rewritten.
	if byName["a"]["sourceField"] != "characterSheet" {
		t.Fatalf("var a sourceField=%v want characterSheet (input=%s)", byName["a"]["sourceField"], rawInput)
	}
	if byName["a"]["sourceTodoId"] != scriptTodoID {
		t.Fatalf("var a sourceTodoId=%v want %q", byName["a"]["sourceTodoId"], scriptTodoID)
	}
	// b: NO sourceField key (omitempty parity with today).
	if _, has := byName["b"]["sourceField"]; has {
		t.Fatalf("var b should have NO sourceField key, got input=%s", rawInput)
	}
}

// TestPlanCustom_VariableNotInDependsOn: a typed node whose VarBindings reference
// a node NOT in DependsOn must be rejected with an error containing "must be in dependsOn".
func TestPlanCustom_VariableNotInDependsOn(t *testing.T) {
	p, _, projID := newPlanner(t)
	ctx := context.Background()

	regParams, _ := json.Marshal(map[string]interface{}{
		"systemPrompt": "s", "userPrompt": "{{draft}}", "outputFormat": "text",
	})
	// c1 depends only on script-1 but binds to "script-2" (not in DependsOn).
	nodes := []WorkflowNode{
		{ID: "script-1", Type: "script"},
		{ID: "script-2", Type: "script"},
		{
			ID: "c1", Type: "custom:llm", TypeId: "reg-1",
			DependsOn:   []string{"script-1"},
			VarBindings: []CustomVariable{{Name: "draft", SourceNodeId: "script-2"}},
		},
	}
	resolved := map[string]ResolvedType{
		"c1": {Kind: "llm", Params: regParams},
	}

	_, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "b"}, nodes, resolved, nil)
	if err == nil {
		t.Fatal("expected error for binding outside DependsOn, got nil")
	}
	if !strings.Contains(err.Error(), "must be in dependsOn") {
		t.Fatalf("error should contain \"must be in dependsOn\", got: %v", err)
	}
}

// TestSourceFieldRegex: the §8.1 charset gate accepts safe OutputSchema field
// identifiers and rejects injection-shaped / whitespace inputs. Pure-Go.
func TestSourceFieldRegex(t *testing.T) {
	for _, ok := range []string{"title", "characterSheet", "score", "_x", "a1", "logline"} {
		if !fieldNameRe.MatchString(ok) {
			t.Errorf("fieldNameRe should ACCEPT %q", ok)
		}
	}
	bad := []string{
		"",                     // empty (handled separately as "whole output", not via the gate)
		" ",                    // whitespace-only (must NOT degrade to empty — §12 a3)
		"a.b",                  // dotted path (multi-level — out of scope)
		"a-b",                  // hyphen
		`a"b`,                  // quote
		`text }} {{ $node["x"`, // template injection attempt
		"1abc",                 // leading digit
		"a b",                  // embedded space
		"a}b",                  // closing brace
		"a[0]",                 // index
	}
	for _, f := range bad {
		if fieldNameRe.MatchString(f) {
			t.Errorf("fieldNameRe should REJECT %q", f)
		}
	}
}

// TestPlanCustom_SourceFieldCharsetGate: the plan-time gate rejects an
// injection-shaped sourceField, INDEPENDENT of SourceNodeId (§12 a5). Pure-Go:
// the gate returns before any DB access, so a zero-value Planner suffices.
func TestPlanCustom_SourceFieldCharsetGate(t *testing.T) {
	p := &Planner{} // gate fires before p.db / p.todos are touched
	ctx := context.Background()
	regParams, _ := json.Marshal(map[string]interface{}{"systemPrompt": "s"})
	mk := func(field, srcNode string) []WorkflowNode {
		return []WorkflowNode{
			{ID: "script-1", Type: "script"},
			{ID: "c1", Type: "custom:llm", TypeId: "reg-1",
				DependsOn:   []string{"script-1"},
				VarBindings: []CustomVariable{{Name: "draft", SourceNodeId: srcNode, SourceField: field}}},
		}
	}
	resolved := map[string]ResolvedType{"c1": {Kind: "llm", Params: regParams}}

	for _, f := range []string{`text }} {{ $node["x"]`, "a.b", "a-b", " ", `a"b`} {
		_, err := p.PlanCustom(ctx, "proj", "", Brief{}, mk(f, "script-1"), resolved, nil)
		if err == nil {
			t.Fatalf("expected plan-time rejection for sourceField %q", f)
		}
		if !strings.Contains(err.Error(), "sourceField") {
			t.Fatalf("error should mention sourceField for %q, got: %v", f, err)
		}
	}
	// §12 a5: bad field rejected even with empty SourceNodeId (independent of the
	// SourceNodeId continue).
	_, err := p.PlanCustom(ctx, "proj", "", Brief{}, mk("a.b", ""), resolved, nil)
	if err == nil || !strings.Contains(err.Error(), "sourceField") {
		t.Fatalf("expected sourceField rejection with empty SourceNodeId, got: %v", err)
	}
}

func TestWorkflowNodeParametersRoundTrip(t *testing.T) {
	in := WorkflowNode{
		ID:          "node-3",
		Type:        "custom:my-llm",
		TypeId:      "a1b2c3",
		DependsOn:   []string{"script-1"},
		TypeVersion: 1,
		Parameters:  json.RawMessage(`{"temperature":0.2,"outputFormat":"json"}`),
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out WorkflowNode
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.TypeVersion != 1 {
		t.Fatalf("typeVersion lost: %d", out.TypeVersion)
	}
	if string(out.Parameters) != `{"temperature":0.2,"outputFormat":"json"}` {
		t.Fatalf("parameters lost: %s", out.Parameters)
	}
	// omitempty: a node without the new fields must NOT emit the keys.
	bare, _ := json.Marshal(WorkflowNode{ID: "n", Type: "script", DependsOn: []string{}})
	if strings.Contains(string(bare), "typeVersion") || strings.Contains(string(bare), "parameters") {
		t.Fatalf("omitempty broken: %s", bare)
	}
}

// TestPlanRunInputs verifies the run-time inputs snapshot lands in
// plans.run_inputs: a non-nil JSON is stored verbatim, and a nil snapshot
// defaults to '{}' (never NULL) — for the custom-workflow path (PlanCustom).
func TestPlanRunInputs(t *testing.T) {
	p, st, projID := newPlanner(t)
	ctx := context.Background()

	// JSONB reformats on storage (adds whitespace); compare compacted forms.
	compact := func(s string) string {
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(s)); err != nil {
			t.Fatalf("compact %q: %v", s, err)
		}
		return buf.String()
	}

	ri := json.RawMessage(`{"values":{"x":"1"}}`)
	var got string

	// PlanCustom stores the snapshot.
	nodes := []WorkflowNode{{ID: "node-script", Type: "script", DependsOn: []string{}}}
	resC, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "b"}, nodes, nil, ri)
	if err != nil {
		t.Fatalf("PlanCustom: %v", err)
	}
	if err := st.Pool().QueryRow(ctx, `SELECT run_inputs FROM plans WHERE id=$1`, resC.PlanID).Scan(&got); err != nil {
		t.Fatalf("query custom run_inputs: %v", err)
	}
	if compact(got) != `{"values":{"x":"1"}}` {
		t.Fatalf("PlanCustom run_inputs=%q want %q", got, `{"values":{"x":"1"}}`)
	}

	// PlanCustom with nil → '{}'.
	resCNil, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "b"}, nodes, nil, nil)
	if err != nil {
		t.Fatalf("PlanCustom nil: %v", err)
	}
	if err := st.Pool().QueryRow(ctx, `SELECT run_inputs FROM plans WHERE id=$1`, resCNil.PlanID).Scan(&got); err != nil {
		t.Fatalf("query custom nil run_inputs: %v", err)
	}
	if got != `{}` {
		t.Fatalf("PlanCustom nil run_inputs=%q want '{}'", got)
	}
}
