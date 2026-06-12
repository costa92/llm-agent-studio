package models

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// testBox 用固定 base64 32 字节密钥构造 enabled box (BYOK 加密)。
func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatalf("test box: %v", err)
	}
	return b
}

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
	st := New(pool, testBox(t))
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
	st := New(pool, testBox(t))
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
	s := New(nil, testBox(t))
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

func TestCreateWithAPIKeyHidesKey(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	const rawKey = "sk-byok-super-secret-987654321"
	mc, err := st.Create(ctx, CreateInput{
		OrgID: "org-byok", Kind: "text", Provider: "openai-compatible", Model: "gpt-4o-mini",
		Enabled: true, IsDefault: true, BaseURL: "https://api.example.com/v1", APIKey: rawKey,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !mc.HasAPIKey || mc.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("create result: hasKey=%v baseURL=%q", mc.HasAPIKey, mc.BaseURL)
	}
	list, err := st.ListByOrg(ctx, "org-byok")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got ModelConfig
	for _, c := range list {
		if c.ID == mc.ID {
			got = c
		}
	}
	if got.ID == "" {
		t.Fatalf("created config %s not in list %+v", mc.ID, list)
	}
	if !got.HasAPIKey || got.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("list result: hasKey=%v baseURL=%q", got.HasAPIKey, got.BaseURL)
	}
	// 关键：序列化后明文 key 绝不出现 (结构体无 key 字段)。
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), rawKey) {
		t.Fatalf("raw key leaked in ModelConfig JSON: %s", b)
	}
	if !strings.Contains(string(b), `"hasApiKey":true`) || !strings.Contains(string(b), `"baseUrl":"https://api.example.com/v1"`) {
		t.Fatalf("JSON shape missing hasApiKey/baseUrl: %s", b)
	}
}

