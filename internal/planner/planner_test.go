package planner

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"

	"github.com/costa92/llm-agent-studio/internal/storage"
	"github.com/costa92/llm-agent-studio/internal/todos"
)

func newPlanner(t *testing.T, model llm.ChatModel) (*Planner, *storage.Storage, string) {
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
	return New(model, todos.New(st.GORM()), st.Pool()), st, projID
}

func TestPlanValidGraph(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`,
	}))
	p, st, projID := newPlanner(t, model)
	ctx := context.Background()
	res, err := p.Plan(ctx, projID, Brief{Brief: "make an ad", Style: "realistic"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !res.Valid || res.FallbackUsed {
		t.Fatalf("want valid plan, got %+v", res)
	}
	var n int
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM todos WHERE project_id=$1`, projID).Scan(&n)
	if n != 2 {
		t.Fatalf("want 2 todos, got %d", n)
	}
	// Exactly the dep-free script node is initially ready (run handler emits
	// todo_ready for it).
	if len(res.ReadyTodos) != 1 || res.ReadyTodos[0].Type != "script" {
		t.Fatalf("want 1 ready script todo, got %+v", res.ReadyTodos)
	}
}

func TestPlanWithUsesPassedModel(t *testing.T) {
	// Bound model would FALL BACK (refusal); the passed model returns a valid graph.
	bound := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "I refuse to plan."}))
	routed := llm.NewScriptedLLM(llm.WithResponses(llm.Response{
		Text: `{"nodes":[{"id":"s","type":"script","dependsOn":[]},{"id":"b","type":"storyboard","dependsOn":["s"]}]}`,
	}))
	p, _, projID := newPlanner(t, bound)
	res, err := p.PlanWith(context.Background(), projID, routed, Brief{Brief: "ad"})
	if err != nil {
		t.Fatalf("planWith: %v", err)
	}
	if !res.Valid || res.FallbackUsed {
		t.Fatalf("PlanWith ignored the passed model: %+v", res)
	}
}

func TestPlanFallbackOnMalformed(t *testing.T) {
	model := llm.NewScriptedLLM(llm.WithResponses(llm.Response{Text: "I refuse to plan."}))
	p, st, projID := newPlanner(t, model)
	ctx := context.Background()
	res, err := p.Plan(ctx, projID, Brief{Brief: "make an ad"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !res.FallbackUsed || res.Valid {
		t.Fatalf("want fallback, got %+v", res)
	}
	var n int
	_ = st.Pool().QueryRow(ctx, `SELECT count(*) FROM todos WHERE project_id=$1`, projID).Scan(&n)
	if n != 2 { // default pipeline = script + storyboard
		t.Fatalf("want 2 fallback todos, got %d", n)
	}
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
	p, st, projID := newPlanner(t, nil)
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

	res, err := p.PlanCustom(ctx, projID, wfID, Brief{Brief: "custom brief", Style: "custom style"}, nodes)
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
	res2, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "legacy"}, nodes)
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
	p, st, projID := newPlanner(t, nil)
	ctx := context.Background()

	nodes := []WorkflowNode{{ID: "node-script", Type: "script", PromptID: "builtin:script-basic"}}
	res, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "b"}, nodes)
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

// TestPlanCustomInlinePromptText: an inline PromptText on a node is used directly
// as systemPrompt and takes precedence over PromptID (no DB/builtin lookup).
func TestPlanCustomInlinePromptText(t *testing.T) {
	p, st, projID := newPlanner(t, nil)
	ctx := context.Background()

	// PromptText set AND a (would-be-erroring) PromptID → PromptText wins, and the
	// bogus PromptID is never resolved (else this would error).
	nodes := []WorkflowNode{{
		ID: "node-script", Type: "script",
		PromptID:   "nonexistent-id",
		PromptText: "临时手写的系统提示词内容",
	}}
	res, err := p.PlanCustom(ctx, projID, "", Brief{Brief: "b"}, nodes)
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
