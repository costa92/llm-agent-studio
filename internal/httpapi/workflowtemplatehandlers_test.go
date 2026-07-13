package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/orgtemplate"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/workflows"
)

// recordingCNTStore 记录 Upsert 调用（用于断言实例化只 upsert 了预期的 slug）。
// 其余方法满足 CustomNodeTypeStore 接口即可。
type recordingCNTStore struct {
	upsertCalls []customnodetype.UpsertInput
}

func (s *recordingCNTStore) List(_ context.Context, _ string) ([]customnodetype.CustomNodeType, error) {
	return nil, nil
}
func (s *recordingCNTStore) Create(_ context.Context, _ string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{ID: "created", Slug: in.Slug}, nil
}
func (s *recordingCNTStore) Upsert(_ context.Context, _ string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	s.upsertCalls = append(s.upsertCalls, in)
	// typeId = "type-<slug>" 便于断言映射。
	return customnodetype.CustomNodeType{ID: "type-" + in.Slug, Slug: in.Slug, Label: in.Label, Kind: in.Kind}, nil
}
func (s *recordingCNTStore) Update(_ context.Context, id, _ string, _ customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{ID: id}, nil
}
func (s *recordingCNTStore) Delete(_ context.Context, _, _ string) error { return nil }
func (s *recordingCNTStore) Get(_ context.Context, id, _ string) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{ID: id}, nil
}

// fakeOrgTemplates 是内存 OrgTemplateStore，严格按 orgID 隔离（跨 org 读/删返回
// ErrNotFound），供 handler 的 org 隔离与快照复制断言用。
type fakeOrgTemplates struct {
	rows []orgtemplate.Template
	seq  int
}

