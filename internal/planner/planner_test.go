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
	return New(model, todos.New(st.Pool()), st.Pool()), st, projID
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
			err := validateCustomGraph(tc.nodes)
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

	res, err := p.PlanCustom(ctx, projID, Brief{Brief: "custom brief", Style: "custom style"}, nodes)
	if err != nil {
		t.Fatalf("PlanCustom: %v", err)
	}

	if !res.Valid || res.FallbackUsed {
		t.Fatalf("want valid plan, got %+v", res)
	}

	// Verify todos created
	var count int
	err = st.Pool().QueryRow(ctx, `SELECT count(*) FROM todos WHERE project_id=$1`, projID).Scan(&count)
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
}
