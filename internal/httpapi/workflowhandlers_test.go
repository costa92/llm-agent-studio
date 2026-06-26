package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-contract/llm"
	"github.com/costa92/llm-agent-studio/internal/customnodetype"
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
	h := createWorkflowHandler(stubProjects{orgID: "o1"}, &stubWorkflows{}, nil)
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
	h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
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
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100, nil)
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
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, rp, stubAppender{}, &stubCost{count: 0}, 100, nil)
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
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfX/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfX")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty-nodes run should 400, got %d", rr.Code)
	}
}

// TestCreateWorkflow_RejectsCycle verifies that save-time validation rejects a
// cyclic graph with 400 and does NOT call WorkflowStore.Create.
func TestCreateWorkflow_RejectsCycle(t *testing.T) {
	// ws.Create will fail the test if called — cycle must be caught before the store.
	ws := &cycleRejectingWorkflows{t: t}
	h := createWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
	// A↔B cycle.
	body := `{"name":"cycle-wf","nodes":[{"id":"A","type":"script","dependsOn":["B"]},{"id":"B","type":"storyboard","dependsOn":["A"]}]}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("cyclic workflow should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cycle") {
		t.Fatalf("body should mention \"cycle\", got: %s", rr.Body.String())
	}
}

func TestUpdateWorkflow_RejectsCycle(t *testing.T) {
	// ws.Update will fail the test if called — cycle must be caught before the store.
	ws := &cycleRejectingWorkflows{t: t}
	h := updateWorkflowHandler(stubProjects{orgID: "o1"}, ws, nil)
	body := `{"name":"cycle-wf","nodes":[{"id":"A","type":"script","dependsOn":["B"]},{"id":"B","type":"storyboard","dependsOn":["A"]}]}`
	req := httptest.NewRequest("PUT", "/api/projects/p1/workflows/w1", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "w1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("cyclic workflow should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cycle") {
		t.Fatalf("body should mention \"cycle\", got: %s", rr.Body.String())
	}
}

// cycleRejectingWorkflows is a WorkflowStore stub whose Create fails the test
// (to prove validation happens BEFORE the store write).
type cycleRejectingWorkflows struct {
	t *testing.T
}

func (s *cycleRejectingWorkflows) Create(_ context.Context, _, _ string, _ json.RawMessage) (workflows.Workflow, error) {
	s.t.Fatal("Create must not be called when the graph is invalid")
	return workflows.Workflow{}, nil
}
func (s *cycleRejectingWorkflows) Get(_ context.Context, _, id string) (workflows.Workflow, error) {
	return workflows.Workflow{ID: id}, nil
}
func (s *cycleRejectingWorkflows) ListByProject(_ context.Context, _ string) ([]workflows.Workflow, error) {
	return nil, nil
}
func (s *cycleRejectingWorkflows) Update(_ context.Context, _, id, name string, nodes json.RawMessage) (workflows.Workflow, error) {
	s.t.Fatal("Update must not be called when the graph is invalid")
	return workflows.Workflow{}, nil
}
func (s *cycleRejectingWorkflows) Delete(_ context.Context, _, _ string) error { return nil }

// trackingAppender records how many times Append was called so tests can assert
// "no planner_started was emitted".
type trackingAppender struct{ count int }

func (a *trackingAppender) Append(_ context.Context, _, _, _ string, _ any) (int64, error) {
	a.count++
	return int64(a.count), nil
}

// TestRunWorkflow_CyclicReturns400 verifies that running a workflow with a
// cyclic graph returns 400 (not 500) and does NOT emit planner_started.
func TestRunWorkflow_CyclicReturns400(t *testing.T) {
	// WorkflowStore returns a workflow whose nodes form a cycle.
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:  "cyclic-wf",
		Nodes: json.RawMessage(`[{"id":"A","type":"script","dependsOn":["B"]},{"id":"B","type":"storyboard","dependsOn":["A"]}]`),
	}}
	ev := &trackingAppender{}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, ev, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfCycle/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfCycle")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("cyclic run should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if ev.count != 0 {
		t.Fatalf("planner_started must not be emitted for cyclic run (got %d Append calls)", ev.count)
	}
}

func TestRunWorkflowHandlerRefusesCustomNodes(t *testing.T) {
	nodes, _ := json.Marshal([]planner.WorkflowNode{
		{ID: "s1", Type: "script"},
		{ID: "c1", Type: "custom:translate", DependsOn: []string{"s1"}},
	})
	ws := &stubWorkflows{got: workflows.Workflow{
		Name:  "wf-custom",
		Nodes: json.RawMessage(nodes),
	}}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, &recordingPlanner{}, stubAppender{}, &stubCost{count: 0}, 100, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wfC/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wfC")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("custom-node run should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "未绑定类型") {
		t.Fatalf("body should contain \"未绑定类型\", got: %s", rr.Body.String())
	}
}

func (rp *recordingPlanner) PlanCustom(_ context.Context, _, workflowID string, _ planner.Brief, _ []planner.WorkflowNode, _ map[string]planner.ResolvedType) (planner.Result, error) {
	rp.gotWorkflowID = workflowID
	return planner.Result{PlanID: "pl", Valid: true, ReadyTodos: []planner.ReadyTodo{{ID: "t1", Type: "script"}}}, nil
}

// TestCreateWorkflowRejectsRegistryOnlyOverlay (W1 create) [DB]: a node overlay
// that smuggles a RegistryOnly field (http url retargeted + allowResponseBody:true)
// must be rejected with 400 at SAVE, before the store write.
func TestCreateWorkflowRejectsRegistryOnlyOverlay(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "fetch", Kind: "http", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ws := &cycleRejectingWorkflows{t: t} // Create must NOT be called
	h := createWorkflowHandler(stubProjects{orgID: org}, ws, store)
	body := `{"name":"wf1","nodes":[{"id":"n1","type":"custom:fetch","typeId":"` + ct.ID + `","dependsOn":[],"typeVersion":1,"parameters":{"url":"http://attacker/collect","allowResponseBody":true}}]}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("RegistryOnly overlay must be rejected at create, got %d: %s", rr.Code, rr.Body.String())
	}
	// Opaque: the rejected attacker URL must NOT leak into the response body.
	if strings.Contains(rr.Body.String(), "attacker") {
		t.Fatalf("error body leaked attacker payload: %s", rr.Body.String())
	}
}

