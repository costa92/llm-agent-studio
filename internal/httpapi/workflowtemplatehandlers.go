package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	authzhttp "github.com/costa92/llm-agent-authz/httpapi"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/orgtemplate"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/workflowtemplate"
)

// OrgTemplateStore 是「组织私有工作流模板」CRUD 面（satisfied by *orgtemplate.Store）。
// 全部方法 org 隔离；nil 时只暴露内置模板、save/delete 报错。
type OrgTemplateStore interface {
	Save(ctx context.Context, orgID string, in orgtemplate.SaveInput) (orgtemplate.Template, error)
	ListByOrg(ctx context.Context, orgID string) ([]orgtemplate.Template, error)
	Get(ctx context.Context, orgID, id string) (orgtemplate.Template, error)
	Delete(ctx context.Context, orgID, id string) error
}

// listWorkflowTemplatesHandler GET /api/orgs/{org}/workflow-templates — viewer+。
// 合并内置模板（source:"builtin",deletable:false）+ org 私有模板（source:"org",
// group:"我的模板",deletable:true），内置在前。item 形状
// {id,name,description,group,source,deletable,createdBy}。ots==nil → 只回内置。
func listWorkflowTemplatesHandler(ots OrgTemplateStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tpls := workflowtemplate.Registry()
		items := make([]map[string]any, 0, len(tpls))
		for _, t := range tpls {
			items = append(items, map[string]any{
				"id":          t.ID,
				"name":        t.Name,
				"description": t.Description,
				"group":       t.Group,
				"source":      "builtin",
				"deletable":   false,
				"createdBy":   "",
			})
		}
		if ots != nil {
			orgID := r.PathValue("org")
			rows, err := ots.ListByOrg(r.Context(), orgID)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			for _, t := range rows {
				items = append(items, map[string]any{
					"id":          t.ID,
					"name":        t.Name,
					"description": t.Description,
					"group":       "我的模板",
					"source":      "org",
					"deletable":   true,
					"createdBy":   t.CreatedBy,
				})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// instantiateTemplateHandler POST /api/projects/{id}/workflows/from-template — editor+。
// 分派：templateId 命中内置注册表 → 走 provision 分支（upsert 自定义节点类型 + 装配
// planner 节点 + ValidateCustomGraph + 落库）；否则按 org 私有模板 Get（快照复制，无
// provision）落一条新工作流。跨 org / 未知 id → 404，不泄漏存在性。
func instantiateTemplateHandler(ps ProjectStore, ws WorkflowStore, cnt CustomNodeTypeStore, ots OrgTemplateStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TemplateID string `json:"templateId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request: invalid JSON body", http.StatusBadRequest)
			return
		}
		projectID := r.PathValue("id")
		p, err := ps.Get(r.Context(), projectID)
		if err != nil {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		orgID := p.OrgID

		// 内置模板：走原有 provision 分支（原样保留）。
		if tpl, ok := workflowtemplate.ByID(body.TemplateID); ok {
			provisionBuiltinTemplate(w, r, ws, cnt, projectID, orgID, tpl)
			return
		}

		// 否则按 org 私有模板实例化（快照逐字复制，无 provision）。跨 org / 未知 → 404。
		if ots == nil {
			http.Error(w, "unknown template id", http.StatusNotFound)
			return
		}
		row, err := ots.Get(r.Context(), orgID, body.TemplateID)
		if errors.Is(err, orgtemplate.ErrNotFound) {
			http.Error(w, "unknown template id", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		wf, err := ws.Create(r.Context(), projectID, row.Name, row.Nodes, row.InputsSchema, row.Settings)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, wf)
	}
}

// provisionBuiltinTemplate 据内置模板取模板 → upsert 其自定义节点类型（幂等，得
// slug→typeId）→ 装配 []planner.WorkflowNode（typed 节点补全 custom:slug/typeId/
// typeVersion；内置节点原样）→ ValidateCustomGraph → 落一条新工作流（settings=nil）。
func provisionBuiltinTemplate(w http.ResponseWriter, r *http.Request, ws WorkflowStore, cnt CustomNodeTypeStore, projectID, orgID string, tpl workflowtemplate.Template) {
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

// saveWorkflowTemplateHandler POST /api/orgs/{org}/workflow-templates — editor+。
// 把一个工作流存为组织私有模板：body {name,description,projectId,workflowId}。
// 安全：orgID 取自 path {org}，校验 project.OrgID==orgID（不符 404，不泄漏存在性）；
// 工作流按 (projectId, workflowId) 定位（跨项目 → ErrNotFound → 404）。name 空 fallback
// 工作流名。快照复制工作流的 nodes/inputsSchema/settings。ots==nil → 500。
func saveWorkflowTemplateHandler(ps ProjectStore, ws WorkflowStore, ots OrgTemplateStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ots == nil {
			http.Error(w, "template store unavailable", http.StatusInternalServerError)
			return
		}
		var body struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			ProjectID   string `json:"projectId"`
			WorkflowID  string `json:"workflowId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request: invalid JSON body", http.StatusBadRequest)
			return
		}
		if body.ProjectID == "" || body.WorkflowID == "" {
			http.Error(w, "bad request: projectId and workflowId required", http.StatusBadRequest)
			return
		}
		orgID := r.PathValue("org")
		// org 隔离守卫：项目必须属于 path 上的 org，否则一律 404（跨 org 存模板 = 不存在）。
		p, err := ps.Get(r.Context(), body.ProjectID)
		if err != nil || p.OrgID != orgID {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		wf, err := ws.Get(r.Context(), body.ProjectID, body.WorkflowID)
		if err != nil {
			http.Error(w, "workflow not found", http.StatusNotFound)
			return
		}
		name := body.Name
		if name == "" {
			name = wf.Name
		}
		t, err := ots.Save(r.Context(), orgID, orgtemplate.SaveInput{
			Name:         name,
			Description:  body.Description,
			Nodes:        wf.Nodes,
			InputsSchema: wf.InputsSchema,
			Settings:     wf.Settings,
			CreatedBy:    authzhttp.UserID(r.Context()),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":          t.ID,
			"name":        t.Name,
			"description": t.Description,
			"createdBy":   t.CreatedBy,
			"createdAt":   t.CreatedAt,
			"updatedAt":   t.UpdatedAt,
		})
	}
}

// deleteWorkflowTemplateHandler DELETE /api/orgs/{org}/workflow-templates/{id} — editor+。
// 删除组织私有模板；orgID+id from path。org 隔离：Delete(orgID,id) 跨租户/不存在 → 404。
func deleteWorkflowTemplateHandler(ots OrgTemplateStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ots == nil {
			http.Error(w, "template store unavailable", http.StatusInternalServerError)
			return
		}
		orgID := r.PathValue("org")
		id := r.PathValue("id")
		err := ots.Delete(r.Context(), orgID, id)
		if errors.Is(err, orgtemplate.ErrNotFound) {
			http.Error(w, "template not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
