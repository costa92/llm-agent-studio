package httpapi

import (
	"context"
	"encoding/json"
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
)

// stubCost is a canned CostStore for handler tests.
type stubCost struct {
	agg      cost.Aggregate
	per      []cost.ProjectAggregate
	recent   []cost.LedgerEntry
	count    int
	recorded []cost.Generation

	gotFrom, gotTo time.Time
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
func (s *stubCost) RecentByOrg(_ context.Context, _ string, _ int) ([]cost.LedgerEntry, error) {
	return s.recent, nil
}
func (s *stubCost) CountByOrgSince(_ context.Context, _ string, _ time.Time) (int, error) {
	return s.count, nil
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
	cs := &stubCost{recent: []cost.LedgerEntry{{ID: "g1", Provider: "openai"}}}
	h := orgGenerationsHandler(cs)
	req := httptest.NewRequest("GET", "/api/orgs/o1/generations?limit=5", nil)
	req.SetPathValue("org", "o1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"id":"g1"`) {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
}

// --- quota stubs ---

type stubProjects struct{ orgID string }

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
func (s stubProjects) SetCover(_ context.Context, _, _ string) error   { return nil }
func (s stubProjects) Cancel(_ context.Context, _ string) error        { return nil }
func (s stubProjects) OrgIDForProject(_ context.Context, _ string) (string, error) {
	return s.orgID, nil
}
func (s stubProjects) ListPlans(_ context.Context, _ string) ([]project.Plan, error) {
	return nil, nil
}
func (s stubProjects) LoadState(_ context.Context, _ string) (projectstate.ProjectState, error) {
	return projectstate.ProjectState{}, nil
}

type stubPlanner struct{}

func (stubPlanner) Plan(_ context.Context, _ string, _ planner.Brief) (planner.Result, error) {
	return planner.Result{PlanID: "pl", Valid: true}, nil
}
func (stubPlanner) PlanWith(_ context.Context, _ string, _ llm.ChatModel, _ planner.Brief) (planner.Result, error) {
	return planner.Result{PlanID: "pl", Valid: true}, nil
}
func (stubPlanner) PlanCustom(_ context.Context, _, _ string, _ planner.Brief, _ []planner.WorkflowNode) (planner.Result, error) {
	return planner.Result{PlanID: "pl", Valid: true}, nil
}

type stubAppender struct{}

func (stubAppender) Append(_ context.Context, _, _, _ string, _ any) (int64, error) { return 1, nil }

func TestRunHandler429WhenQuotaExhausted(t *testing.T) {
	cs := &stubCost{count: 5} // org already used 5 generations in the window
	h := runHandler(stubProjects{orgID: "o1"}, stubPlanner{}, stubAppender{}, cs, 5, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/run", nil)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("quota-exhausted run should 429, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRunHandlerPassesUnderQuota(t *testing.T) {
	cs := &stubCost{count: 4}
	h := runHandler(stubProjects{orgID: "o1"}, stubPlanner{}, stubAppender{}, cs, 5, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/run", nil)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("under-quota run should 202, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestRunHandlerQuotaZeroDisabled(t *testing.T) {
	cs := &stubCost{count: 1000}
	h := runHandler(stubProjects{orgID: "o1"}, stubPlanner{}, stubAppender{}, cs, 0, nil)
	req := httptest.NewRequest("POST", "/api/projects/p1/run", nil)
	req.SetPathValue("id", "p1")
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
