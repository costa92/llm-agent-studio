package planner

import (
	"context"
	"os"
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
