package storageconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/costa92/llm-agent-studio/internal/secretbox"
	"github.com/costa92/llm-agent-studio/internal/storage"
)

// testBox 用固定 base64 32 字节密钥构造 enabled box (与 BYOK 同一把)。
func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatalf("test box: %v", err)
	}
	return b
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
	if dsn == "" {
		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run storage config store tests")
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

func s3Input(secret string) UpsertInput {
	return UpsertInput{
		Mode: "s3", Endpoint: "https://s3.example.com", Region: "us-east-1",
		Bucket: "assets", AccessKeyID: "AKID", UseSSL: true, Enabled: true, Secret: secret,
	}
}

func TestUpsertGlobalRoundTrip(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	sc, err := st.UpsertGlobal(ctx, s3Input("topsecret"))
	if err != nil {
		t.Fatalf("upsert global: %v", err)
	}
	if sc.Scope != "global" || sc.Mode != "s3" || sc.Bucket != "assets" || !sc.HasSecret {
		t.Fatalf("upsert result: %+v", sc)
	}
	got, ok, err := st.GetGlobal(ctx)
	if err != nil || !ok {
		t.Fatalf("get global: %v ok=%v", err, ok)
	}
	if got.AccessKeyID != "AKID" || got.Endpoint != "https://s3.example.com" || !got.HasSecret {
		t.Fatalf("get global: %+v", got)
	}
}

func TestUpsertGlobalIsSingleton(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	if _, err := st.UpsertGlobal(ctx, s3Input("s1")); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	in := s3Input("s2")
	in.Bucket = "assets2"
	if _, err := st.UpsertGlobal(ctx, in); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM storage_configs WHERE scope='global'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("global must be a singleton, got %d rows", n)
	}
	got, _, _ := st.GetGlobal(ctx)
	if got.Bucket != "assets2" {
		t.Fatalf("second upsert must update, bucket=%q", got.Bucket)
	}
}

