package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/workflows"
)

// stubWorkflows is a configurable WorkflowStore for handler tests.
type stubWorkflows struct {
	getErr   error
	got      workflows.Workflow
	created  workflows.Workflow
	createIn struct {
		projectID, name string
		nodes           json.RawMessage
	}
}

func (s *stubWorkflows) Create(_ context.Context, projectID, name string, nodes json.RawMessage) (workflows.Workflow, error) {
	s.createIn.projectID, s.createIn.name, s.createIn.nodes = projectID, name, nodes
	return workflows.Workflow{ID: "wf1", ProjectID: projectID, Name: name, Nodes: nodes}, nil
}
func (s *stubWorkflows) Get(_ context.Context, _, id string) (workflows.Workflow, error) {
	if s.getErr != nil {
		return workflows.Workflow{}, s.getErr
	}
	w := s.got
	w.ID = id
	return w, nil
}
func (s *stubWorkflows) ListByProject(_ context.Context, _ string) ([]workflows.Workflow, error) {
	return []workflows.Workflow{{ID: "wf1", Name: "a"}}, nil
}
func (s *stubWorkflows) Update(_ context.Context, projectID, id, name string, nodes json.RawMessage) (workflows.Workflow, error) {
	return workflows.Workflow{ID: id, ProjectID: projectID, Name: name, Nodes: nodes}, nil
}
func (s *stubWorkflows) Delete(_ context.Context, _, _ string) error { return nil }

// recordingPlanner captures the workflowID passed to PlanCustom.
type recordingPlanner struct{ gotWorkflowID string }

func (recordingPlanner) Plan(_ context.Context, _ string, _ planner.Brief) (planner.Result, error) {
	return planner.Result{PlanID: "pl"}, nil
}
func (recordingPlanner) PlanWith(_ context.Context, _ string, _ llm.ChatModel, _ planner.Brief) (planner.Result, error) {
	return planner.Result{PlanID: "pl"}, nil
}

func TestCreateWorkflowHandlerRejectsEmptyName(t *testing.T) {
	h := createWorkflowHandler(&stubWorkflows{})
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(`{"name":""}`))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty name should 400, got %d", rr.Code)
	}
}

func TestCreateWorkflowHandlerHappy(t *testing.T) {
	ws := &stubWorkflows{}
	h := createWorkflowHandler(ws)
	body := `{"name":"工作流 A","nodes":[{"id":"n1","type":"script","promptId":"","dependsOn":[]}]}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("create should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ws.createIn.projectID != "p1" || ws.createIn.name != "工作流 A" {
		t.Fatalf("create args mismatch: %+v", ws.createIn)
	}
}

func TestRunWorkflowHandlerNotFound(t *testing.T) {
	ws := &stubWorkflows{getErr: workflows.ErrNotFound}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/missing/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "missing")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing workflow run should 404, got %d", rr.Code)
	}
}

func TestRunWorkflowHandlerPassesWorkflowID(t *testing.T) {
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:  "wf",
		Nodes: json.RawMessage(`[{"id":"n1","type":"script","promptId":"","dependsOn":[]}]`),
	}}
	rp := &recordingPlanner{}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, rp, stubAppender{}, &stubCost{count: 0}, 100)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("run should 202, got %d: %s", rr.Code, rr.Body.String())
	}
	if rp.gotWorkflowID != "wfX" {
		t.Fatalf("PlanCustom got workflowID %q, want wfX", rp.gotWorkflowID)
	}
}

func TestRunWorkflowHandlerEmptyNodes(t *testing.T) {
	ws := &stubWorkflows{got: workflows.Workflow{Name: "wf", Nodes: json.RawMessage(`[]`)}}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty-nodes run should 400, got %d", rr.Code)
	}
}

func (rp *recordingPlanner) PlanCustom(_ context.Context, _, workflowID string, _ planner.Brief, _ []planner.WorkflowNode) (planner.Result, error) {
	rp.gotWorkflowID = workflowID
	return planner.Result{PlanID: "pl", Valid: true, ReadyTodos: []planner.ReadyTodo{{ID: "t1", Type: "script"}}}, nil
}
