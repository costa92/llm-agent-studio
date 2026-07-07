package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-contract/llm"

	"github.com/costa92/llm-agent-studio/internal/cost"
	"github.com/costa92/llm-agent-studio/internal/models"
	"github.com/costa92/llm-agent-studio/internal/planner"
	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/projectstate"
	"github.com/costa92/llm-agent-studio/internal/workflows"
)

// stubCost is a canned CostStore for handler tests.
type stubCost struct {
	agg        cost.Aggregate
	per        []cost.ProjectAggregate
	recent     []cost.LedgerEntry
	recentNext string
	recentErr  error
	count      int
	recorded   []cost.Generation
	planCost   cost.PlanCost

	gotFrom, gotTo          time.Time
	gotCursor               string
	gotProjectID, gotPlanID string
}

func (s *stubCost) Record(_ context.Context, g cost.Generation) error {
	s.recorded = append(s.recorded, g)
	return nil
}

func (s *stubCost) ByOrgBetween(_ context.Context, _ string, from, to time.Time) (cost.Aggregate, error) {
	s.gotFrom, s.gotTo = from, to
	return s.agg, nil
}
func (s *stubCost) ByProjectBetween(_ context.Context, _ string, from, to time.Time) (cost.Aggregate, error) {
	s.gotFrom, s.gotTo = from, to
	return s.agg, nil
}
func (s *stubCost) PerProjectByOrg(_ context.Context, _ string, _, _ time.Time) ([]cost.ProjectAggregate, error) {
	return s.per, nil
}
func (s *stubCost) RecentByOrg(_ context.Context, _ string, _ int, cursor string) ([]cost.LedgerEntry, string, error) {
	s.gotCursor = cursor
	return s.recent, s.recentNext, s.recentErr
}
func (s *stubCost) CountByOrgSince(_ context.Context, _ string, _ time.Time) (int, error) {
	return s.count, nil
}
func (s *stubCost) ByPlan(_ context.Context, projectID, planID string) (cost.PlanCost, error) {
	s.gotProjectID, s.gotPlanID = projectID, planID
	return s.planCost, nil
}

func TestPlanCostHandler(t *testing.T) {
	cs := &stubCost{planCost: cost.PlanCost{
		Aggregate:  cost.Aggregate{Generations: 3, Tokens: 900, ImageCount: 2, CostMicros: 11600},
		KindCounts: map[string]int{"chat": 1, "image": 2},
		Todos: []cost.TodoCost{{
			TodoID: "t1", TodoType: "script", Kind: "chat", Provider: "openai", Model: "gpt",
			Aggregate: cost.Aggregate{Generations: 1, Tokens: 800, CostMicros: 1600},
		}},
	}}
	h := planCostHandler(cs)
	req := httptest.NewRequest("GET", "/api/projects/p1/plans/plan-1/cost", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("planId", "plan-1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rr.Code, rr.Body.String())
	}
	if cs.gotProjectID != "p1" || cs.gotPlanID != "plan-1" {
		t.Fatalf("path values not forwarded: %q %q", cs.gotProjectID, cs.gotPlanID)
	}
	body := rr.Body.String()
	for _, want := range []string{`"costMicros":11600`, `"kindCounts":{"chat":1,"image":2}`, `"todoId":"t1"`, `"todoType":"script"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

// TestPlanCostHandlerEmptyRun: run 无账本行 → 零总计 + 空数组（非 null），前端空态依赖它。
func TestPlanCostHandlerEmptyRun(t *testing.T) {
	cs := &stubCost{planCost: cost.PlanCost{KindCounts: map[string]int{}, Todos: []cost.TodoCost{}}}
	h := planCostHandler(cs)
	req := httptest.NewRequest("GET", "/api/projects/p1/plans/plan-x/cost", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("planId", "plan-x")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"todos":[]`) {
		t.Fatalf("empty run must serialize todos as []: %s", rr.Body.String())
	}
}

func TestOrgCostHandlerParsesRange(t *testing.T) {
	cs := &stubCost{agg: cost.Aggregate{Generations: 3, CostMicros: 900}}
	h := orgCostHandler(cs)
	req := httptest.NewRequest("GET", "/api/orgs/o1/cost?from=2026-06-01T00:00:00Z&to=2026-06-02T00:00:00Z", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rr.Code, rr.Body.String())
	}
	if cs.gotFrom.IsZero() || cs.gotTo.IsZero() {
		t.Fatalf("from/to not forwarded: %v %v", cs.gotFrom, cs.gotTo)
	}
	var agg cost.Aggregate
	_ = json.Unmarshal(rr.Body.Bytes(), &agg)
	if agg.Generations != 3 || agg.CostMicros != 900 {
		t.Fatalf("agg = %+v", agg)
	}
}

