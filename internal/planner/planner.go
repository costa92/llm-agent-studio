package planner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	coreagents "github.com/costa92/llm-agent"
	"github.com/costa92/llm-agent-contract/llm"
	"gorm.io/gorm"

	"github.com/costa92/llm-agent-studio/internal/prompt"
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
	model llm.ChatModel // bound default for Plan; PlanWith routes per-org (BYOK)
	todos *todos.Store
	db    *gorm.DB
}

const plannerSystemPrompt = `You are a content-production planner. Given a brief, output a todo graph as a JSON object with EXACTLY this shape and nothing else:
{"nodes":[{"id":string,"type":string,"dependsOn":[string]}]}
Allowed node types: "script", "storyboard". The graph MUST contain a "script" node, must be acyclic, and "storyboard" should depend on "script". Output ONLY the JSON object.`

// New builds a Planner over the bound default model (used by Plan).
func New(model llm.ChatModel, todoStore *todos.Store, db *gorm.DB) *Planner {
	return &Planner{model: model, todos: todoStore, db: db}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Plan asks the LLM for a graph, validates it, and persists plans + todos. On
// a malformed/invalid plan it falls back to the default pipeline (spec §7.1)
// and records fallback_used=true. The plans row stores the raw LLM text. Uses
// the bound default model; PlanWith routes per-org (BYOK).
func (p *Planner) Plan(ctx context.Context, projectID string, b Brief) (Result, error) {
	return p.PlanWith(ctx, projectID, p.model, b)
}

// PlanWith is Plan with an explicit chat model (BYOK 模型路由): the run handler
// resolves the org's text model through the ModelRouter and passes it here.
func (p *Planner) PlanWith(ctx context.Context, projectID string, model llm.ChatModel, b Brief) (Result, error) {
	agent := coreagents.NewSimpleAgent(model, coreagents.SimpleOptions{
		Name: "planner", SystemPrompt: plannerSystemPrompt,
	})
	planID := newID()
	prompt := fmt.Sprintf("Brief: %s\nContent type: %s\nTarget platform: %s\nStyle: %s",
		b.Brief, b.ContentType, b.TargetPlatform, b.Style)

	rawText := ""
	valid := false
	fallback := false
	graph := DefaultPipeline()

	res, err := agent.Run(ctx, prompt)
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
	if err := p.db.WithContext(ctx).Exec(
		`INSERT INTO plans (id, project_id, status, raw_plan_json, valid, fallback_used)
		 VALUES ($1,$2,'created',$3,$4,$5)`,
		planID, projectID, rawJSON, valid, fallback).Error; err != nil {
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

// WorkflowNode defines a node in a custom agent workflow.
type WorkflowNode struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "script", "storyboard", "asset", etc.
	// PromptID references a prompts.id (org library) or a built-in preset id
	// ("builtin:…"). Ignored when PromptText is set.
	PromptID string `json:"promptId"`
	// PromptText is an inline, ad-hoc system prompt typed directly on the node
	// (not saved to the library). Takes precedence over PromptID when non-empty.
	PromptText string   `json:"promptText"`
	DependsOn  []string `json:"dependsOn"`
}

// ValidateCustomGraph enforces the custom-workflow DAG rules (non-empty, unique
// ids, whitelisted types, known deps, acyclic). Callers validate at save AND
// run time so a saved workflow is always runnable.
func ValidateCustomGraph(nodes []WorkflowNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("custom workflow: empty graph")
	}
	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n.ID == "" {
			return fmt.Errorf("custom workflow: node with empty id")
		}
		if ids[n.ID] {
			return fmt.Errorf("custom workflow: duplicate node id %q", n.ID)
		}
		ids[n.ID] = true
		if !isTypeAllowed(n.Type) {
			return fmt.Errorf("custom workflow: node %q has non-whitelisted type %q", n.ID, n.Type)
		}
	}
	for _, n := range nodes {
		for _, dep := range n.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("custom workflow: node %q depends on unknown node %q", n.ID, dep)
			}
		}
	}
	// Cycle detection using DFS
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodes))
	deps := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		deps[n.ID] = n.DependsOn
	}
	var visit func(id string) error
	visit = func(id string) error {
		color[id] = gray
		for _, dep := range deps[id] {
			switch color[dep] {
			case gray:
				return fmt.Errorf("custom workflow: dependency cycle at %q→%q", id, dep)
			case white:
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	for _, n := range nodes {
		if color[n.ID] == white {
			if err := visit(n.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// PlanCustom plans a workflow defined manually by the user, bypassing the LLM
// planner. workflowID ties the run (plans row) to its first-class workflow so a
// workflow's runs/assets/timeline can be isolated; pass "" for the legacy
// project-level custom run (stored as NULL workflow_id).
func (p *Planner) PlanCustom(ctx context.Context, projectID, workflowID string, b Brief, nodes []WorkflowNode) (Result, error) {
	if err := ValidateCustomGraph(nodes); err != nil {
		return Result{}, fmt.Errorf("planner: validate custom graph: %w", err)
	}

	planID := newID()

	// Persist the plan row. Status 'created', valid true, fallback_used false.
	// NULLIF maps an empty workflowID to NULL (the FK is nullable).
	rawJSON, _ := json.Marshal(nodes)
	if err := p.db.WithContext(ctx).Exec(
		`INSERT INTO plans (id, project_id, workflow_id, status, raw_plan_json, valid, fallback_used)
		 VALUES ($1,$2,NULLIF($3,''),'created',$4,true,false)`,
		planID, projectID, workflowID, rawJSON).Error; err != nil {
		return Result{}, fmt.Errorf("planner: insert plan: %w", err)
	}

	specs := make([]todos.NodeSpec, 0, len(nodes))
	for _, n := range nodes {
		inputMap := map[string]interface{}{}
		if n.Type == "script" {
			inputMap["brief"] = b.Brief
			inputMap["contentType"] = b.ContentType
			inputMap["targetPlatform"] = b.TargetPlatform
			inputMap["style"] = b.Style
		}

		// Prompt precedence: inline custom text > PromptID (builtin/library) >
		// the agent's built-in default (no systemPrompt set).
		if n.PromptText != "" {
			inputMap["systemPrompt"] = n.PromptText
		} else if n.PromptID != "" {
			// Built-in presets ("builtin:…") resolve from code (no DB row); any
			// other id is an org prompt-library entry resolved from the table.
			if content, ok := prompt.BasicPromptContent(n.PromptID); ok {
				inputMap["systemPrompt"] = content
			} else {
				var promptContent string
				err := p.db.WithContext(ctx).Raw("SELECT content FROM prompts WHERE id=$1", n.PromptID).Row().Scan(&promptContent)
				if err != nil {
					return Result{}, fmt.Errorf("planner: get prompt %q: %w", n.PromptID, err)
				}
				inputMap["systemPrompt"] = promptContent
			}
		}

		inputBytes, err := json.Marshal(inputMap)
		if err != nil {
			return Result{}, fmt.Errorf("planner: marshal node input: %w", err)
		}

		specs = append(specs, todos.NodeSpec{
			LocalID: n.ID, Type: n.Type, DependsOn: n.DependsOn, InputJSON: inputBytes,
		})
	}

	idMap, err := p.todos.CreateGraph(ctx, projectID, planID, specs)
	if err != nil {
		return Result{}, fmt.Errorf("planner: create todo graph: %w", err)
	}

	var ready []ReadyTodo
	for _, n := range nodes {
		if len(n.DependsOn) == 0 {
			ready = append(ready, ReadyTodo{ID: idMap[n.ID], Type: n.Type})
		}
	}

	return Result{PlanID: planID, Valid: true, FallbackUsed: false, ReadyTodos: ready}, nil
}
