package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/customnodetype"
)

// TestCreateProjectRejectsRegistryOnlyOverlay (W2 createProject) [DB]: a custom
// workflow node that smuggles a RegistryOnly overlay (script code carrying a
// {{secret:}} ref) must be rejected with 400 at SAVE, before project create.
func TestCreateProjectRejectsRegistryOnlyOverlay(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"code": "print(1)", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "code", Kind: "script", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := createProjectHandler(stubProjects{orgID: org}, store)
	nodes := `[{"id":"n1","type":"custom:code","typeId":"` + ct.ID + `","dependsOn":[],"typeVersion":1,"parameters":{"code":"x = {{secret:K}}"}}]`
	body := `{"name":"p1","customWorkflowEnabled":true,"workflowNodes":` + nodes + `}`
	req := httptest.NewRequest("POST", "/api/orgs/"+org+"/projects", strings.NewReader(body))
	req.SetPathValue("org", org)
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("RegistryOnly overlay must be rejected at createProject, got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "secret:K") {
		t.Fatalf("error body leaked secret payload: %s", rr.Body.String())
	}
}

// TestCreateProjectAcceptsCleanOverlay (W2) [DB]: a non-dangerous overlay passes
// the save-time gate and the project create proceeds.
func TestCreateProjectAcceptsCleanOverlay(t *testing.T) {
	store := mergeTestStore(t)
	org := "org-" + t.Name()
	base, _ := json.Marshal(map[string]any{"code": "print(1)", "outputFormat": "text"})
	ct, err := store.Create(context.Background(), org, customnodetype.UpsertInput{Label: "code", Kind: "script", Params: base})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := createProjectHandler(stubProjects{orgID: org}, store)
	nodes := `[{"id":"n1","type":"custom:code","typeId":"` + ct.ID + `","dependsOn":[],"typeVersion":1,"parameters":{"outputFormat":"json"}}]`
	body := `{"name":"p1","customWorkflowEnabled":true,"workflowNodes":` + nodes + `}`
	req := httptest.NewRequest("POST", "/api/orgs/"+org+"/projects", strings.NewReader(body))
	req.SetPathValue("org", org)
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clean overlay should 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
