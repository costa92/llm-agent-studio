package modellist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var fallback = []string{"catalog-a", "catalog-b"}

func TestOpenAICompatibleLive(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/models" {
			http.Error(w, "wrong path "+r.URL.Path, 404)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"},{"id":""}]}`))
	}))
	defer srv.Close()

	res, err := List(context.Background(), "openai", srv.URL, "sk-test", fallback)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if res.Source != "live" {
		t.Fatalf("source=%q, want live", res.Source)
	}
	if len(res.Models) != 2 || res.Models[0] != "gpt-4o" || res.Models[1] != "gpt-4o-mini" {
		t.Fatalf("models=%v, want [gpt-4o gpt-4o-mini] (empty id dropped)", res.Models)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("auth header=%q, want Bearer sk-test", gotAuth)
	}
}

func TestOllamaLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.Error(w, "wrong path", 404)
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3"},{"name":"qwen2.5"}]}`))
	}))
	defer srv.Close()

	res, err := List(context.Background(), "ollama", srv.URL, "", fallback)
	if err != nil || res.Source != "live" || len(res.Models) != 2 {
		t.Fatalf("ollama: res=%+v err=%v", res, err)
	}
}

// TestMinimaxLiveUsesOpenAICompatibleShape: MiniMax exposes /v1/models in the
// same {data:[{id}]} shape as OpenAI. Listing must hit that endpoint with a
// Bearer key, parse the OpenAI envelope, and return Source "live".
func TestMinimaxLiveUsesOpenAICompatibleShape(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[{"id":"MiniMax-Text-01"},{"id":"MiniMax-M1"},{"id":"abab-6.5s-chat"}]}`))
	}))
	defer srv.Close()

	// Caller is expected to pass a base URL that already includes the /v1 prefix
	// (e.g. the defaultBaseURL value "https://api.minimax.chat/v1", or an
	// override entered in the admin UI).
	base := srv.URL + "/v1"
	res, info := List(context.Background(), "minimax", base, "sk-minimax-test", fallback)
	if info != nil {
		t.Fatalf("expected live source, got info=%+v", info)
	}
	if res.Source != "live" {
		t.Fatalf("source=%q, want live", res.Source)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("path=%q, want /v1/models", gotPath)
	}
	if gotAuth != "Bearer sk-minimax-test" {
		t.Fatalf("auth=%q, want Bearer sk-minimax-test", gotAuth)
	}
	if len(res.Models) != 3 || res.Models[0] != "MiniMax-Text-01" {
		t.Fatalf("models=%v, want MiniMax text models", res.Models)
	}
}

// TestMinimaxDefaultBaseURL: built-in minimax should resolve to the official
// MiniMax API endpoint when the caller leaves BaseURL empty.
func TestMinimaxDefaultBaseURL(t *testing.T) {
	if got := defaultBaseURL("minimax"); got != "https://api.minimax.chat/v1" {
		t.Fatalf("defaultBaseURL(minimax)=%q, want https://api.minimax.chat/v1", got)
	}
}

func TestGoogleLiveStripsPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-1.5-pro"},{"name":"models/gemini-1.5-flash"}]}`))
	}))
	defer srv.Close()

	res, err := List(context.Background(), "google", srv.URL, "secret-key", fallback)
	if err != nil {
		t.Fatalf("google: %v", err)
	}
	if res.Models[0] != "gemini-1.5-pro" {
		t.Fatalf("want models/ prefix stripped, got %v", res.Models)
	}
}

func TestUnsupportedProviderFallsBack(t *testing.T) {
	res, err := List(context.Background(), "minimax", "", "", fallback)
	if err == nil {
		t.Fatalf("unsupported provider should return an explanatory error")
	}
	if res.Source != "catalog" || len(res.Models) != 2 {
		t.Fatalf("want catalog fallback, got %+v", res)
	}
}

func TestErrorFallsBackToCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid key", http.StatusUnauthorized)
	}))
	defer srv.Close()

	res, err := List(context.Background(), "openai", srv.URL, "bad", fallback)
	if err == nil || res.Source != "catalog" {
		t.Fatalf("401 should fall back with error: res=%+v err=%v", res, err)
	}
}

// TestGoogleErrorDoesNotLeakKey: the Gemini key rides in the query string, so a
// failure must NOT echo the URL (and thus the key) back to the caller.
func TestGoogleErrorDoesNotLeakKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := List(context.Background(), "google", srv.URL, "super-secret-key", fallback)
	if err == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(err.Error(), "super-secret-key") {
		t.Fatalf("error leaked the API key: %v", err)
	}
}

func TestEmptyLiveResultFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	res, err := List(context.Background(), "openai", srv.URL, "k", fallback)
	if err == nil || res.Source != "catalog" {
		t.Fatalf("empty live list should fall back: res=%+v err=%v", res, err)
	}
}

// TestErrInfoIsUserFacing: failures must surface a clean Message + Hint, never
// the raw Go error chain (no "modellist:" prefix, no double-printed URL).
func TestErrInfoIsUserFacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, info := List(context.Background(), "openai", srv.URL, "bad", fallback)
	if info == nil {
		t.Fatalf("expected ErrInfo on 401")
	}
	if strings.Contains(info.Message, "modellist:") {
		t.Errorf("Message leaks internal 'modellist:' prefix: %q", info.Message)
	}
	if strings.Contains(info.Message, srv.URL) {
		t.Errorf("Message leaks raw URL: %q", info.Message)
	}
	if info.Hint == "" {
		t.Errorf("expected non-empty Hint for auth failure")
	}
	if info.Internal == nil {
		t.Errorf("Internal should be set so ops can log it")
	}
}

// TestErrInfoOllamaConnectionRefused: Ollama's canonical "service not running"
// case must produce the specific Ollama-aware hint.
func TestErrInfoOllamaConnectionRefused(t *testing.T) {
	_, info := List(context.Background(), "ollama", "http://127.0.0.1:1", "", fallback)
	if info == nil {
		t.Fatalf("expected ErrInfo when Ollama port is closed")
	}
	if !strings.Contains(info.Message, "Ollama") {
		t.Errorf("Message should mention Ollama by name, got: %q", info.Message)
	}
	if !strings.Contains(info.Hint, "ollama") {
		t.Errorf("Hint should reference the ollama command, got: %q", info.Hint)
	}
	if strings.Contains(info.Message, "modellist:") || strings.Contains(info.Message, "dial tcp") {
		t.Errorf("Message leaks Go error chain: %q", info.Message)
	}
}

// TestErrInfoUnsupportedProvider: providers without a live listing API must get
// a friendly "no listing API" message, not a Go formatting string.
func TestErrInfoUnsupportedProvider(t *testing.T) {
	_, info := List(context.Background(), "minimax", "", "", fallback)
	if info == nil {
		t.Fatalf("expected ErrInfo for unsupported provider")
	}
	if strings.Contains(info.Message, "%") {
		t.Errorf("Message looks like an unformatted fmt template: %q", info.Message)
	}
	if strings.Contains(info.Message, "modellist:") {
		t.Errorf("Message leaks internal prefix: %q", info.Message)
	}
}

// TestGoogleErrorDoesNotLeakKey: the Gemini key rides in the query string, so a
// failure must NOT echo the URL (and thus the key) in the user-facing Message.
// Only the Internal field may carry the raw cause for ops logs.
func TestGoogleErrorDoesNotLeakKeyInMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusForbidden)
	}))
	defer srv.Close()

	_, info := List(context.Background(), "google", srv.URL, "super-secret-key", fallback)
	if info == nil {
		t.Fatalf("expected error")
	}
	if strings.Contains(info.Message, "super-secret-key") {
		t.Fatalf("user-facing Message leaked the API key: %v", info)
	}
	if strings.Contains(info.Message, srv.URL) {
		t.Fatalf("user-facing Message leaked the URL (which carries the key): %v", info)
	}
}