func TestResolveForOrgDecryptsKey(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	const rawKey = "sk-resolve-me-555"
	if _, err := st.Create(ctx, CreateInput{
		OrgID: "org-rv", Kind: "text", Provider: "deepseek", Model: "deepseek-chat",
		Enabled: true, IsDefault: true, BaseURL: "https://ds.example.com", APIKey: rawKey,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	rm, ok, err := st.ResolveForOrg(ctx, "org-rv", "text")
	if err != nil || !ok {
		t.Fatalf("resolve: %v ok=%v", err, ok)
	}
	if rm.APIKey != rawKey || rm.BaseURL != "https://ds.example.com" || rm.Provider != "deepseek" {
		t.Fatalf("resolved: %+v", rm)
	}
	// 无启用默认 → ok=false。
	if _, ok, err := st.ResolveForOrg(ctx, "org-none", "text"); err != nil || ok {
		t.Fatalf("no default: ok=%v err=%v", ok, err)
	}
}

func TestCreateAPIKeyDisabledBoxFails(t *testing.T) {
	pool := testPool(t)
	disabled, _ := secretbox.New("") // disabled box
	st := New(pool, disabled)
	_, err := st.Create(context.Background(), CreateInput{
		OrgID: "org-x", Kind: "text", Provider: "openai", Model: "gpt-4o-mini", APIKey: "sk-nope",
	})
	if !errors.Is(err, ErrEncUnavailable) {
		t.Fatalf("APIKey with disabled box must return ErrEncUnavailable, got %v", err)
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

func TestUpdateKeepsKeyWhenBlank(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	const rawKey = "sk-keep-me-111"
	mc, err := st.Create(ctx, CreateInput{
		OrgID: "org-keep", Kind: "text", Provider: "deepseek", Model: "deepseek-chat",
		Enabled: true, IsDefault: true, BaseURL: "https://a.example.com", APIKey: rawKey,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 空 APIKey → 保留既有 key；改 model+base_url。
	upd, err := st.Update(ctx, mc.ID, "org-keep", UpdateInput{
		Kind: "text", Provider: "deepseek", Model: "deepseek-coder",
		Enabled: true, IsDefault: true, BaseURL: "https://b.example.com",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !upd.HasAPIKey || upd.Model != "deepseek-coder" || upd.BaseURL != "https://b.example.com" {
		t.Fatalf("update result: hasKey=%v model=%q baseURL=%q", upd.HasAPIKey, upd.Model, upd.BaseURL)
	}
	// 解密仍是原 key (keep 未动 api_key_enc)。
	rm, ok, err := st.ResolveForOrg(ctx, "org-keep", "text")
	if err != nil || !ok || rm.APIKey != rawKey {
		t.Fatalf("resolve after keep: ok=%v err=%v key=%q (want %q)", ok, err, rm.APIKey, rawKey)
	}
}

func TestUpdateReplacesKeyWhenSet(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	mc, err := st.Create(ctx, CreateInput{
		OrgID: "org-rep", Kind: "text", Provider: "deepseek", Model: "deepseek-chat",
		Enabled: true, IsDefault: true, APIKey: "sk-old-000",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const newKey = "sk-new-999"
	upd, err := st.Update(ctx, mc.ID, "org-rep", UpdateInput{
		Kind: "text", Provider: "deepseek", Model: "deepseek-chat",
		Enabled: true, IsDefault: true, APIKey: newKey,
	})
	if err != nil || !upd.HasAPIKey {
		t.Fatalf("update: %v hasKey=%v", err, upd.HasAPIKey)
	}
	rm, ok, err := st.ResolveForOrg(ctx, "org-rep", "text")
	if err != nil || !ok || rm.APIKey != newKey {
		t.Fatalf("resolve after replace: ok=%v err=%v key=%q (want %q)", ok, err, rm.APIKey, newKey)
	}
}

func TestUpdateScopedByOrg(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	mc, err := st.Create(ctx, CreateInput{
		OrgID: "org-owner", Kind: "text", Provider: "openai", Model: "gpt-4o-mini", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 用另一个 org 的身份更新同一 id → ErrNotFound，且原行不变。
	_, err = st.Update(ctx, mc.ID, "org-other", UpdateInput{
		Kind: "text", Provider: "evil", Model: "evil-model", Enabled: true,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org update must return ErrNotFound, got %v", err)
	}
	list, _ := st.ListByOrg(ctx, "org-owner")
	if len(list) != 1 || list[0].Provider != "openai" || list[0].Model != "gpt-4o-mini" {
		t.Fatalf("cross-org update mutated the row: %+v", list)
	}
}

func TestUpdateIsDefaultClearsSiblings(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	a, _ := st.Create(ctx, CreateInput{OrgID: "org-def", Kind: "text", Provider: "openai", Model: "gpt-4o-mini", Enabled: true, IsDefault: true})
	b, _ := st.Create(ctx, CreateInput{OrgID: "org-def", Kind: "text", Provider: "deepseek", Model: "deepseek-chat", Enabled: true, IsDefault: false})
	// 把 b 设为默认 → a 应被清掉默认。
	if _, err := st.Update(ctx, b.ID, "org-def", UpdateInput{
		Kind: "text", Provider: "deepseek", Model: "deepseek-chat", Enabled: true, IsDefault: true,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, _ := st.ListByOrg(ctx, "org-def")
	for _, c := range list {
		switch c.ID {
		case a.ID:
			if c.IsDefault {
				t.Fatalf("sibling %s should no longer be default", a.ID)
			}
		case b.ID:
			if !c.IsDefault {
				t.Fatalf("updated %s should be default", b.ID)
			}
		}
	}
}

func TestDeleteScopedByOrg(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	mc, err := st.Create(ctx, CreateInput{OrgID: "org-del", Kind: "text", Provider: "openai", Model: "gpt-4o-mini", Enabled: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 另一个 org 删不掉 → ErrNotFound，行仍在。
	if err := st.Delete(ctx, mc.ID, "org-del-other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org delete must return ErrNotFound, got %v", err)
	}
	if list, _ := st.ListByOrg(ctx, "org-del"); len(list) != 1 {
		t.Fatalf("cross-org delete removed the row: %+v", list)
	}
	// 正确 org 删除成功，再删 → ErrNotFound。
	if err := st.Delete(ctx, mc.ID, "org-del"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list, _ := st.ListByOrg(ctx, "org-del"); len(list) != 0 {
		t.Fatalf("delete did not remove the row: %+v", list)
	}
	if err := st.Delete(ctx, mc.ID, "org-del"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-delete must return ErrNotFound, got %v", err)
	}
}

func TestUpdateRejectsSecretParams(t *testing.T) {
	// 校验先于任何 DB 访问，故 nil pool 证明顺序 (mirrors TestCreateRejectsSecretParams)。
	s := New(nil, testBox(t))
	_, err := s.Update(context.Background(), "some-id", "o", UpdateInput{
		Provider: "p", Model: "m", Params: json.RawMessage(`{"api_key":"sk-123"}`),
	})
	if !errors.Is(err, ErrSecretParam) {
		t.Fatalf("update with credential param must return ErrSecretParam, got %v", err)
	}
}

func TestCatalogIncludesOllamaText(t *testing.T) {
	var hasOllama bool
	for _, e := range Catalog() {
		if e.Provider == "ollama" && e.Kind == "text" {
			hasOllama = true
		}
	}
	if !hasOllama {
		t.Fatal("catalog must include an ollama text entry (local chat provider)")
	}
}
