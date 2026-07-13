package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/planner"
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

// TestListWorkflowTemplates 断言目录返回 7 项且含 standard。
func TestListWorkflowTemplates(t *testing.T) {
	h := listWorkflowTemplatesHandler()
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
		}
	}
	if !found {
		t.Fatalf("items must include standard")
	}
}

// TestInstantiateTemplate_Music 断言 music 实例化：Upsert 调 1 次(slug=tpl-music-lyricist)、
// Create 收到的 nodes 里 llm 节点 type=custom:tpl-music-lyricist & typeId!="" & typeVersion==1、
// script 节点带正确 varBinding、返回 200。
func TestInstantiateTemplate_Music(t *testing.T) {
	cnt := &recordingCNTStore{}
	ws := &stubWorkflows{}
	h := instantiateTemplateHandler(stubProjects{orgID: "o1"}, ws, cnt)

	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/from-template",
		strings.NewReader(`{"templateId":"music"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("instantiate should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// Upsert 恰好 1 次，slug 正确。
	if len(cnt.upsertCalls) != 1 {
		t.Fatalf("want 1 upsert call, got %d", len(cnt.upsertCalls))
	}
	if cnt.upsertCalls[0].Slug != "tpl-music-lyricist" {
		t.Fatalf("upsert slug = %q, want tpl-music-lyricist", cnt.upsertCalls[0].Slug)
	}
	// Create 收到的 nodes 解回 planner 形状做断言。
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
	// 落库的工作流名 = 模板名。
	if ws.createIn.name != "歌曲+封面" {
		t.Fatalf("workflow name = %q, want 歌曲+封面", ws.createIn.name)
	}
}

// TestInstantiateTemplate_UnknownID 断言未知模板 → 400。
func TestInstantiateTemplate_UnknownID(t *testing.T) {
	h := instantiateTemplateHandler(stubProjects{orgID: "o1"}, &stubWorkflows{}, &recordingCNTStore{})
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/from-template",
		strings.NewReader(`{"templateId":"does-not-exist"}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown template should 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestInstantiateTemplate_StandardNoUpsert 断言 standard（无自定义类型）不触发 Upsert
// 且能落库返回 200。
func TestInstantiateTemplate_StandardNoUpsert(t *testing.T) {
	cnt := &recordingCNTStore{}
	ws := &stubWorkflows{}
	h := instantiateTemplateHandler(stubProjects{orgID: "o1"}, ws, cnt)
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
