package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/workflowtemplate"
)

// listWorkflowTemplatesHandler GET /api/orgs/{org}/workflow-templates — viewer+。
// 纯静态：只回模板元信息 {id,name,description,group}，不含 nodes/params。
func listWorkflowTemplatesHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tpls := workflowtemplate.Registry()
		items := make([]map[string]any, 0, len(tpls))
		for _, t := range tpls {
			items = append(items, map[string]any{
				"id":          t.ID,
				"name":        t.Name,
				"description": t.Description,
				"group":       t.Group,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// instantiateTemplateHandler POST /api/projects/{id}/workflows/from-template — editor+。
// 据 templateId 取模板 → upsert 其自定义节点类型（幂等，得 slug→typeId）→ 装配
// []planner.WorkflowNode（typed 节点补全 custom:slug/typeId/typeVersion；内置节点原样）
// → ValidateCustomGraph → 落一条新工作流（settings=nil）。
func instantiateTemplateHandler(ps ProjectStore, ws WorkflowStore, cnt CustomNodeTypeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TemplateID string `json:"templateId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request: invalid JSON body", http.StatusBadRequest)
			return
		}
		tpl, ok := workflowtemplate.ByID(body.TemplateID)
		if !ok {
			http.Error(w, "unknown template id", http.StatusBadRequest)
			return
		}
		projectID := r.PathValue("id")
		p, err := ps.Get(r.Context(), projectID)
		if err != nil {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		orgID := p.OrgID

		// fail-closed：模板含自定义节点类型但无注册表 store（focused 测试省略）→ 报错，
		// 绝不落一个引用不存在类型的工作流。
		if len(tpl.NodeTypes) > 0 && cnt == nil {
			http.Error(w, "custom node type store unavailable", http.StatusInternalServerError)
			return
		}
		// 幂等 upsert 每个节点类型，建立 slug→typeId 映射。
		slugToTypeID := make(map[string]string, len(tpl.NodeTypes))
		for _, nt := range tpl.NodeTypes {
			ct, err := cnt.Upsert(r.Context(), orgID, customnodetype.UpsertInput{
				Slug:   nt.Slug,
				Label:  nt.Label,
				Color:  nt.Color,
				Kind:   nt.Kind,
				Params: nt.Params,
			})
			if err != nil {
				http.Error(w, "instantiate template: "+err.Error(), http.StatusInternalServerError)
				return
			}
			slugToTypeID[nt.Slug] = ct.ID
		}

		// 装配 planner 节点：typed 节点补全 Type/TypeId/TypeVersion，内置节点原样透传。
		nodes := make([]planner.WorkflowNode, 0, len(tpl.Nodes))
		for _, tn := range tpl.Nodes {
			n := planner.WorkflowNode{
				ID:         tn.ID,
				DependsOn:  tn.DependsOn,
				PromptText: tn.PromptText,
				Parameters: tn.Parameters,
			}
			if tn.TypeSlug != "" {
				typeID, ok := slugToTypeID[tn.TypeSlug]
				if !ok {
					http.Error(w, "instantiate template: unresolved node type", http.StatusInternalServerError)
					return
				}
				n.Type = "custom:" + tn.TypeSlug
				n.TypeId = typeID
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
		if err := planner.ValidateCustomGraph(nodes); err != nil {
			http.Error(w, "invalid template graph: "+err.Error(), http.StatusInternalServerError)
			return
		}
		nodesJSON, err := json.Marshal(nodes)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		wf, err := ws.Create(r.Context(), projectID, tpl.Name, nodesJSON, tpl.InputsSchema, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, wf)
	}
}
