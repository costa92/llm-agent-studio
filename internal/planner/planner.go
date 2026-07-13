package planner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"

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

// Planner turns a custom workflow graph into a persisted todo graph. The LLM
// auto-planner (Plan/PlanWith) was removed; only PlanCustom remains.
type Planner struct {
	todos *todos.Store
	db    *gorm.DB
}

// New builds a Planner.
func New(todoStore *todos.Store, db *gorm.DB) *Planner {
	return &Planner{todos: todoStore, db: db}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// nodeParamStyle 从内置节点的 Parameters（自由表单 JSON）中读取可选的 style 覆盖。
// 复用既有 Parameters 字段承载每节点风格，风格随 nodes JSONB 往返，不需数据库迁移。
// 缺省/解析失败/空串一律返回空串（= 跟随工作流，交由调用方落回 b.Style）。
func nodeParamStyle(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var p struct {
		Style string `json:"style"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Style
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
	// TypeVersion records the description.Version pinned at placement/save time.
	// The resolve layer selects the description by (resolved kind, TypeVersion);
	// an unknown TypeVersion fails closed (spec §4.3) — never a silent v1 fallback.
	TypeVersion int `json:"typeVersion,omitempty"`
	// Parameters is the serialized PropertiesForm value object: NON-dangerous
	// per-node overrides only. RegistryOnly/dangerous fields stay in the registry
	// (spec §6) and are filtered out at the resolve choke point.
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

// CustomVariable binds a template var name to an upstream node's text output.
// SourceNodeId is a workflow-LOCAL node id at plan time (lives on the node's
// VarBindings); PlanCustom rewrites it to the produced todo id (SourceTodoId)
// after CreateGraph (two-pass) and injects it into params.variables.
type CustomVariable struct {
	Name         string `json:"name"`
	SourceNodeId string `json:"sourceNodeId,omitempty"`
	SourceTodoId string `json:"sourceTodoId,omitempty"`
	// SourceField (B/P5) optional: the target field name of the upstream node's
	// output. Empty = bind the whole output (= today's behavior, accessor still
	// inferred by exprNodeAccessor as .json.text / .json). Non-empty = .json.<field>.
	// MUST match the safe-identifier charset (fieldNameRe, §8.1 injection gate);
	// candidates come from the upstream type's OutputSchema (§6).
	SourceField string `json:"sourceField,omitempty"`
}

// fieldNameRe is the §8.1 injection gate for varBinding sourceField: a safe
// identifier contains no '}'/'{'/'"'/'['/'.'/whitespace, so a user-controlled
// field name can never break out of the {{ $node["id"].json.<field> }} template
// it is concatenated into. Applied at plan time here AND at run time in the
// worker (expr_resolver.go) — defense in depth (§8.1 double validation).
var fieldNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
// createdBy 是触发本次 run 的成员 userID（成本「按人」口径）：落到 plans.created_by，
// worker 认领该 plan 的 todo 时读出、写进 generations.actor_user_id，成本中心据此按成员
// 归集。空串（无登录上下文 / m33 前的历史 run）落「未归属」桶，零回归。
func (p *Planner) PlanCustom(ctx context.Context, projectID, workflowID string, b Brief, nodes []WorkflowNode, resolved map[string]ResolvedType, runInputs json.RawMessage, createdBy string) (Result, error) {
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
			// §8.1 charset gate (plan-time, defense-in-depth). Checked INDEPENDENT of
			// the empty-SourceNodeId continue below (§12 amendment 5): a bad field is
			// rejected even on a binding with no source node, for clarity + regression
			// safety. Whitespace-only fails the regex (no TrimSpace degrade — §12 a3).
			if v.SourceField != "" && !fieldNameRe.MatchString(v.SourceField) {
				return Result{}, fmt.Errorf("planner: custom node %q variable %q invalid sourceField %q", n.ID, v.Name, v.SourceField)
			}
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
	if len(runInputs) == 0 {
		runInputs = []byte("{}") // run_inputs is NOT NULL DEFAULT '{}'
	}
	if err := p.db.WithContext(ctx).Exec(
		`INSERT INTO plans (id, project_id, workflow_id, status, raw_plan_json, valid, fallback_used, run_inputs, created_by)
		 VALUES ($1,$2,NULLIF($3,''),'created',$4,true,false,$5,$6)`,
		planID, projectID, workflowID, rawJSON, []byte(runInputs), createdBy).Error; err != nil {
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
				m := map[string]interface{}{"name": v.Name, "sourceNodeId": v.SourceNodeId}
				if v.SourceField != "" {
					m["sourceField"] = v.SourceField // B/P5: carry the field through pass 1
				}
				vars = append(vars, m)
			}
			params["variables"] = vars
			inputMap["kind"] = rt.Kind
			inputMap["params"] = params
		} else {
			// Built-in node (script/storyboard/asset): existing prompt-precedence logic.
			// 每节点风格（per-node style）：b.Style 已是 run-input/workflow.settings/project
			// 的解析结果；节点 Parameters.style 非空则就地覆盖，得到本节点的有效风格。
			// 优先级：node.Parameters.style > workflow.settings.style > project.style > 无。
			// 空/缺键 = 跟随工作流（继承 b.Style），存量/在途 plan 无此键，零回归。
			effStyle := b.Style
			if s := nodeParamStyle(n.Parameters); s != "" {
				effStyle = s
			}
			if n.Type == "script" {
				inputMap["brief"] = b.Brief
				inputMap["contentType"] = b.ContentType
				inputMap["targetPlatform"] = b.TargetPlatform
				inputMap["style"] = effStyle
			}
			// 分镜/预审节点：把已解析的 style 写进 input，让 worker 从 input 取风格
			// （而非直接读 projects 行），这样 run-input/workflow.settings 覆盖能生效。
			// 存量/在途 plan 的 input 无 style 键，worker 会落回读 projects（零回归）。
			// 分镜节点的有效风格会被 worker 扇出时逐一盖到每个资产 todo 的 input.style 上，
			// 故整条扇出继承该分镜节点的风格（worker 无需改动）。
			if n.Type == "storyboard" || n.Type == "prescreen" {
				inputMap["style"] = effStyle
			}
			// 独立资产节点（非分镜扇出）：同样注入本节点有效风格，供 worker runAsset 使用
			//（其自身 > 工作流 > 项目 > 无）。
			if n.Type == "asset" {
				inputMap["style"] = effStyle
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
			if v.SourceField != "" {
				out["sourceField"] = v.SourceField // B/P5: carry the field through pass 2 (fresh map)
			}
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
