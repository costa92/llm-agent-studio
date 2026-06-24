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
	// TypeId references a custom_node_types.id (org registry). Non-empty ⇒ a
	// runnable "typed" custom node; empty on a custom:* node ⇒ Phase 1 annotation
	// (non-runnable). Discriminator for HasUnboundCustomNode + run resolution.
	TypeId string `json:"typeId"`
	// VarBindings binds the template's {{name}} tokens to upstream workflow-LOCAL
	// node ids. Lives on the node instance (NOT registry params) because sourceNodeId
	// is workflow-local. PlanCustom reads THIS to inject params.variables + two-pass
	// rewrite local→todo. Empty for annotation nodes.
	VarBindings []CustomVariable `json:"varBindings"`
}

// CustomVariable binds a template var name to an upstream node's text output.
// SourceNodeId is a workflow-LOCAL node id at plan time (lives on the node's
// VarBindings); PlanCustom rewrites it to the produced todo id (SourceTodoId)
// after CreateGraph (two-pass) and injects it into params.variables.
type CustomVariable struct {
	Name         string `json:"name"`
	SourceNodeId string `json:"sourceNodeId,omitempty"`
	SourceTodoId string `json:"sourceTodoId,omitempty"`
}

// ResolvedType is the run handler's per-node registry resolution (org-scoped):
// the entry's Kind + raw Params (LlmParams: systemPrompt/userPrompt/model/
// temperature/outputFormat — NO variables; variable bindings come from the node's
// VarBindings). The handler builds map[nodeID]ResolvedType and passes it into
// PlanCustom; the planner never reads the registry (store-thin).
type ResolvedType struct {
	Kind   string
	Params json.RawMessage
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
		if !isTypeAllowed(n.Type) && !isCustomType(n.Type) {
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

// HasUnboundCustomNode reports whether any node is a custom:* node WITHOUT a
// typeId (= a Phase 1 annotation, non-runnable). Run handlers refuse such
// workflows (typed-only workflows are runnable). Discriminator = explicit typeId.
func HasUnboundCustomNode(nodes []WorkflowNode) bool {
	for _, n := range nodes {
		if isCustomType(n.Type) && n.TypeId == "" {
			return true
		}
	}
	return false
}

// PlanCustom plans a workflow defined manually by the user, bypassing the LLM
// planner. workflowID ties the run (plans row) to its first-class workflow so a
// workflow's runs/assets/timeline can be isolated; pass "" for the legacy
// project-level custom run (stored as NULL workflow_id).
// resolved is the handler-built org-scoped registry map (nodeID→ResolvedType);
// nil/empty means no typed custom nodes in this workflow.
func (p *Planner) PlanCustom(ctx context.Context, projectID, workflowID string, b Brief, nodes []WorkflowNode, resolved map[string]ResolvedType) (Result, error) {
	if err := ValidateCustomGraph(nodes); err != nil {
		return Result{}, fmt.Errorf("planner: validate custom graph: %w", err)
	}

	// Validate typed-node variable bindings: every binding's SourceNodeId MUST be an
	// upstream dependency (in DependsOn) so the data is actually produced before this
	// node runs. Reject otherwise (would read a non-existent / unordered output).
	// Bindings live on the NODE (n.VarBindings), not registry params.
	depSet := make(map[string]map[string]bool, len(nodes))
	for _, n := range nodes {
		ds := make(map[string]bool, len(n.DependsOn))
		for _, d := range n.DependsOn {
			ds[d] = true
		}
		depSet[n.ID] = ds
	}
	for _, n := range nodes {
		if _, ok := resolved[n.ID]; !ok {
			continue // not a typed node
		}
		for _, v := range n.VarBindings {
			if v.SourceNodeId == "" {
				continue
			}
			if !depSet[n.ID][v.SourceNodeId] {
				return Result{}, fmt.Errorf("planner: custom node %q variable %q sourceNodeId %q must be in dependsOn", n.ID, v.Name, v.SourceNodeId)
			}
		}
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
		if rt, ok := resolved[n.ID]; ok {
			// Typed custom node: write {kind, params} into input_json. params = the
			// registry params (NO variables) PLUS an injected variables list built from
			// the NODE's VarBindings, with LOCAL sourceNodeId here; pass 2 (after
			// CreateGraph) rewrites each to its todo id.
			var params map[string]interface{}
			if err := json.Unmarshal(rt.Params, &params); err != nil {
				return Result{}, fmt.Errorf("planner: unmarshal resolved params for %q: %w", n.ID, err)
			}
			vars := make([]map[string]interface{}, 0, len(n.VarBindings))
			for _, v := range n.VarBindings {
				vars = append(vars, map[string]interface{}{"name": v.Name, "sourceNodeId": v.SourceNodeId})
			}
			params["variables"] = vars
			inputMap["kind"] = rt.Kind
			inputMap["params"] = params
		} else {
			// Built-in node (script/storyboard/asset): existing prompt-precedence logic.
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

	// Pass 2: rewrite each typed-node binding's sourceNodeId (local) → sourceTodoId
	// (todo id from idMap) and UPDATE input_json. The local→todo map only exists now.
	// Source of truth is the NODE's VarBindings (registry params never held variables).
	for _, n := range nodes {
		rt, ok := resolved[n.ID]
		if !ok {
			continue
		}
		if len(n.VarBindings) == 0 {
			continue
		}
		var params map[string]interface{}
		if err := json.Unmarshal(rt.Params, &params); err != nil {
			return Result{}, fmt.Errorf("planner: re-parse params for %q: %w", n.ID, err)
		}
		newVars := make([]map[string]interface{}, 0, len(n.VarBindings))
		for _, v := range n.VarBindings {
			out := map[string]interface{}{"name": v.Name}
			if v.SourceNodeId != "" {
				todoID, ok := idMap[v.SourceNodeId]
				if !ok {
					return Result{}, fmt.Errorf("planner: variable %q references unknown local node %q", v.Name, v.SourceNodeId)
				}
				out["sourceTodoId"] = todoID // drop the local sourceNodeId key
			}
			newVars = append(newVars, out)
		}
		params["variables"] = newVars
		inputBytes, err := json.Marshal(map[string]interface{}{"kind": rt.Kind, "params": params})
		if err != nil {
			return Result{}, fmt.Errorf("planner: marshal rewritten input for %q: %w", n.ID, err)
		}
		if err := p.db.WithContext(ctx).Exec(
			`UPDATE todos SET input_json=$1 WHERE id=$2`, inputBytes, idMap[n.ID]).Error; err != nil {
			return Result{}, fmt.Errorf("planner: update typed node input for %q: %w", n.ID, err)
		}
	}

	var ready []ReadyTodo
	for _, n := range nodes {
		if len(n.DependsOn) == 0 {
			ready = append(ready, ReadyTodo{ID: idMap[n.ID], Type: n.Type})
		}
	}

	return Result{PlanID: planID, Valid: true, FallbackUsed: false, ReadyTodos: ready}, nil
}
