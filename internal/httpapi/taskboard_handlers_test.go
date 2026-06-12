package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/studiosvc"
)

// stubTaskBoard is a fake TaskBoardReader returning a fixed slice.
type stubTaskBoard struct {
	rows    []studiosvc.TaskRow
	lastOrg string
}

func (s *stubTaskBoard) Board(_ context.Context, orgID string) ([]studiosvc.TaskRow, error) {
	s.lastOrg = orgID
	return s.rows, nil
}

// TestTaskboardHandler proves the handler wraps rows in {items, counts}, buckets
// planning→running and canceled→all-only, and renders nil items as [].
func TestTaskboardHandler(t *testing.T) {
	st := &stubTaskBoard{rows: []studiosvc.TaskRow{
		{ProjectID: "p1", Status: "planning"},
		{ProjectID: "p2", Status: "running"},
		{ProjectID: "p3", Status: "review", PendingReview: 2},
		{ProjectID: "p4", Status: "failed", Failed: true, FailingAgent: "ScriptAgent"},
		{ProjectID: "p5", Status: "completed"},
		{ProjectID: "p6", Status: "draft"},
		{ProjectID: "p7", Status: "canceled"},
	}}
	rr := httptest.NewRecorder()
	req := storageReq("GET", "/api/orgs/o1/tasks", "")
	req.SetPathValue("org", "o1")
	taskboardHandler(st)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if st.lastOrg != "o1" {
		t.Fatalf("org not passed: %q", st.lastOrg)
	}

	var resp struct {
		Items  []studiosvc.TaskRow `json:"items"`
		Counts map[string]int      `json:"counts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if len(resp.Items) != 7 {
		t.Fatalf("want 7 items, got %d", len(resp.Items))
	}
	want := map[string]int{
		"all": 7, "running": 2, "review": 1, "failed": 1, "completed": 1, "draft": 1,
	}
	for k, v := range want {
		if resp.Counts[k] != v {
			t.Fatalf("counts[%q] want %d, got %d (counts=%v)", k, v, resp.Counts[k], resp.Counts)
		}
	}

	// nil items → [].
	rr2 := httptest.NewRecorder()
	req2 := storageReq("GET", "/api/orgs/o1/tasks", "")
	req2.SetPathValue("org", "o1")
	taskboardHandler(&stubTaskBoard{})(rr2, req2)
	var resp2 struct {
		Items []studiosvc.TaskRow `json:"items"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode2: %v", err)
	}
	if resp2.Items == nil {
		t.Fatalf("nil items must render as [], got null: %s", rr2.Body.String())
	}
}