func TestOrgCostHandlerRejectsBadRange(t *testing.T) {
	h := orgCostHandler(&stubCost{})
	req := httptest.NewRequest("GET", "/api/orgs/o1/cost?from=yesterday", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed from should 400, got %d", rr.Code)
	}
}

func TestOrgCostProjectsHandler(t *testing.T) {
	cs := &stubCost{per: []cost.ProjectAggregate{{ProjectID: "p1", ProjectName: "Promo"}}}
	h := orgCostProjectsHandler(cs)
	req := httptest.NewRequest("GET", "/api/orgs/o1/cost/projects", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"projectId":"p1"`) {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestOrgGenerationsHandler(t *testing.T) {
	cs := &stubCost{recent: []cost.LedgerEntry{{ID: "g1", Provider: "openai"}}, recentNext: "cur-next"}
	h := orgGenerationsHandler(cs)
	req := httptest.NewRequest("GET", "/api/orgs/o1/generations?limit=5&cursor=cur-1", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"id":"g1"`) {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	if cs.gotCursor != "cur-1" {
		t.Fatalf("cursor not forwarded: %q", cs.gotCursor)
	}
	if !strings.Contains(rr.Body.String(), `"next_cursor":"cur-next"`) {
		t.Fatalf("next_cursor missing: %s", rr.Body.String())
	}
}

func TestOrgGenerationsHandlerRejectsBadCursor(t *testing.T) {
	h := orgGenerationsHandler(&stubCost{recentErr: cost.ErrBadCursor})
	req := httptest.NewRequest("GET", "/api/orgs/o1/generations?cursor=garbage", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor should 400, got %d", rr.Code)
	}
}

// --- quota stubs ---

type stubProjects struct {
	orgID string
	// runBusy=true 模拟项目已有在途 run（TryBeginRun 返 false → 运行入口 409）。
	runBusy bool
}

func (s stubProjects) Create(_ context.Context, _ project.CreateInput) (project.Project, error) {
	return project.Project{}, nil
}
func (s stubProjects) Get(_ context.Context, id string) (project.Project, error) {
	return project.Project{ID: id, OrgID: s.orgID, Status: "draft"}, nil
}
func (s stubProjects) ListByOrg(_ context.Context, _ string, _ int, _ string) ([]project.Project, string, error) {
	return nil, "", nil
}
func (s stubProjects) Update(_ context.Context, _ string, _ project.UpdateInput) (project.Project, error) {
	return project.Project{}, nil
}
func (s stubProjects) SetStatus(_ context.Context, _, _ string) error  { return nil }
func (s stubProjects) TryBeginRun(_ context.Context, _ string) (bool, error) {
	return !s.runBusy, nil
}
func (s stubProjects) SetCover(_ context.Context, _, _ string) error   { return nil }
func (s stubProjects) Cancel(_ context.Context, _ string) error        { return nil }
func (s stubProjects) Deleted(_ context.Context, _ string) (bool, error) { return false, nil }
func (s stubProjects) SoftDelete(_ context.Context, _ string) error      { return nil }
func (s stubProjects) OrgIDForProject(_ context.Context, _ string) (string, error) {
	return s.orgID, nil
}
func (s stubProjects) ListPlans(_ context.Context, _ string) ([]project.Plan, error) {
	return nil, nil
}
func (s stubProjects) LoadState(_ context.Context, _, _ string) (projectstate.ProjectState, error) {
	return projectstate.ProjectState{}, nil
}

type stubPlanner struct{}

func (stubPlanner) Plan(_ context.Context, _ string, _ planner.Brief, _ json.RawMessage) (planner.Result, error) {
	return planner.Result{PlanID: "pl", Valid: true}, nil
}
func (stubPlanner) PlanWith(_ context.Context, _ string, _ llm.ChatModel, _ planner.Brief, _ json.RawMessage) (planner.Result, error) {
	return planner.Result{PlanID: "pl", Valid: true}, nil
}
func (stubPlanner) PlanCustom(_ context.Context, _, _ string, _ planner.Brief, _ []planner.WorkflowNode, _ map[string]planner.ResolvedType, _ json.RawMessage) (planner.Result, error) {
	return planner.Result{PlanID: "pl", Valid: true}, nil
}

type stubAppender struct{}

func (stubAppender) Append(_ context.Context, _, _, _ string, _ any) (int64, error) { return 1, nil }

// quotaTestWorkflow is a runnable single-node workflow store so the run reaches
// the quota gate (invalid graphs 400 before quota).
func quotaTestWorkflow() *stubWorkflows {
	return &stubWorkflows{got: workflows.Workflow{
		Name:  "wf",
		Nodes: json.RawMessage(`[{"id":"s1","type":"script","promptId":"","dependsOn":[]}]`),
	}}
}

func TestRunWorkflowHandler429WhenQuotaExhausted(t *testing.T) {
	cs := &stubCost{count: 5} // org already used 5 generations in the window
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, quotaTestWorkflow(), stubPlanner{}, stubAppender{}, cs, 5, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wf1/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wf1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("quota-exhausted run should 429, got %d: %s", rr.Code, rr.Body.String())
	}
}