func (f *fakeOrgTemplates) Save(_ context.Context, orgID string, in orgtemplate.SaveInput) (orgtemplate.Template, error) {
	f.seq++
	t := orgtemplate.Template{
		ID:           "tpl" + strconv.Itoa(f.seq),
		OrgID:        orgID,
		Name:         in.Name,
		Description:  in.Description,
		Nodes:        in.Nodes,
		InputsSchema: in.InputsSchema,
		Settings:     in.Settings,
		CreatedBy:    in.CreatedBy,
	}
	f.rows = append(f.rows, t)
	return t, nil
}
func (f *fakeOrgTemplates) ListByOrg(_ context.Context, orgID string) ([]orgtemplate.Template, error) {
	out := []orgtemplate.Template{}
	for _, t := range f.rows {
		if t.OrgID == orgID {
			out = append(out, t)
		}
	}
	return out, nil
}
func (f *fakeOrgTemplates) Get(_ context.Context, orgID, id string) (orgtemplate.Template, error) {
	for _, t := range f.rows {
		if t.ID == id && t.OrgID == orgID {
			return t, nil
		}
	}
	return orgtemplate.Template{}, orgtemplate.ErrNotFound
}
func (f *fakeOrgTemplates) Delete(_ context.Context, orgID, id string) error {
	for i, t := range f.rows {
		if t.ID == id && t.OrgID == orgID {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return orgtemplate.ErrNotFound
}

// TestListWorkflowTemplates 断言目录返回内置 7 项且含 standard（ots==nil 只回内置）。
func TestListWorkflowTemplates(t *testing.T) {
	h := listWorkflowTemplatesHandler(nil)
	req := httptest.NewRequest("GET", "/api/orgs/o1/workflow-templates", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Group       string `json:"group"`
			Source      string `json:"source"`
			Deletable   bool   `json:"deletable"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 7 {
		t.Fatalf("want 7 items, got %d", len(resp.Items))
	}
	found := false
	for _, it := range resp.Items {
		if it.ID == "standard" {
			found = true
			if it.Source != "builtin" || it.Deletable {
				t.Fatalf("standard must be builtin/non-deletable, got %+v", it)
			}
		}
	}
	if !found {
		t.Fatalf("items must include standard")
	}
}

// TestListWorkflowTemplates_MergeOrg 断言内置 + org 私有模板合并：内置在前，org 项
// source=org / group=我的模板 / deletable=true / 带 createdBy。
func TestListWorkflowTemplates_MergeOrg(t *testing.T) {
	ots := &fakeOrgTemplates{}
	_, _ = ots.Save(context.Background(), "o1", orgtemplate.SaveInput{Name: "我的流程", CreatedBy: "u9"})
	// 另一 org 的模板不应出现在 o1 的列表里。
	_, _ = ots.Save(context.Background(), "other", orgtemplate.SaveInput{Name: "别人的"})

	h := listWorkflowTemplatesHandler(ots)
	req := httptest.NewRequest("GET", "/api/orgs/o1/workflow-templates", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []struct {
			ID, Name, Group, Source, CreatedBy string
			Deletable                          bool
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// 7 内置 + 1 org（other org 被隔离）。
	if len(resp.Items) != 8 {
		t.Fatalf("want 8 items (7 builtin + 1 org), got %d", len(resp.Items))
	}
	// 内置在前：前 7 项全 builtin。
	for i := 0; i < 7; i++ {
		if resp.Items[i].Source != "builtin" {
			t.Fatalf("item %d should be builtin, got %+v", i, resp.Items[i])
		}
	}
	org := resp.Items[7]
	if org.Source != "org" || org.Group != "我的模板" || !org.Deletable || org.Name != "我的流程" || org.CreatedBy != "u9" {
		t.Fatalf("org item wrong: %+v", org)
	}
}

// TestInstantiateTemplate_Music 断言内置 music 走 provision：Upsert 1 次、llm 节点补全、
// script varBinding 正确、200。
func TestInstantiateTemplate_Music(t *testing.T) {
	cnt := &recordingCNTStore{}
	ws := &stubWorkflows{}
	h := instantiateTemplateHandler(stubProjects{orgID: "o1"}, ws, cnt, &fakeOrgTemplates{})

	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/from-template",
		strings.NewReader(`{"templateId":"music"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("instantiate should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(cnt.upsertCalls) != 1 {
		t.Fatalf("want 1 upsert call, got %d", len(cnt.upsertCalls))
	}
	if cnt.upsertCalls[0].Slug != "tpl-music-lyricist" {
		t.Fatalf("upsert slug = %q, want tpl-music-lyricist", cnt.upsertCalls[0].Slug)
	}
	var nodes []planner.WorkflowNode
	if err := json.Unmarshal(ws.createIn.nodes, &nodes); err != nil {
		t.Fatalf("decode created nodes: %v", err)
	}
	var llm, script *planner.WorkflowNode
	for i := range nodes {
		switch nodes[i].ID {
		case "lyrics":
			llm = &nodes[i]
		case "script-1":
			script = &nodes[i]
		}
	}
	if llm == nil || script == nil {
		t.Fatalf("expected lyrics + script-1 nodes, got %+v", nodes)
	}
	if llm.Type != "custom:tpl-music-lyricist" {
		t.Fatalf("llm node type = %q, want custom:tpl-music-lyricist", llm.Type)
	}
	if llm.TypeId == "" {
		t.Fatalf("llm node typeId must be non-empty")
	}
	if llm.TypeVersion != 1 {
		t.Fatalf("llm node typeVersion = %d, want 1", llm.TypeVersion)
	}
	if len(script.VarBindings) != 1 {
		t.Fatalf("script node want 1 varBinding, got %d", len(script.VarBindings))
	}
	vb := script.VarBindings[0]
	if vb.Name != "song" || vb.SourceNodeId != "lyrics" || vb.SourceField != "lyrics" {
		t.Fatalf("script varBinding = %+v, want {song lyrics lyrics}", vb)
	}
	if ws.createIn.name != "歌曲+封面" {
		t.Fatalf("workflow name = %q, want 歌曲+封面", ws.createIn.name)
	}
}

// TestInstantiateTemplate_OrgSnapshot 断言 org 私有模板走快照复制：nodes 与快照逐字一致、
// 无任何 custom type upsert、200。
func TestInstantiateTemplate_OrgSnapshot(t *testing.T) {
	cnt := &recordingCNTStore{}
	ws := &stubWorkflows{}
	ots := &fakeOrgTemplates{}
	snapshot := json.RawMessage(`[{"id":"n1","type":"script","dependsOn":[]}]`)
	saved, _ := ots.Save(context.Background(), "o1", orgtemplate.SaveInput{
		Name: "我的模板", Nodes: snapshot,
		InputsSchema: json.RawMessage(`[{"name":"theme"}]`),
		Settings:     json.RawMessage(`{"style":"x"}`),
	})

	h := instantiateTemplateHandler(stubProjects{orgID: "o1"}, ws, cnt, ots)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/from-template",
		strings.NewReader(`{"templateId":"`+saved.ID+`"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("org instantiate should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(cnt.upsertCalls) != 0 {
		t.Fatalf("org snapshot must not provision custom types, got %d upserts", len(cnt.upsertCalls))
	}
	if !bytes.Equal(ws.createIn.nodes, snapshot) {
		t.Fatalf("created nodes not byte-identical to snapshot: got %s want %s", ws.createIn.nodes, snapshot)
	}
	if ws.createIn.name != "我的模板" {
		t.Fatalf("workflow name = %q, want 我的模板", ws.createIn.name)
	}
}

// TestInstantiateTemplate_CrossOrg 断言拿别 org 的模板 id 实例化 → 404（不泄漏存在性）。
func TestInstantiateTemplate_CrossOrg(t *testing.T) {
	ots := &fakeOrgTemplates{}
	saved, _ := ots.Save(context.Background(), "o1", orgtemplate.SaveInput{Name: "o1 私有"})
	// 项目属于 o2；用 o1 的模板 id。
	h := instantiateTemplateHandler(stubProjects{orgID: "o2"}, &stubWorkflows{}, &recordingCNTStore{}, ots)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/from-template",
		strings.NewReader(`{"templateId":"`+saved.ID+`"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-org template should 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestInstantiateTemplate_UnknownID 断言未知模板（有 ots）→ 404。
func TestInstantiateTemplate_UnknownID(t *testing.T) {
	h := instantiateTemplateHandler(stubProjects{orgID: "o1"}, &stubWorkflows{}, &recordingCNTStore{}, &fakeOrgTemplates{})
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/from-template",
		strings.NewReader(`{"templateId":"does-not-exist"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown template should 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestInstantiateTemplate_UnknownNilOTS 断言 ots==nil + 未知 id → 404（内置未命中即无处可查）。
func TestInstantiateTemplate_UnknownNilOTS(t *testing.T) {
	h := instantiateTemplateHandler(stubProjects{orgID: "o1"}, &stubWorkflows{}, &recordingCNTStore{}, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/from-template",
		strings.NewReader(`{"templateId":"does-not-exist"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown template (nil ots) should 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestInstantiateTemplate_StandardNoUpsert 断言 standard（内置无自定义类型）不触发 Upsert
// 且能落库返回 200。
func TestInstantiateTemplate_StandardNoUpsert(t *testing.T) {
	cnt := &recordingCNTStore{}
	ws := &stubWorkflows{}
	h := instantiateTemplateHandler(stubProjects{orgID: "o1"}, ws, cnt, &fakeOrgTemplates{})
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/from-template",
		strings.NewReader(`{"templateId":"standard"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("standard instantiate should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(cnt.upsertCalls) != 0 {
		t.Fatalf("standard must not upsert any type, got %d calls", len(cnt.upsertCalls))
	}
}

// TestSaveWorkflowTemplate 断言正常存模板 → 201，且随后 list 能读回。
func TestSaveWorkflowTemplate(t *testing.T) {
	ots := &fakeOrgTemplates{}
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:         "画布工作流",
		Nodes:        json.RawMessage(`[{"id":"n1","type":"script"}]`),
		InputsSchema: json.RawMessage(`[]`),
		Settings:     json.RawMessage(`{}`),
	}}
	h := saveWorkflowTemplateHandler(stubProjects{orgID: "o1"}, ws, ots)
	body := `{"name":"存为模板","description":"desc","projectId":"p1","workflowId":"wf1"}`
	req := httptest.NewRequest("POST", "/api/orgs/o1/workflow-templates", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("save should 201, got %d: %s", rr.Code, rr.Body.String())
	}
	// list 能读回。
	rows, _ := ots.ListByOrg(context.Background(), "o1")
	if len(rows) != 1 || rows[0].Name != "存为模板" {
		t.Fatalf("saved template not readable via list: %+v", rows)
	}
	// 快照复制了工作流定义。
	if !bytes.Equal(rows[0].Nodes, json.RawMessage(`[{"id":"n1","type":"script"}]`)) {
		t.Fatalf("saved nodes = %s", rows[0].Nodes)
	}
}

// TestSaveWorkflowTemplate_NameFallback 断言 name 空时 fallback 到工作流名。
func TestSaveWorkflowTemplate_NameFallback(t *testing.T) {
	ots := &fakeOrgTemplates{}
	ws := &stubWorkflows{got: workflows.Workflow{Name: "画布工作流"}}
	h := saveWorkflowTemplateHandler(stubProjects{orgID: "o1"}, ws, ots)
	body := `{"name":"","projectId":"p1","workflowId":"wf1"}`
	req := httptest.NewRequest("POST", "/api/orgs/o1/workflow-templates", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("save should 201, got %d: %s", rr.Code, rr.Body.String())
	}
	rows, _ := ots.ListByOrg(context.Background(), "o1")
	if len(rows) != 1 || rows[0].Name != "画布工作流" {
		t.Fatalf("name fallback failed: %+v", rows)
	}
}

// TestSaveWorkflowTemplate_CrossOrgProject 断言 projectId 属于别 org → 404（不写模板）。
func TestSaveWorkflowTemplate_CrossOrgProject(t *testing.T) {
	ots := &fakeOrgTemplates{}
	// 项目属于 o2；路径 org 是 o1。
	h := saveWorkflowTemplateHandler(stubProjects{orgID: "o2"}, &stubWorkflows{}, ots)
	body := `{"name":"x","projectId":"p1","workflowId":"wf1"}`
	req := httptest.NewRequest("POST", "/api/orgs/o1/workflow-templates", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-org project should 404, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(ots.rows) != 0 {
		t.Fatalf("cross-org save must not write, got %d rows", len(ots.rows))
	}
}

// TestSaveWorkflowTemplate_NilStore 断言 ots==nil → 500。
func TestSaveWorkflowTemplate_NilStore(t *testing.T) {
	h := saveWorkflowTemplateHandler(stubProjects{orgID: "o1"}, &stubWorkflows{}, nil)
	body := `{"name":"x","projectId":"p1","workflowId":"wf1"}`
	req := httptest.NewRequest("POST", "/api/orgs/o1/workflow-templates", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("nil ots should 500, got %d", rr.Code)
	}
}

// TestDeleteWorkflowTemplate 断言正常删除 → 204。
func TestDeleteWorkflowTemplate(t *testing.T) {
	ots := &fakeOrgTemplates{}
	saved, _ := ots.Save(context.Background(), "o1", orgtemplate.SaveInput{Name: "待删"})
	h := deleteWorkflowTemplateHandler(ots)
	req := httptest.NewRequest("DELETE", "/api/orgs/o1/workflow-templates/"+saved.ID, nil)
	req.SetPathValue("org", "o1")
	req.SetPathValue("id", saved.ID)
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete should 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(ots.rows) != 0 {
		t.Fatalf("row not deleted")
	}
}

// TestDeleteWorkflowTemplate_CrossOrg 断言删别 org 的模板 → 404（且原行仍在）。
func TestDeleteWorkflowTemplate_CrossOrg(t *testing.T) {
	ots := &fakeOrgTemplates{}
	saved, _ := ots.Save(context.Background(), "o1", orgtemplate.SaveInput{Name: "o1 私有"})
	h := deleteWorkflowTemplateHandler(ots)
	// 路径 org=o2，删 o1 的 id。
	req := httptest.NewRequest("DELETE", "/api/orgs/o2/workflow-templates/"+saved.ID, nil)
	req.SetPathValue("org", "o2")
	req.SetPathValue("id", saved.ID)
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-org delete should 404, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(ots.rows) != 1 {
		t.Fatalf("cross-org delete must not remove the row")
	}
}
