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
	"github.com/costa92/llm-agent-studio/internal/project"
)

// branchPlanner captures which Plan* method ran plus the runInputs snapshot it
// received, so run-handler branch behavior can be asserted without a DB.
type branchPlanner struct {
	method    string
	runInputs json.RawMessage
	brief     planner.Brief
}

func (r *branchPlanner) Plan(_ context.Context, _ string, b planner.Brief, ri json.RawMessage) (planner.Result, error) {
	r.method, r.brief, r.runInputs = "Plan", b, ri
	return planner.Result{PlanID: "pl", Valid: true}, nil
}
func (r *branchPlanner) PlanWith(_ context.Context, _ string, _ llm.ChatModel, b planner.Brief, ri json.RawMessage) (planner.Result, error) {
	r.method, r.brief, r.runInputs = "PlanWith", b, ri
	return planner.Result{PlanID: "pl", Valid: true}, nil
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

const pbBaseConfig = `{"ageBand":"3-6","bookType":"narrative","illustrationStyle":"watercolor","narrationStyle":"plain","voice":"warm","themes":["friendship"],"pageCount":16}`

func pbProject() project.Project {
	return project.Project{ID: "p1", OrgID: "o1", Status: "draft", Kind: "picturebook", PictureBookConfig: pbBaseConfig}
}

// 绘本带合法 inputs（改 ageBand）→ 202，run_inputs 落 pbConfig 项，schema 快照随带。
func TestRunHandler_PictureBookInputs_LandRunInputs(t *testing.T) {
	rp, rr := runReq(t, pbProject(), `{"inputs":{"ageBand":"6-8"}}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", rr.Code, rr.Body.String())
	}
	if rp.method != "Plan" {
		t.Fatalf("picturebook should route to Plan/PlanWith, got %s", rp.method)
	}
	var ri struct {
		Values map[string]json.RawMessage `json:"values"`
		Schema []map[string]any           `json:"schema"`
	}
	if err := json.Unmarshal(rp.runInputs, &ri); err != nil {
		t.Fatalf("run_inputs snapshot not parseable: %v (%s)", err, rp.runInputs)
	}
	if string(ri.Values["ageBand"]) != `"6-8"` {
		t.Fatalf("values.ageBand=%s want \"6-8\"", ri.Values["ageBand"])
	}
	var sawPB bool
	for _, f := range ri.Schema {
		if f["target"] == "pbConfig" {
			sawPB = true
		}
	}
	if !sawPB {
		t.Fatalf("schema snapshot should carry pbConfig fields: %s", rp.runInputs)
	}
}

// 标准项目带 inputs → 400（无 schema 来源）。
func TestRunHandler_StandardInputs_400(t *testing.T) {
	p := project.Project{ID: "p1", OrgID: "o1", Status: "draft", Kind: "standard"}
	_, rr := runReq(t, p, `{"inputs":{"x":"y"}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("standard project + inputs should 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// 标准项目空 body → 202（零回归）。
func TestRunHandler_StandardNoInputs_OK(t *testing.T) {
	p := project.Project{ID: "p1", OrgID: "o1", Status: "draft", Kind: "standard"}
	rp, rr := runReq(t, p, "")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("standard empty run should 202, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(rp.runInputs) != 0 {
		t.Fatalf("standard run should carry nil run_inputs, got %s", rp.runInputs)
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

// 绘本 PB override 枚举越界（theme 不在 options）→ 400。
func TestRunHandler_PictureBookEnumViolation_400(t *testing.T) {
	_, rr := runReq(t, pbProject(), `{"inputs":{"themes":["not-a-real-theme"]}}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("out-of-enum theme should 400, got %d: %s", rr.Code, rr.Body.String())
	}
}
