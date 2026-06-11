package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/costa92/llm-agent-studio/internal/cost"
)

// stubCost is a canned CostStore for handler tests.
type stubCost struct {
	agg    cost.Aggregate
	per    []cost.ProjectAggregate
	recent []cost.LedgerEntry
	count  int

	gotFrom, gotTo time.Time
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
