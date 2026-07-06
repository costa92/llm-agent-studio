package models

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/costa92/llm-agent-studio/internal/localcache"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// TestCachedResolveWriteThroughAndK8 exercises the cache-backed store end to end
// against a real PG: preload → write-through invalidate → cache-served resolve
// with decrypt-at-read (keystone K8) → delete invalidation → parity with the
// non-cached DB path.
func TestCachedResolveWriteThroughAndK8(t *testing.T) {
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run cached model store tests")
	}
	ctx := context.Background()
	st, err := storage.Open(ctx, storage.Config{PGURL: dsn})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	hub, err := localcache.NewHub(st.Pool(), dsn, slog.Default())
	if err != nil {
		t.Fatalf("hub: %v", err)
	}
	box := testBox(t)
	cached := NewCached(st.GORM(), box, hub)
	if err := hub.PreloadAll(); err != nil {
		t.Fatalf("preload: %v", err)
	}

	org := "cache-org-" + uniqueSuffix()

	// Create through the cached store → write-through invalidate reloads local cache.
	if _, err := cached.Create(ctx, CreateInput{
		OrgID: org, Kind: "text", Provider: "openai", Model: "gpt-4o",
		Enabled: true, IsDefault: true, APIKey: "sk-secret-123",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Resolve is served from the cache; decrypt-at-read yields the plaintext key.
	// (K8: only the ciphertext lives in the cache; plaintext is materialized here.)
	rm, ok, err := cached.ResolveForOrg(ctx, org, "text")
	if err != nil || !ok {
		t.Fatalf("cached resolve: %v ok=%v", err, ok)
	}
	if rm.Provider != "openai" || rm.Model != "gpt-4o" {
		t.Fatalf("cached resolve wrong: %+v", rm)
	}
	if rm.APIKey != "sk-secret-123" {
		t.Fatalf("decrypt-at-read failed: key=%q", rm.APIKey)
	}

	// Parity: a non-cached store reading the same DB returns identical resolution.
	plain := New(st.GORM(), box)
	rm2, ok2, err2 := plain.ResolveForOrg(ctx, org, "text")
	if err2 != nil || !ok2 {
		t.Fatalf("plain resolve: %v ok=%v", err2, ok2)
	}
	if rm2.Provider != rm.Provider || rm2.Model != rm.Model || rm2.BaseURL != rm.BaseURL || rm2.APIKey != rm.APIKey {
		t.Fatalf("cache/DB parity mismatch: cached=%+v db=%+v", rm, rm2)
	}

	// ListByOrg served from cache; DTO exposes HasAPIKey, never the key.
	list, err := cached.ListByOrg(ctx, org)
	if err != nil || len(list) != 1 {
		t.Fatalf("cached list: %v len=%d", err, len(list))
	}
	if !list[0].HasAPIKey {
		t.Fatalf("HasAPIKey should be true")
	}

	// Delete through the cached store → invalidate → cache no longer resolves.
	if err := cached.Delete(ctx, list[0].ID, org); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := cached.ResolveForOrg(ctx, org, "text"); ok {
		t.Fatalf("resolve after delete still ok — stale cache")
	}
}
