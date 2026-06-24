package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/costa92/llm-agent-studio/internal/customnodetype"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/projectstate"
)

// stubCNTStore implements CustomNodeTypeStore for DB-free handler tests.
type stubCNTStore struct {
	getErr    error
	deleteErr error
	updateErr error
	got       customnodetype.CustomNodeType
}

func (s *stubCNTStore) List(_ context.Context, _ string) ([]customnodetype.CustomNodeType, error) {
	return []customnodetype.CustomNodeType{}, nil
}
func (s *stubCNTStore) Create(_ context.Context, _ string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	return customnodetype.CustomNodeType{ID: "new", Label: in.Label, Kind: in.Kind, Color: in.Color, Params: in.Params}, nil
}
func (s *stubCNTStore) Update(_ context.Context, id, _ string, in customnodetype.UpsertInput) (customnodetype.CustomNodeType, error) {
	if s.updateErr != nil {
		return customnodetype.CustomNodeType{}, s.updateErr
	}
	return customnodetype.CustomNodeType{ID: id, Label: in.Label, Kind: in.Kind}, nil
}
func (s *stubCNTStore) Delete(_ context.Context, _, _ string) error {
	return s.deleteErr
}
func (s *stubCNTStore) Get(_ context.Context, id, _ string) (customnodetype.CustomNodeType, error) {
	if s.getErr != nil {
		return customnodetype.CustomNodeType{}, s.getErr
	}
	ct := s.got
	ct.ID = id
	return ct, nil
}

func TestCreateCustomNodeType_Happy(t *testing.T) {
	h := createCustomNodeTypeHandler(&stubCNTStore{})
	body := `{"label":"翻译","color":"#7c93ff","kind":"llm","params":{"userPrompt":"{{draft}}","outputFormat":"text"}}`
	req := httptest.NewRequest("POST", "/api/orgs/o1/custom-node-types", strings.NewReader(body))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var ct customnodetype.CustomNodeType
	if err := json.NewDecoder(rr.Body).Decode(&ct); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ct.ID != "new" || ct.Label != "翻译" {
		t.Fatalf("got %+v", ct)
	}
}

func TestDeleteCustomNodeType_InUse409(t *testing.T) {
	h := deleteCustomNodeTypeHandler(&stubCNTStore{deleteErr: customnodetype.ErrInUse})
	req := httptest.NewRequest("DELETE", "/api/orgs/o1/custom-node-types/ct1", nil)
	req.SetPathValue("org", "o1")
	req.SetPathValue("id", "ct1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("in-use delete should 409, got %d", rr.Code)
	}
}

func TestDeleteCustomNodeType_NotFound404(t *testing.T) {
	h := deleteCustomNodeTypeHandler(&stubCNTStore{deleteErr: customnodetype.ErrNotFound})
	req := httptest.NewRequest("DELETE", "/api/orgs/o1/custom-node-types/ct1", nil)
	req.SetPathValue("org", "o1")
	req.SetPathValue("id", "ct1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("not-found delete should 404, got %d", rr.Code)
	}
}

func TestUpdateCustomNodeType_CrossOrg404(t *testing.T) {
	h := updateCustomNodeTypeHandler(&stubCNTStore{updateErr: customnodetype.ErrNotFound})
	body := `{"label":"x","color":"","kind":"llm","params":{"userPrompt":"hi","outputFormat":"text"}}`
	req := httptest.NewRequest("PUT", "/api/orgs/o2/custom-node-types/ct1", strings.NewReader(body))
	req.SetPathValue("org", "o2")
	req.SetPathValue("id", "ct1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-org update should 404, got %d", rr.Code)
	}
}

// capturingPlanner records the resolved map passed to PlanCustom.
type capturingPlanner struct {
	gotResolved map[string]planner.ResolvedType
}

func (c *capturingPlanner) Plan(_ context.Context, _ string, _ planner.Brief) (planner.Result, error) {
	return planner.Result{PlanID: "pl", Valid: true}, nil
}
func (c *capturingPlanner) PlanWith(_ context.Context, _ string, _ llm.ChatModel, _ planner.Brief) (planner.Result, error) {
	return planner.Result{PlanID: "pl", Valid: true}, nil
}
func (c *capturingPlanner) PlanCustom(_ context.Context, _, _ string, _ planner.Brief, _ []planner.WorkflowNode, resolved map[string]planner.ResolvedType) (planner.Result, error) {
	c.gotResolved = resolved
	return planner.Result{PlanID: "pl", Valid: true, ReadyTodos: []planner.ReadyTodo{{ID: "t1", Type: "custom:llm"}}}, nil
}

