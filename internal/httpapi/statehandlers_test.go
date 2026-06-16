package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/projectstate"
)

func TestStateHandler_ReturnsSnapshot(t *testing.T) {
	want := projectstate.ProjectState{ProjectID: "p1", Version: 3, Status: "running", RunStatus: "running"}
	h := stateHandler(stateStoreStub{state: want})
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

type stateStoreStub struct{ state projectstate.ProjectState }

func (s stateStoreStub) LoadState(ctx context.Context, id string) (projectstate.ProjectState, error) {
	return s.state, nil
}