// TestUpdateWorkflowRejectsRegistryOnlyOverlay (W1 update) [DB].
func TestUpdateWorkflowRejectsRegistryOnlyOverlay(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"code": "print(1)", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "code", Kind: "script", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ws := &cycleRejectingWorkflows{t: t} // Update must NOT be called
	h := updateWorkflowHandler(stubProjects{orgID: org}, ws, store)
	body := `{"name":"wf1","nodes":[{"id":"n1","type":"custom:code","typeId":"` + ct.ID + `","dependsOn":[],"typeVersion":1,"parameters":{"code":"x = {{secret:K}}"}}]}`
	req := httptest.NewRequest("PUT", "/api/projects/p1/workflows/w1", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "w1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("RegistryOnly overlay must be rejected at update, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestCreateWorkflowAcceptsCleanOverlay (W1 create) [DB]: a non-dangerous overlay
// (outputFormat=json) merges cleanly and the save proceeds to the store.
func TestCreateWorkflowAcceptsCleanOverlay(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"method": "GET", "url": "https://api.example.com", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "fetch", Kind: "http", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ws := &stubWorkflows{}
	h := createWorkflowHandler(stubProjects{orgID: org}, ws, store)
	body := `{"name":"wf1","nodes":[{"id":"n1","type":"custom:fetch","typeId":"` + ct.ID + `","dependsOn":[],"typeVersion":1,"parameters":{"outputFormat":"json"}}]}`
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows", strings.NewReader(body))
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clean overlay should 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
