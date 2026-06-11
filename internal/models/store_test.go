package models

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/storage"
)

func TestCatalogListsImageProviders(t *testing.T) {
	cat := Catalog()
	// spec §13 R3: the openai/google/minimax/volcengine image providers are all
	// present and image-kind. (M4 also adds video/audio entries — see
	// TestCatalogIncludesVideoAndAudio — so this no longer asserts image-only.)
	want := map[string]bool{"openai": false, "google": false, "minimax": false, "volcengine": false}
	for _, e := range cat {
		if e.Kind != "image" {
			continue
		}
		if _, ok := want[e.Provider]; ok {
			want[e.Provider] = true
		}
	}
	for p, seen := range want {
		if !seen {
			t.Fatalf("catalog missing provider %q", p)
		}
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run model store tests")
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
	return st.Pool()
}

func TestCreateAndListByOrg(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	mc, err := st.Create(ctx, CreateInput{
		OrgID: "org-m", Kind: "image", Provider: "openai", Model: "gpt-image-1",
		Enabled: true, IsDefault: true,
	})
	if err != nil || mc.ID == "" {
		t.Fatalf("create: %v %+v", err, mc)
	}
	list, err := st.ListByOrg(ctx, "org-m")
	if err != nil || len(list) != 1 || list[0].Provider != "openai" {
		t.Fatalf("list: %v %+v", err, list)
	}
	if _, err := st.Create(ctx, CreateInput{OrgID: "org-m", Provider: "openai", Model: "dall-e-3",
		Params: json.RawMessage(`{"size":"1024x1024","max_tokens":1024}`)}); err != nil {
		t.Fatalf("benign params (incl. max_tokens count field, 评审修复 M2) rejected: %v", err)
	}
}

func TestDefaultForOrg(t *testing.T) {
	pool := testPool(t)
	st := New(pool)
	ctx := context.Background()
	_, _ = st.Create(ctx, CreateInput{OrgID: "org-d", Kind: "image", Provider: "minimax", Model: "image-01", Enabled: true, IsDefault: false})
	_, _ = st.Create(ctx, CreateInput{OrgID: "org-d", Kind: "image", Provider: "openai", Model: "gpt-image-1", Enabled: true, IsDefault: true})
	prov, model, ok, err := st.DefaultForOrg(ctx, "org-d", "image")
	if err != nil || !ok || prov != "openai" || model != "gpt-image-1" {
		t.Fatalf("default: %v ok=%v %s/%s", err, ok, prov, model)
	}
}

func TestCreateRejectsSecretParams(t *testing.T) {
	// 密钥审计 (spec §6/§8): API keys live ONLY in server env. params_json is a
	// free-form column an admin could be tempted to stash a key into — reject
	// credential-looking keys outright, recursively. Validation fires before
	// any DB access, so a nil pool proves the ordering.
	s := New(nil)
	for _, params := range []string{
		`{"apiKey":"sk-123"}`,
		`{"api_key":"sk-123"}`,
		`{"client_secret":"x"}`,
		`{"access_token":"t"}`,
		`{"api_token":"x"}`,
		`{"password":"p"}`,
		`{"nested":{"ApiKey":"sk"}}`,
	} {
		_, err := s.Create(context.Background(), CreateInput{
			OrgID: "o", Provider: "p", Model: "m", Params: json.RawMessage(params),
		})
		if !errors.Is(err, ErrSecretParam) {
			t.Fatalf("params %s must be rejected with ErrSecretParam, got %v", params, err)
		}
	}
}

func TestSecretParamMatchingExcludesTokenCounts(t *testing.T) {
	// 评审修复 M2: substring "token" false-positived on legitimate count/config
	// fields. Matching is now word-bounded — max_tokens / token_budget pass,
	// api_token (asserted above) stays rejected.
	for _, params := range []string{
		`{"max_tokens":1024}`,
		`{"token_budget":4096}`,
	} {
		var m map[string]any
		if err := json.Unmarshal([]byte(params), &m); err != nil {
			t.Fatal(err)
		}
		if k, found := secretKeyIn(m); found {
			t.Fatalf("params %s wrongly flagged (key %q) — count fields are legal", params, k)
		}
	}
}

func TestCatalogIncludesVideoAndAudio(t *testing.T) {
	var hasVideo, hasAudio bool
	for _, e := range Catalog() {
		if e.Kind == "video" {
			hasVideo = true
		}
		if e.Kind == "audio" {
			hasAudio = true
		}
	}
	if !hasVideo || !hasAudio {
		t.Fatalf("catalog must include video + audio entries (M4): video=%v audio=%v", hasVideo, hasAudio)
	}
}
