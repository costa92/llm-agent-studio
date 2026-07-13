package workflowtemplate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/runinputs"
)

// toPlannerNodes 把模板节点翻成 planner.WorkflowNode（typed 节点填假 typeId），
// 与 httpapi 的实例化 handler 同构，供图校验。
func toPlannerNodes(t Template) []planner.WorkflowNode {
	nodes := make([]planner.WorkflowNode, 0, len(t.Nodes))
	for _, tn := range t.Nodes {
		n := planner.WorkflowNode{
			ID:         tn.ID,
			DependsOn:  tn.DependsOn,
			PromptText: tn.PromptText,
			Parameters: tn.Parameters,
		}
		if tn.TypeSlug != "" {
			n.Type = "custom:" + tn.TypeSlug
			n.TypeId = "fake-" + tn.TypeSlug
			n.TypeVersion = 1
		} else {
			n.Type = tn.Type
		}
		for _, vb := range tn.VarBindings {
			n.VarBindings = append(n.VarBindings, planner.CustomVariable{
				Name:         vb.Name,
				SourceNodeId: vb.SourceNodeId,
				SourceField:  vb.SourceField,
			})
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// TestRegistryGraphsValid 钉死每个模板都能装配成合法、可运行的 DAG。
func TestRegistryGraphsValid(t *testing.T) {
	reg := Registry()
	if len(reg) != 7 {
		t.Fatalf("want 7 templates, got %d", len(reg))
	}
	if reg[0].ID != "standard" {
		t.Fatalf("standard must be first, got %q", reg[0].ID)
	}
	for _, tpl := range reg {
		nodes := toPlannerNodes(tpl)
		if err := planner.ValidateCustomGraph(nodes); err != nil {
			t.Errorf("template %q: ValidateCustomGraph: %v", tpl.ID, err)
		}
		if planner.HasUnboundCustomNode(nodes) {
			t.Errorf("template %q: has unbound custom node", tpl.ID)
		}
	}
}

// TestRegistryByID 覆盖 ByID 命中/未命中。
func TestRegistryByID(t *testing.T) {
	if _, ok := ByID("music"); !ok {
		t.Fatalf("ByID(music) should be found")
	}
	if _, ok := ByID("nope"); ok {
		t.Fatalf("ByID(nope) should not be found")
	}
}

// TestLLMParamsFidelity 钉死保真修正：每个 llm 节点类型的 Params 是合法 JSON、
// 含 systemPrompt/userPrompt，且 userPrompt 必含 {{input:theme}}（否则 theme 不注入）。
func TestLLMParamsFidelity(t *testing.T) {
	for _, tpl := range Registry() {
		for _, nt := range tpl.NodeTypes {
			if nt.Kind != "llm" {
				continue
			}
			var p struct {
				SystemPrompt string `json:"systemPrompt"`
				UserPrompt   string `json:"userPrompt"`
			}
			if err := json.Unmarshal(nt.Params, &p); err != nil {
				t.Errorf("template %q slug %q: params not valid JSON: %v", tpl.ID, nt.Slug, err)
				continue
			}
			if strings.TrimSpace(p.SystemPrompt) == "" {
				t.Errorf("template %q slug %q: empty systemPrompt", tpl.ID, nt.Slug)
			}
			if !strings.Contains(p.UserPrompt, "{{input:theme}}") {
				t.Errorf("template %q slug %q: userPrompt must contain {{input:theme}}, got %q", tpl.ID, nt.Slug, p.UserPrompt)
			}
		}
	}
}

// TestInputsSchemaValid 钉死每个模板的 InputsSchema 都能过设计期校验。
func TestInputsSchemaValid(t *testing.T) {
	for _, tpl := range Registry() {
		var fields []runinputs.Field
		if err := json.Unmarshal(tpl.InputsSchema, &fields); err != nil {
			t.Errorf("template %q: inputsSchema not valid JSON: %v", tpl.ID, err)
			continue
		}
		if err := runinputs.ValidateSchema(fields); err != nil {
			t.Errorf("template %q: ValidateSchema: %v", tpl.ID, err)
		}
	}
}

// TestVarBindingsInDependsOn 钉死每个 varBinding 的 sourceNodeId 都在该节点的
// dependsOn 内（否则 planner 会拒绝：读一个尚未产出的上游输出）。
func TestVarBindingsInDependsOn(t *testing.T) {
	for _, tpl := range Registry() {
		for _, n := range tpl.Nodes {
			deps := make(map[string]bool, len(n.DependsOn))
			for _, d := range n.DependsOn {
				deps[d] = true
			}
			for _, vb := range n.VarBindings {
				if vb.SourceNodeId == "" {
					continue
				}
				if !deps[vb.SourceNodeId] {
					t.Errorf("template %q node %q var %q: sourceNodeId %q not in dependsOn", tpl.ID, n.ID, vb.Name, vb.SourceNodeId)
				}
			}
		}
	}
}