func TestUpsertForOrgRoundTrip(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	sc, err := st.UpsertForOrg(ctx, "org-a", s3Input("sek"))
	if err != nil {
		t.Fatalf("upsert org: %v", err)
	}
	if sc.Scope != "org" || sc.OrgID != "org-a" || !sc.HasSecret {
		t.Fatalf("upsert org result: %+v", sc)
	}
	got, ok, err := st.GetForOrg(ctx, "org-a", "s3")
	if err != nil || !ok || got.OrgID != "org-a" {
		t.Fatalf("get org: %v ok=%v %+v", err, ok, got)
	}
	// 第二次 upsert 更新而非新增行。
	in := s3Input("sek")
	in.Region = "eu-west-1"
	if _, err := st.UpsertForOrg(ctx, "org-a", in); err != nil {
		t.Fatalf("second upsert org: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM storage_configs WHERE scope='org' AND org_id='org-a'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("per-org must be unique, got %d rows", n)
	}
	got, _, _ = st.GetForOrg(ctx, "org-a", "s3")
	if got.Region != "eu-west-1" {
		t.Fatalf("second org upsert must update, region=%q", got.Region)
	}
}

func TestUpsertKeepOrReplaceSecret(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	if _, err := st.UpsertForOrg(ctx, "org-k", s3Input("orig-secret")); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 空 Secret → 保留既有 secret_enc，改 region。
	in := s3Input("")
	in.Region = "ap-1"
	if _, err := st.UpsertForOrg(ctx, "org-k", in); err != nil {
		t.Fatalf("keep upsert: %v", err)
	}
	rs, ok, err := st.ResolveForOrg(ctx, "org-k")
	if err != nil || !ok {
		t.Fatalf("resolve after keep: %v ok=%v", err, ok)
	}
	if rs.SecretKey != "orig-secret" || rs.Region != "ap-1" {
		t.Fatalf("keep: secret=%q region=%q", rs.SecretKey, rs.Region)
	}
	// 非空 Secret → 替换。
	in2 := s3Input("new-secret")
	if _, err := st.UpsertForOrg(ctx, "org-k", in2); err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	rs, ok, err = st.ResolveForOrg(ctx, "org-k")
	if err != nil || !ok || rs.SecretKey != "new-secret" {
		t.Fatalf("replace: ok=%v err=%v secret=%q", ok, err, rs.SecretKey)
	}
}

func TestGetMissing(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	if _, ok, err := st.GetForOrg(ctx, "org-nope-"+t.Name(), ""); err != nil || ok {
		t.Fatalf("missing org: ok=%v err=%v", ok, err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	// global enabled.
	gin := s3Input("global-secret")
	gin.Bucket = "global-bucket"
	if _, err := st.UpsertGlobal(ctx, gin); err != nil {
		t.Fatalf("global: %v", err)
	}
	// org-p enabled → 取 org。
	oin := s3Input("org-secret")
	oin.Bucket = "org-bucket"
	if _, err := st.UpsertForOrg(ctx, "org-p", oin); err != nil {
		t.Fatalf("org: %v", err)
	}
	rs, ok, err := st.ResolveForOrg(ctx, "org-p")
	if err != nil || !ok || rs.Bucket != "org-bucket" || rs.SecretKey != "org-secret" {
		t.Fatalf("org-enabled should win: ok=%v err=%v %+v", ok, err, rs)
	}
	// org-p 禁用 → 回落 global。
	din := oin
	din.Enabled = false
	din.Secret = "" // keep secret
	if _, err := st.UpsertForOrg(ctx, "org-p", din); err != nil {
		t.Fatalf("disable org: %v", err)
	}
	rs, ok, err = st.ResolveForOrg(ctx, "org-p")
	if err != nil || !ok || rs.Bucket != "global-bucket" || rs.SecretKey != "global-secret" {
		t.Fatalf("disabled org should fall to global: ok=%v err=%v %+v", ok, err, rs)
	}
	// 无 org 行且无 global → ok=false。先删 global。
	if err := st.DeleteForOrg(ctx, "org-p", ""); err != nil {
		t.Fatalf("delete org-p: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM storage_configs WHERE scope='global'`); err != nil {
		t.Fatalf("drop global: %v", err)
	}
	if _, ok, err := st.ResolveForOrg(ctx, "org-p"); err != nil || ok {
		t.Fatalf("no config should be ok=false: ok=%v err=%v", ok, err)
	}
}

func TestDeleteForOrg(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	if _, err := st.UpsertForOrg(ctx, "org-d", s3Input("s")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.DeleteForOrg(ctx, "org-d", "s3"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.GetForOrg(ctx, "org-d", "s3"); ok {
		t.Fatalf("row should be gone")
	}
	if err := st.DeleteForOrg(ctx, "org-d", "s3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-delete must return ErrNotFound, got %v", err)
	}
}

func TestValidationBeforeDBAccess(t *testing.T) {
	// 校验先于任何 DB 访问 → nil pool 证明顺序。
	st := New(nil, testBox(t))
	ctx := context.Background()
	// 非法 mode。
	bad := s3Input("s")
	bad.Mode = "ftp"
	if _, err := st.UpsertGlobal(ctx, bad); err == nil {
		t.Fatalf("invalid mode must be rejected")
	}
	// s3 缺 bucket。
	noBucket := s3Input("s")
	noBucket.Bucket = ""
	if _, err := st.UpsertForOrg(ctx, "o", noBucket); err == nil {
		t.Fatalf("s3 without bucket must be rejected")
	}
	// github 列复用：缺 repo (bucket) 或 owner (accessKeyId) 必拒 (nil pool 证明
	// 校验先于 DB 访问)。
	ghMissingRepo := UpsertInput{Mode: "github", AccessKeyID: "octo", Enabled: true}
	if _, err := st.UpsertGlobal(ctx, ghMissingRepo); err == nil {
		t.Fatalf("github without repo (bucket) must be rejected")
	}
	ghMissingOwner := UpsertInput{Mode: "github", Bucket: "assets", Enabled: true}
	if _, err := st.UpsertGlobal(ctx, ghMissingOwner); err == nil {
		t.Fatalf("github without owner (accessKeyId) must be rejected")
	}
	// disabled box + 非空 secret → ErrEncUnavailable (校验/加密守卫先于 DB 访问，
	// nil pool 证明顺序)。
	disabledBoxStore := New(nil, mustDisabledBox(t))
	if _, err := disabledBoxStore.UpsertGlobal(ctx, s3Input("secret")); !errors.Is(err, ErrEncUnavailable) {
		t.Fatalf("disabled box with secret must return ErrEncUnavailable, got %v", err)
	}
}

// 真实生产事故：用户把 jsDelivr CDN 链接（costa92/article-images 的缓存前缀）填进
// github 模式的 Endpoint 字段——后端在它后面拼 /repos/.../contents/...，URL 形态错位 +
// CDN 不可写，asset 6/6 失败。save 校验必须早期拒绝（而不是落库后等 worker 默默 fallback）。
func TestGithubEndpointMustLookLikeAPIRoot(t *testing.T) {
	gh := func(endpoint string) UpsertInput {
		return UpsertInput{
			Mode: "github", AccessKeyID: "octo", Bucket: "assets",
			Endpoint: endpoint, Enabled: true,
		}
	}
	// 这些必拒（生产已知错值 + 明显不是 API 根的形态）。
	for _, bad := range []string{
		"https://cdn.jsdelivr.net/gh/costa92/article-images",
		"https://raw.githubusercontent.com",
		"https://cdn.example.com",
		"http://api.github.com",
		"not-a-url",
	} {
		if err := validate(gh(bad)); err == nil {
			t.Fatalf("github endpoint=%q must be rejected", bad)
		}
	}
	// 这些必过：默认空（走 api.github.com）+ 显式默认 + GHE 形态。
	for _, ok := range []string{
		"",
		"https://api.github.com",
		"https://api.github.com/",
		"https://ghe.example.com/api/v3",
		"https://ghe.example.com/github/api/v3",
	} {
		if err := validate(gh(ok)); err != nil {
			t.Fatalf("github endpoint=%q should pass validation, got: %v", ok, err)
		}
	}
}

func mustDisabledBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New("")
	if err != nil {
		t.Fatalf("disabled box: %v", err)
	}
	return b
}

func TestLocalfsModeNoBucketRequired(t *testing.T) {
	// localfs 不需要 bucket/endpoint，且无 secret → 入库成功 (round-trip via PG)。
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	in := UpsertInput{Mode: "localfs", PublicPrefix: "/files", UseSSL: true, Enabled: true}
	sc, err := st.UpsertForOrg(ctx, "org-lfs", in)
	if err != nil {
		t.Fatalf("localfs upsert: %v", err)
	}
	if sc.Mode != "localfs" || sc.PublicPrefix != "/files" || sc.HasSecret {
		t.Fatalf("localfs result: %+v", sc)
	}
	rs, ok, err := st.ResolveForOrg(ctx, "org-lfs")
	if err != nil || !ok || rs.Mode != "localfs" || rs.PublicPrefix != "/files" {
		t.Fatalf("resolve localfs: ok=%v err=%v %+v", ok, err, rs)
	}
}

// github 列复用 round-trip：AccessKeyID=owner, Bucket=repo, Region=branch,
// PublicPrefix=path 前缀, Endpoint=GHE API 根, SecretKey=token (加密入库)。
func TestGithubModeColumnOverload(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	in := UpsertInput{
		Mode: "github", AccessKeyID: "octo", Bucket: "assets-repo", Region: "prod",
		PublicPrefix: "media", Endpoint: "https://ghe.example.com/api/v3",
		Secret: "ghp_token", Enabled: true,
	}
	sc, err := st.UpsertForOrg(ctx, "org-gh", in)
	if err != nil {
		t.Fatalf("github upsert: %v", err)
	}
	if sc.Mode != "github" || sc.AccessKeyID != "octo" || sc.Bucket != "assets-repo" || !sc.HasSecret {
		t.Fatalf("github upsert result: %+v", sc)
	}
	rs, ok, err := st.ResolveForOrg(ctx, "org-gh")
	if err != nil || !ok {
		t.Fatalf("resolve github: ok=%v err=%v", ok, err)
	}
	if rs.AccessKeyID != "octo" || rs.Bucket != "assets-repo" || rs.Region != "prod" ||
		rs.PublicPrefix != "media" || rs.Endpoint != "https://ghe.example.com/api/v3" || rs.SecretKey != "ghp_token" {
		t.Fatalf("github resolve column overload mismatch: %+v", rs)
	}
}

func TestMultipleOrgConfigsAndResolution(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	orgID := "org-multi-" + uniqueSuffix()

	// Configure S3 for this org.
	s3In := s3Input("s3sek")
	if _, err := st.UpsertForOrg(ctx, orgID, s3In); err != nil {
		t.Fatalf("upsert S3: %v", err)
	}

	// Configure GitHub for this org.
	ghIn := UpsertInput{
		Mode: "github", AccessKeyID: "octo", Bucket: "assets-repo", Region: "prod",
		PublicPrefix: "media", Endpoint: "https://api.github.com",
		Secret: "ghp_token", Enabled: true,
	}
	if _, err := st.UpsertForOrg(ctx, orgID, ghIn); err != nil {
		t.Fatalf("upsert github: %v", err)
	}

	// Verify both configs exist and can be fetched.
	s3Config, ok, err := st.GetForOrg(ctx, orgID, "s3")
	if err != nil || !ok || s3Config.Mode != "s3" {
		t.Fatalf("get s3 config: %v %v", err, ok)
	}
	ghConfig, ok, err := st.GetForOrg(ctx, orgID, "github")
	if err != nil || !ok || ghConfig.Mode != "github" {
		t.Fatalf("get github config: %v %v", err, ok)
	}

	// Verify resolution with specific mode.
	s3Res, ok, err := st.ResolveForOrgAndMode(ctx, orgID, "s3")
	if err != nil || !ok || s3Res.Mode != "s3" || s3Res.SecretKey != "s3sek" {
		t.Fatalf("resolve s3: %v %v", err, ok)
	}

	ghRes, ok, err := st.ResolveForOrgAndMode(ctx, orgID, "github")
	if err != nil || !ok || ghRes.Mode != "github" || ghRes.SecretKey != "ghp_token" {
		t.Fatalf("resolve github: %v %v", err, ok)
	}

	// Verify delete per mode.
	if err := st.DeleteForOrg(ctx, orgID, "github"); err != nil {
		t.Fatalf("delete github: %v", err)
	}

	// GitHub config should be gone, S3 config should still exist.
	_, ok, _ = st.GetForOrg(ctx, orgID, "github")
	if ok {
		t.Fatalf("github config should be deleted")
	}
	_, ok, _ = st.GetForOrg(ctx, orgID, "s3")
	if !ok {
		t.Fatalf("s3 config should still exist")
	}
}

// Helper uniqueSuffix to avoid test pollution
func uniqueSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TestResolveByID 验证按 config id 直接解析（serve 路径用：asset 持久化的 backend
// 身份 → 解析回当时写入的后端，独立于 org 当前 storage_mode）。
func TestResolveByID(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	org := "org-rbid-" + uniqueSuffix()
	in := s3Input("sek-rbid")
	in.Bucket = "rbid-bucket"
	sc, err := st.UpsertForOrg(ctx, org, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rs, ok, err := st.ResolveByID(ctx, sc.ID)
	if err != nil || !ok {
		t.Fatalf("resolve by id: ok=%v err=%v", ok, err)
	}
	if rs.Bucket != "rbid-bucket" || rs.SecretKey != "sek-rbid" || rs.Mode != "s3" {
		t.Fatalf("resolve by id mismatch: %+v", rs)
	}
	// 未知 id → ok=false。
	if _, ok, err := st.ResolveByID(ctx, "nonexistent-"+uniqueSuffix()); err != nil || ok {
		t.Fatalf("unknown id: ok=%v err=%v", ok, err)
	}
	// disabled id → ok=false (WHERE enabled=true)。
	dis := in
	dis.Enabled = false
	dis.Secret = ""
	if _, err := st.UpsertForOrg(ctx, org, dis); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, ok, err := st.ResolveByID(ctx, sc.ID); err != nil || ok {
		t.Fatalf("disabled id must be ok=false: ok=%v err=%v", ok, err)
	}
}

// TestConfigIDForOrgAndMode 验证写路径要持久化的 token：配置后端返回其 storage_configs.id；
// 无 config 行（builtin 默认）返回 ""/ok=false，由调用方落 "builtin" sentinel。
func TestConfigIDForOrgAndMode(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	org := "org-cfgid-" + uniqueSuffix()
	in := s3Input("sek-cfgid")
	sc, err := st.UpsertForOrg(ctx, org, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	id, ok, err := st.ConfigIDForOrgAndMode(ctx, org, "s3")
	if err != nil || !ok {
		t.Fatalf("config id for mode: ok=%v err=%v", ok, err)
	}
	if id != sc.ID {
		t.Fatalf("want config id %q, got %q", sc.ID, id)
	}
	// 无匹配 config → ok=false (builtin)。
	if _, ok, err := st.ConfigIDForOrgAndMode(ctx, org, "localfs"); err != nil || ok {
		t.Fatalf("no config for mode must be ok=false: ok=%v err=%v", ok, err)
	}
	// org 无任何 config 的另一 mode → ok=false。
	if _, ok, err := st.ConfigIDForOrgAndMode(ctx, "org-none-"+uniqueSuffix(), "s3"); err != nil || ok {
		t.Fatalf("unknown org must be ok=false: ok=%v err=%v", ok, err)
	}
}