// cntProjectStore is a ProjectStore that returns a project whose WorkflowNodes
// contain a custom:* node with a given typeId (for run-gate tests).
type cntProjectStore struct {
	p project.Project
}

func (s cntProjectStore) Create(_ context.Context, _ project.CreateInput) (project.Project, error) {
	return project.Project{}, nil
}
func (s cntProjectStore) Get(_ context.Context, _ string) (project.Project, error) { return s.p, nil }
func (s cntProjectStore) ListByOrg(_ context.Context, _ string, _ int, _ string) ([]project.Project, string, error) {
	return nil, "", nil
}
func (s cntProjectStore) Update(_ context.Context, _ string, _ project.UpdateInput) (project.Project, error) {
	return project.Project{}, nil
}
func (s cntProjectStore) SetStatus(_ context.Context, _, _ string) error  { return nil }
func (s cntProjectStore) SetCover(_ context.Context, _, _ string) error   { return nil }
func (s cntProjectStore) Cancel(_ context.Context, _ string) error        { return nil }
func (s cntProjectStore) OrgIDForProject(_ context.Context, _ string) (string, error) {
	return s.p.OrgID, nil
}
func (s cntProjectStore) ListPlans(_ context.Context, _ string) ([]project.Plan, error) {
	return nil, nil
}
func (s cntProjectStore) LoadState(_ context.Context, _, _ string) (projectstate.ProjectState, error) {
	return projectstate.ProjectState{}, nil
}

// TestRunHandler_AnnotationCustomNode400 verifies annotation custom nodes (no typeId) → 400.
func TestRunHandler_AnnotationCustomNode400(t *testing.T) {
	nodes, _ := json.Marshal([]planner.WorkflowNode{
		{ID: "s1", Type: "script"},
		{ID: "c1", Type: "custom:translate", DependsOn: []string{"s1"}},
	})
	ps := cntProjectStore{p: project.Project{
		ID:                    "p1",
		OrgID:                 "o1",
		Status:                "draft",
		CustomWorkflowEnabled: true,
		WorkflowNodes:         json.RawMessage(nodes),
	}}
	h := runHandler(ps, &capturingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100, nil, &stubCNTStore{})
	req := httptest.NewRequest("POST", "/api/projects/p1/run", nil)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("annotation custom node should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "未绑定类型") {
		t.Fatalf("body should mention 未绑定类型, got: %s", rr.Body.String())
	}
}

// TestRunHandler_TypedCustomNode202 verifies typed custom nodes (with typeId) are resolved
// and PlanCustom is called with a non-nil resolved map entry.
func TestRunHandler_TypedCustomNode202(t *testing.T) {
	params, _ := json.Marshal(map[string]any{"userPrompt": "{{draft}}", "outputFormat": "text"})
	nodes, _ := json.Marshal([]planner.WorkflowNode{
		{ID: "s1", Type: "script"},
		{ID: "c1", Type: "custom:llm", TypeId: "reg-1", DependsOn: []string{"s1"}},
	})
	ps := cntProjectStore{p: project.Project{
		ID:                    "p1",
		OrgID:                 "o1",
		Status:                "draft",
		CustomWorkflowEnabled: true,
		WorkflowNodes:         json.RawMessage(nodes),
	}}
	cnt := &stubCNTStore{got: customnodetype.CustomNodeType{
		ID:     "reg-1",
		Kind:   "llm",
		Params: params,
	}}
	cp := &capturingPlanner{}
	h := runHandler(ps, cp, stubAppender{}, &stubCost{count: 0}, 100, nil, cnt)
	req := httptest.NewRequest("POST", "/api/projects/p1/run", nil)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("typed custom node should 202, got %d: %s", rr.Code, rr.Body.String())
	}
	if rt, ok := cp.gotResolved["c1"]; !ok || rt.Kind != "llm" {
		t.Fatalf("PlanCustom resolved[c1] = %+v (ok=%v), want kind=llm", rt, ok)
	}
}

// Suppress unused import errors by ensuring errors is used.
var _ = errors.New
