package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
)

// branchPlanner captures which Plan* method ran plus the runInputs snapshot it
// received, so run-handler branch behavior can be asserted without a DB.
type branchPlanner struct {
	method    string
	runInputs json.RawMessage
	brief     planner.Brief
}

func (r *branchPlanner) PlanCustom(_ context.Context, _, _ string, b planner.Brief, _ []planner.WorkflowNode, _ map[string]planner.ResolvedType, ri json.RawMessage) (planner.Result, error) {
	r.method, r.brief, r.runInputs = "PlanCustom", b, ri
	return planner.Result{PlanID: "pl", Valid: true}, nil
}

func runReq(t *testing.T, p project.Project, body string) (*branchPlanner, *httptest.ResponseRecorder) {
	t.Helper()
	rp := &branchPlanner{}
	ps := fixedProjectStore{p: p}
	h := runHandler(ps, rp, stubAppender{}, &stubCost{count: 0}, 100, nil, nil)
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest("POST", "/api/projects/p1/run", rdr)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	return rp, rr
}

// 非自定义项目（无 custom_workflow_enabled）→ 400：绘本/标准管线已移除，项目级
// /run 仅服务自定义工作流。
func TestRunHandler_NonCustomProject_400(t *testing.T) {
	p := project.Project{ID: "p1", OrgID: "o1", Status: "draft", Kind: "custom"}
	_, rr := runReq(t, p, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("non-custom project should 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// legacy 自定义（CustomWorkflowEnabled）带 inputs → 忽略不报错，PlanCustom 收 nil。
func TestRunHandler_LegacyCustomInputs_Ignored(t *testing.T) {
	nodes, _ := json.Marshal([]planner.WorkflowNode{{ID: "s1", Type: "script"}})
	p := project.Project{
		ID: "p1", OrgID: "o1", Status: "draft",
		CustomWorkflowEnabled: true, WorkflowNodes: json.RawMessage(nodes),
	}
	rp, rr := runReq(t, p, `{"inputs":{"x":"y"}}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("legacy custom + inputs should 202 (ignored), got %d: %s", rr.Code, rr.Body.String())
	}
	if rp.method != "PlanCustom" {
		t.Fatalf("legacy custom should route to PlanCustom, got %s", rp.method)
	}
	if len(rp.runInputs) != 0 {
		t.Fatalf("legacy custom must drop inputs (nil run_inputs), got %s", rp.runInputs)
	}
}
