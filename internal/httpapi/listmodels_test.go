package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/models"
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

// revealReq builds a GET reveal request scoped to (org, id).
func revealReq(org, id string) *http.Request {
	req := httptest.NewRequest("GET", "/api/orgs/"+org+"/model-configs/"+id+"/reveal", nil)
	req.SetPathValue("org", org)
	req.SetPathValue("id", id)
	return req
}

// TestRevealModelKeyHandlerReturnsDecryptedKey: an admin reveal returns the full
// plaintext key plus hasApiKey:true. This is the one endpoint allowed to echo the
// key (the list/create/update guard tests cover that those still never do).
func TestRevealModelKeyHandlerReturnsDecryptedKey(t *testing.T) {
	keyLookup := func(_ context.Context, orgID, configID string) (string, error) {
		if orgID == "org1" && configID == "cfg1" {
			return "sk-stored-secret", nil
		}
		return "", models.ErrNotFound
	}
	rr := httptest.NewRecorder()
	revealModelKeyHandler(keyLookup)(rr, revealReq("org1", "cfg1"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out struct {
		HasAPIKey bool   `json:"hasApiKey"`
		APIKey    string `json:"apiKey"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if !out.HasAPIKey || out.APIKey != "sk-stored-secret" {
		t.Fatalf("want full key revealed, got %+v", out)
	}
}

// TestRevealModelKeyHandlerNoStoredKey: a config with no per-config key reveals
// hasApiKey:false and an empty key (not a 404 — the config exists).
func TestRevealModelKeyHandlerNoStoredKey(t *testing.T) {
	keyLookup := func(_ context.Context, _, _ string) (string, error) { return "", nil }
	rr := httptest.NewRecorder()
	revealModelKeyHandler(keyLookup)(rr, revealReq("org1", "cfg1"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var out struct {
		HasAPIKey bool   `json:"hasApiKey"`
		APIKey    string `json:"apiKey"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.HasAPIKey || out.APIKey != "" {
		t.Fatalf("want hasApiKey:false empty key, got %+v", out)
	}
}

// TestRevealModelKeyHandlerNotFound: a missing / cross-org config is a 404.
func TestRevealModelKeyHandlerNotFound(t *testing.T) {
	keyLookup := func(_ context.Context, _, _ string) (string, error) { return "", models.ErrNotFound }
	rr := httptest.NewRecorder()
	revealModelKeyHandler(keyLookup)(rr, revealReq("org1", "missing"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing config should be 404, got %d", rr.Code)
	}
}

// TestRevealModelKeyHandlerDecryptFailureSanitized: a decrypt error (e.g. an
// enc-key rotation left the ciphertext undecryptable) returns 500 with a redacted
// operator message — the raw NaCl secretbox internals must NEVER cross the wire.
func TestRevealModelKeyHandlerDecryptFailureSanitized(t *testing.T) {
	keyLookup := func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("decrypt key: secretbox: open: cipher: message authentication failed")
	}
	rr := httptest.NewRecorder()
	revealModelKeyHandler(keyLookup)(rr, revealReq("org1", "cfg1"))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("decrypt failure should be 500, got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, leak := range []string{"secretbox", "cipher", "authentication failed", "decrypt key"} {
		if strings.Contains(body, leak) {
			t.Fatalf("reveal leaked NaCl internal %q to client: %s", leak, body)
		}
	}
	if !strings.Contains(body, "密钥解密失败") {
		t.Fatalf("want redacted operator message, got: %s", body)
	}
}
