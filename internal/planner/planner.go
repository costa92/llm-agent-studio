package planner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/todos"
)

// Brief is the project brief the planner reasons over.
type Brief struct {
	Brief          string
	ContentType    string
	TargetPlatform string
	Style          string
}

// Result reports what Plan persisted.
type Result struct {
	PlanID       string
	Valid        bool
	FallbackUsed bool
	// ReadyTodos are the todos created in 'ready' status (no dependencies). The
	// run handler emits a todo_ready run_event for each so the very first node
	// (the script todo) appears in the SSE timeline before todo_started (spec §9).
	ReadyTodos []ReadyTodo
}

// ReadyTodo identifies an initially-ready todo for the run handler to announce.
type ReadyTodo struct {
	ID   string
	Type string
}

// Planner turns a brief into a persisted todo graph via one LLM call.
type Planner struct {
	agent coreagents.Agent
	todos *todos.Store
	pool  *pgxpool.Pool
}

const plannerSystemPrompt = `You are a content-production planner. Given a brief, output a todo graph as a JSON object with EXACTLY this shape and nothing else:
{"nodes":[{"id":string,"type":string,"dependsOn":[string]}]}
Allowed node types: "script", "storyboard". The graph MUST contain a "script" node, must be acyclic, and "storyboard" should depend on "script". Output ONLY the JSON object.`

// New builds a Planner. model is wrapped in a SimpleAgent (one Generate call).
func New(model llm.ChatModel, todoStore *todos.Store, pool *pgxpool.Pool) *Planner {
	return &Planner{
		agent: coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{
			Name: "planner", SystemPrompt: plannerSystemPrompt,
		}),
		todos: todoStore,
		pool:  pool,
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Plan asks the LLM for a graph, validates it, and persists plans + todos. On
// a malformed/invalid plan it falls back to the default pipeline (spec §7.1)
// and records fallback_used=true. The plans row stores the raw LLM text.
func (p *Planner) Plan(ctx context.Context, projectID string, b Brief) (Result, error) {
	planID := newID()
	prompt := fmt.Sprintf("Brief: %s\nContent type: %s\nTarget platform: %s\nStyle: %s",
		b.Brief, b.ContentType, b.TargetPlatform, b.Style)

	rawText := ""
	valid := false
	fallback := false
	graph := DefaultPipeline()

	res, err := p.agent.Run(ctx, prompt)
	if err != nil {
		// LLM failed → fallback (still produce a runnable pipeline).
		fallback = true
		rawText = fmt.Sprintf("agent error: %v", err)
	} else {
		rawText = res.Answer
		g, perr := ParseGraph(res.Answer)
		if perr == nil && Validate(g) == nil {
			graph = g
			valid = true
		} else {
			fallback = true
		}
	}

	// Persist the plan row (raw text stored as a JSON string for auditing).
	rawJSON, _ := json.Marshal(rawText)
	if _, err := p.pool.Exec(ctx,
		`INSERT INTO plans (id, project_id, status, raw_plan_json, valid, fallback_used)
		 VALUES ($1,$2,'created',$3,$4,$5)`,
		planID, projectID, rawJSON, valid, fallback); err != nil {
		return Result{}, fmt.Errorf("planner: insert plan: %w", err)
	}

	// Map the graph to todo specs. The script node carries the brief as input;
	// downstream nodes get an empty input (the worker fills it from upstream
	// artifacts at dispatch time).
	specs := make([]todos.NodeSpec, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		var input []byte
		if n.Type == "script" {
			input, _ = json.Marshal(map[string]string{
				"brief": b.Brief, "contentType": b.ContentType,
				"targetPlatform": b.TargetPlatform, "style": b.Style,
			})
		} else {
			input = []byte(`{}`)
		}
		specs = append(specs, todos.NodeSpec{
			LocalID: n.ID, Type: n.Type, DependsOn: n.DependsOn, InputJSON: input,
		})
	}
	idMap, err := p.todos.CreateGraph(ctx, projectID, planID, specs)
	if err != nil {
		return Result{}, fmt.Errorf("planner: create todo graph: %w", err)
	}
	// Collect the nodes created 'ready' (no dependencies) so the run handler can
	// emit todo_ready for them — CreateGraph starts dep-free nodes in 'ready'.
	var ready []ReadyTodo
	for _, n := range graph.Nodes {
		if len(n.DependsOn) == 0 {
			ready = append(ready, ReadyTodo{ID: idMap[n.ID], Type: n.Type})
		}
	}
	return Result{PlanID: planID, Valid: valid, FallbackUsed: fallback, ReadyTodos: ready}, nil
}
