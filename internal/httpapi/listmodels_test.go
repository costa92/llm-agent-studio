package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListModelsHandlerUsesStoredKey: when the request omits apiKey but names a
// configId, the handler resolves the stored key via keyLookup and forwards it as
// a Bearer token to the provider's official /models endpoint.
func TestListModelsHandlerUsesStoredKey(t *testing.T) {
	var gotAuth string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
	}))
	defer provider.Close()

	keyLookup := func(_ context.Context, orgID, configID string) (string, error) {
		if orgID == "org1" && configID == "cfg1" {
			return "stored-secret", nil
		}
		return "", nil
	}
	body := `{"provider":"openai","baseUrl":"` + provider.URL + `","configId":"cfg1"}`
	req := httptest.NewRequest("POST", "/api/orgs/org1/model-configs/list-models", strings.NewReader(body))
	req.SetPathValue("org", "org1")
	rr := httptest.NewRecorder()

	listModelsHandler(keyLookup)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		Models []string `json:"models"`
		Source string   `json:"source"`
		Error  string   `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Source != "live" || len(out.Models) != 2 {
		t.Fatalf("want live with 2 models, got %+v", out)
	}
	if gotAuth != "Bearer stored-secret" {
		t.Fatalf("stored key not forwarded as Bearer: %q", gotAuth)
	}
}

// TestListModelsHandlerFallsBackForUnsupportedProvider: a provider with no live
// API returns the static catalog plus an explanatory error, still HTTP 200.
func TestListModelsHandlerFallsBackForUnsupportedProvider(t *testing.T) {
	body := `{"provider":"minimax"}`
	req := httptest.NewRequest("POST", "/api/orgs/org1/model-configs/list-models", strings.NewReader(body))
	req.SetPathValue("org", "org1")
	rr := httptest.NewRecorder()

	listModelsHandler(nil)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var out struct {
		Models []string `json:"models"`
		Source string   `json:"source"`
		Error  string   `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.Source != "catalog" || out.Error == "" {
		t.Fatalf("want catalog fallback with error, got %+v", out)
	}
	// minimax has a static catalog entry (image-01), so fallback is non-empty.
	if len(out.Models) == 0 {
		t.Fatalf("expected static catalog fallback models for minimax")
	}
}

// TestListModelsHandlerRejectsMissingProvider.
func TestListModelsHandlerRejectsMissingProvider(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/orgs/org1/model-configs/list-models", strings.NewReader(`{}`))
	req.SetPathValue("org", "org1")
	rr := httptest.NewRecorder()
	listModelsHandler(nil)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("missing provider should be 400, got %d", rr.Code)
	}
}
