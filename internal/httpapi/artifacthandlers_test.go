package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubArtifactReader captures the args passed to Script and Shots so tests can
// assert that the handler extracted the right query parameters and path value.
type stubArtifactReader struct {
	scriptProjectID string
	scriptPlanID    string
	scriptTodoID    string

	shotsProjectID string
	shotsPlanID    string
	shotsTodoID    string
}

func (s *stubArtifactReader) Todos(_ context.Context, _, _ string) ([]map[string]any, error) {
	return nil, nil
}

func (s *stubArtifactReader) Script(_ context.Context, projectID, planID, todoID string) (json.RawMessage, bool, error) {
	s.scriptProjectID = projectID
	s.scriptPlanID = planID
	s.scriptTodoID = todoID
	// Return a minimal JSON payload so the handler writes 200.
	return json.RawMessage(`{"ok":true}`), true, nil
}

func (s *stubArtifactReader) Shots(_ context.Context, projectID, planID, todoID string) ([]map[string]any, error) {
	s.shotsProjectID = projectID
	s.shotsPlanID = planID
	s.shotsTodoID = todoID
	return []map[string]any{}, nil
}

func (s *stubArtifactReader) Assets(_ context.Context, _, _, _ string) ([]map[string]any, error) {
	return nil, nil
}

// TestScriptHandler_QueryParamsReachReader verifies that ?todoId= and ?planId=
// are forwarded verbatim to the ArtifactReader and that the project-id path
// value is also forwarded correctly.
func TestScriptHandler_QueryParamsReachReader(t *testing.T) {
	stub := &stubArtifactReader{}
	h := scriptHandler(stub)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/script?todoId=tdX&planId=plY", nil)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("scriptHandler should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if stub.scriptProjectID != "p1" {
		t.Errorf("projectID: got %q, want %q", stub.scriptProjectID, "p1")
	}
	if stub.scriptTodoID != "tdX" {
		t.Errorf("todoID: got %q, want %q", stub.scriptTodoID, "tdX")
	}
	if stub.scriptPlanID != "plY" {
		t.Errorf("planID: got %q, want %q", stub.scriptPlanID, "plY")
	}
}

// TestShotsHandler_QueryParamsReachReader verifies the same routing for the
// shots endpoint.
func TestShotsHandler_QueryParamsReachReader(t *testing.T) {
	stub := &stubArtifactReader{}
	h := shotsHandler(stub)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/shots?todoId=tdX&planId=plY", nil)
	req.SetPathValue("id", "p1")
	rr := httptest.NewRecorder()
	h(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("shotsHandler should 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if stub.shotsProjectID != "p1" {
		t.Errorf("projectID: got %q, want %q", stub.shotsProjectID, "p1")
	}
	if stub.shotsTodoID != "tdX" {
		t.Errorf("todoID: got %q, want %q", stub.shotsTodoID, "tdX")
	}
	if stub.shotsPlanID != "plY" {
		t.Errorf("planID: got %q, want %q", stub.shotsPlanID, "plY")
	}
}