// recordingProjects 记录 SetStatus 调用，用于断言 PlanCustom 失败时项目状态被回滚
// （不留在 planning，否则 TryBeginRun 会把项目永久 409 锁死）。
type recordingProjects struct {
	stubProjects
	lastStatus *string
}

func (s recordingProjects) SetStatus(_ context.Context, _, status string) error {
	*s.lastStatus = status
	return nil
}
func (s recordingProjects) TryBeginRun(_ context.Context, _ string) (bool, error) {
	*s.lastStatus = "planning" // 模拟 CAS 翻到 planning
	return true, nil
}

// failPlanner 的 PlanCustom 恒失败（500 路径），用于验证状态回滚。
type failPlanner struct{}

func (failPlanner) PlanCustom(_ context.Context, _, _ string, _ planner.Brief, _ []planner.WorkflowNode, _ map[string]planner.ResolvedType, _ json.RawMessage) (planner.Result, error) {
	return planner.Result{}, errors.New("boom")
}

func TestRunWorkflowHandlerRollsBackStatusOnPlanFailure(t *testing.T) {
	// PlanCustom 失败（500）后，项目状态必须从 planning 回滚（这里到 failed），
	// 否则 TryBeginRun 拒绝 planning，项目被永久锁死无法再运行。
	last := "draft"
	ps := recordingProjects{stubProjects: stubProjects{orgID: "o1"}, lastStatus: &last}
	h := runWorkflowHandler(ps, quotaTestWorkflow(), failPlanner{}, stubAppender{}, &stubCost{count: 0}, 0, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wf1/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wf1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("plan failure should 500, got %d: %s", rr.Code, rr.Body.String())
	}
	if last == "planning" {
		t.Fatalf("project left stranded in planning after plan failure (would 409-lock future runs)")
	}
	if last != "failed" {
		t.Fatalf("expected status rolled back to failed, got %q", last)
	}
}

func TestRunWorkflowHandler409WhenRunInProgress(t *testing.T) {
	// 项目已有在途 run（TryBeginRun 返 false）→ 并发门禁应 409，且不建 plan。
	cs := &stubCost{count: 0}
	h := runWorkflowHandler(stubProjects{orgID: "o1", runBusy: true}, quotaTestWorkflow(), stubPlanner{}, stubAppender{}, cs, 0, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wf1/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wf1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("in-progress run should 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRunWorkflowHandlerEmptyNodesLocalized(t *testing.T) {
	// 空工作流（0 节点）→ 400，且中文文案（本地化，非 raw English）。
	ws := &stubWorkflows{got: workflows.Workflow{Name: "wf", Nodes: json.RawMessage(`[]`)}}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, ws, stubPlanner{}, stubAppender{}, &stubCost{count: 0}, 0, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wf1/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wf1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty-workflow run should 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "工作流没有任何节点") {
		t.Fatalf("empty-workflow 400 body should be localized Chinese, got %q", rr.Body.String())
	}
}

func TestRunWorkflowHandlerPassesUnderQuota(t *testing.T) {
	cs := &stubCost{count: 4}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, quotaTestWorkflow(), stubPlanner{}, stubAppender{}, cs, 5, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wf1/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wf1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("under-quota run should 202, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRunWorkflowHandlerQuotaZeroDisabled(t *testing.T) {
	cs := &stubCost{count: 1000}
	h := runWorkflowHandler(stubProjects{orgID: "o1"}, quotaTestWorkflow(), stubPlanner{}, stubAppender{}, cs, 0, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/workflows/wf1/run", nil)
	req.SetPathValue("id", "p1")
	req.SetPathValue("wfId", "wf1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("quota=0 must disable the check, got %d", rr.Code)
	}
}

type secretRejectingModels struct{}

func (secretRejectingModels) Create(_ context.Context, _ models.CreateInput) (models.ModelConfig, error) {
	return models.ModelConfig{}, models.ErrSecretParam
}
func (secretRejectingModels) ListByOrg(_ context.Context, _ string) ([]models.ModelConfig, error) {
	return nil, nil
}
func (secretRejectingModels) Update(_ context.Context, _, _ string, _ models.UpdateInput) (models.ModelConfig, error) {
	return models.ModelConfig{}, models.ErrSecretParam
}
func (secretRejectingModels) Delete(_ context.Context, _, _ string) error { return nil }

func TestCreateModelConfig400OnSecretParams(t *testing.T) {
	h := createModelConfigHandler(secretRejectingModels{})
	req := httptest.NewRequest("POST", "/api/orgs/o1/model-configs",
		strings.NewReader(`{"provider":"openai","model":"dall-e-3","params":{"apiKey":"sk-1"}}`))
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("secret params must 400 (audit: keys never persisted), got %d", rr.Code)
	}
}
