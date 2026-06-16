package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/project"
	"github.com/costa92/llm-agent-studio/internal/projectstate"
)

func TestStateHandler_ReturnsSnapshot(t *testing.T) {
	want := projectstate.ProjectState{ProjectID: "p1", Version: 3, Status: "running", RunStatus: "running"}
	h := stateHandler(&stateStoreStub{state: want})
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/state", nil)
	req.SetPathValue("id", "p1")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	var got projectstate.ProjectState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.Version != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestStateHandler_NotFound(t *testing.T) {
	h := stateHandler(errStoreStub{err: project.ErrNotFound})
	req := httptest.NewRequest(http.MethodGet, "/api/projects/missing/state", nil)
	req.SetPathValue("id", "missing")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

// TestStateHandler_ForwardsPlanID asserts that ?planId=<id> is forwarded to
// LoadState so historical runs render their own plan's state, not the latest.
func TestStateHandler_ForwardsPlanID(t *testing.T) {
	stub := &stateStoreStub{state: projectstate.ProjectState{ProjectID: "p1"}}
	h := stateHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/state?planId=foo", nil)
	req.SetPathValue("id", "p1")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if stub.calledWith != "foo" {
		t.Errorf("LoadState called with planID=%q, want %q", stub.calledWith, "foo")
	}
}

type stateStoreStub struct {
	state      projectstate.ProjectState
	calledWith string // records the planID argument from the last LoadState call
}

func (s *stateStoreStub) LoadState(ctx context.Context, id, planID string) (projectstate.ProjectState, error) {
	s.calledWith = planID
	return s.state, nil
}

type errStoreStub struct{ err error }

func (s errStoreStub) LoadState(_ context.Context, _, _ string) (projectstate.ProjectState, error) {
	return projectstate.ProjectState{}, s.err
}
