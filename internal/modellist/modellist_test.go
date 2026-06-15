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
