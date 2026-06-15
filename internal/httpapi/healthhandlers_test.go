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

	"github.com/costa92/llm-agent-studio/internal/health"
)

// fakeHealth is a canned HealthReporter for handler-level tests (no DB).
type fakeHealth struct {
	pingErr   error
	report    health.Report
	reportErr error
	repairErr error
	repaired  int
	failures  []health.Failure
}

func (f fakeHealth) Report(context.Context) (health.Report, error) {
	return f.report, f.reportErr
}
func (f fakeHealth) Repair(_ context.Context, checkID string) (health.RepairResult, error) {
	if f.repairErr != nil {
		return health.RepairResult{}, f.repairErr
	}
	return health.RepairResult{CheckID: checkID, Repaired: f.repaired}, nil
}
func (f fakeHealth) Ping(context.Context) error { return f.pingErr }
func (f fakeHealth) RecentFailures(context.Context, int) ([]health.Failure, error) {
	return f.failures, nil
}

func TestHealthzHandler(t *testing.T) {
	// down
	h := healthzHandler(fakeHealth{pingErr: errors.New("db down")})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("down code=%d want 503 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "down") {
		t.Fatalf("down body=%s", rec.Body.String())
	}
	// ok
	h = healthzHandler(fakeHealth{})
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("ok code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMetricsHandler(t *testing.T) {
	rep := health.Report{
		System: health.System{DBOK: true, DBLatencyMs: 7},
		Checks: []health.Check{
			{ID: "stuck_todos", Count: 3},
			{ID: "stuck_assets", Count: 1},
		},
	}
	h := metricsHandler(fakeHealth{report: rep})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content-type=%q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "studio_stuck_todos 3") {
		t.Fatalf("metrics body missing studio_stuck_todos: %s", body)
	}
	if !strings.Contains(body, "studio_db_up 1") || !strings.Contains(body, "studio_db_latency_ms 7") {
		t.Fatalf("metrics body missing db gauges: %s", body)
	}

	// Report error → only studio_db_up 0.
	h = metricsHandler(fakeHealth{reportErr: errors.New("boom")})
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "studio_db_up 0") {
		t.Fatalf("error path code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPlatformHealthRepairHandler(t *testing.T) {
	// empty body → 400
	h := platformHealthRepairHandler(fakeHealth{})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/api/platform/health/repair", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code=%d want 400", rec.Code)
	}
	// repair returns error → 400
	h = platformHealthRepairHandler(fakeHealth{repairErr: errors.New("unknown check")})
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/api/platform/health/repair", strings.NewReader(`{"checkId":"bogus"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("repair-err code=%d want 400", rec.Code)
	}
	// success → 200 with repaired
	h = platformHealthRepairHandler(fakeHealth{repaired: 4})
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest("POST", "/api/platform/health/repair", strings.NewReader(`{"checkId":"stuck_todos"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("ok code=%d body=%s", rec.Code, rec.Body.String())
	}
	var res health.RepairResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Repaired != 4 || res.CheckID != "stuck_todos" {
		t.Fatalf("result=%+v", res)
	}
}

func TestPlatformHealthEventsHandler(t *testing.T) {
	h := platformHealthEventsHandler(fakeHealth{failures: []health.Failure{
		{TodoID: "t1", ProjectName: "P", At: time.Now()},
	}})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/platform/health/events?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"items"`) || !strings.Contains(rec.Body.String(), "t1") {
		t.Fatalf("events body=%s", rec.Body.String())
	}
}

func TestPlatformHealthHandler(t *testing.T) {
	h := platformHealthHandler(fakeHealth{report: health.Report{System: health.System{DBOK: true}}})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/api/platform/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"dbOk":true`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}
