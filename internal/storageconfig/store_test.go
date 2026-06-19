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

func TestCreateForOrgRoundTrip(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	org := "org-a-" + uniqueSuffix()
	in := s3Input("sek")
	in.Name = "main"
	sc, err := st.Create(ctx, org, in)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if sc.Scope != "org" || sc.OrgID != org || !sc.HasSecret {
		t.Fatalf("create org result: %+v", sc)
	}
	// List returns our row.
	list, err := st.List(ctx, org)
	if err != nil || len(list) != 1 || list[0].OrgID != org {
		t.Fatalf("list: %v ok=%v %+v", err, len(list), list)
	}
	// Update changes region.
	in2 := s3Input("sek")
	in2.Name = "main"
	in2.Region = "eu-west-1"
	if _, err := st.Update(ctx, org, sc.ID, in2); err != nil {
		t.Fatalf("update org: %v", err)
	}
	list, _ = st.List(ctx, org)
	if len(list) != 1 {
		t.Fatalf("after update still 1 row, got %d", len(list))
	}
	if list[0].Region != "eu-west-1" {
		t.Fatalf("update must change region, got %q", list[0].Region)
	}
}

func TestUpdateKeepOrReplaceSecret(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	org := "org-k-" + uniqueSuffix()
	in := s3Input("orig-secret")
	in.Name = "k"
	sc, err := st.Create(ctx, org, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 空 Secret → 保留既有 secret_enc，改 region。
	in2 := s3Input("")
	in2.Name = "k"
	in2.Region = "ap-1"
	if _, err := st.Update(ctx, org, sc.ID, in2); err != nil {
		t.Fatalf("keep update: %v", err)
	}
	rs, ok, err := st.ResolveForOrg(ctx, org)
	if err != nil || !ok {
		t.Fatalf("resolve after keep: %v ok=%v", err, ok)
	}
	if rs.SecretKey != "orig-secret" || rs.Region != "ap-1" {
		t.Fatalf("keep: secret=%q region=%q", rs.SecretKey, rs.Region)
	}
	// 非空 Secret → 替换。
	in3 := s3Input("new-secret")
	in3.Name = "k"
	if _, err := st.Update(ctx, org, sc.ID, in3); err != nil {
		t.Fatalf("replace update: %v", err)
	}
	rs, ok, err = st.ResolveForOrg(ctx, org)
	if err != nil || !ok || rs.SecretKey != "new-secret" {
		t.Fatalf("replace: ok=%v err=%v secret=%q", ok, err, rs.SecretKey)
	}
	_ = pool // used via testPool
}

func TestListMissing(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	list, err := st.List(ctx, "org-nope-"+uniqueSuffix())
	if err != nil || len(list) != 0 {
		t.Fatalf("missing org list: err=%v len=%d", err, len(list))
	}
	_ = pool
}

func TestResolvePrecedence(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	orgP := "org-p-" + uniqueSuffix()
	// global enabled.
	gin := s3Input("global-secret")
	gin.Bucket = "global-bucket"
	if _, err := st.UpsertGlobal(ctx, gin); err != nil {
		t.Fatalf("global: %v", err)
	}
	// org-p enabled → 取 org。
	oin := s3Input("org-secret")
	oin.Bucket = "org-bucket"
	oin.Name = "p"
	orgsc, err := st.Create(ctx, orgP, oin)
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	rs, ok, rerr := st.ResolveForOrg(ctx, orgP)
	if rerr != nil || !ok || rs.Bucket != "org-bucket" || rs.SecretKey != "org-secret" {
		t.Fatalf("org-enabled should win: ok=%v err=%v %+v", ok, rerr, rs)
	}
	// org-p 禁用 → 回落 global。
	din := oin
	din.Enabled = false
	din.Secret = "" // keep secret
	if _, err := st.Update(ctx, orgP, orgsc.ID, din); err != nil {
		t.Fatalf("disable org: %v", err)
	}
	rs, ok, rerr = st.ResolveForOrg(ctx, orgP)
	if rerr != nil || !ok || rs.Bucket != "global-bucket" || rs.SecretKey != "global-secret" {
		t.Fatalf("disabled org should fall to global: ok=%v err=%v %+v", ok, rerr, rs)
	}
	// 无 org 行且无 global → ok=false。先删 org rows (via SQL since Delete guards asset refs),
	// then drop global.
	if _, err := pool.Exec(ctx, `DELETE FROM storage_configs WHERE scope='org' AND org_id=$1`, orgP); err != nil {
		t.Fatalf("delete org-p rows: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM storage_configs WHERE scope='global'`); err != nil {
		t.Fatalf("drop global: %v", err)
	}
	if _, ok, err := st.ResolveForOrg(ctx, orgP); err != nil || ok {
		t.Fatalf("no config should be ok=false: ok=%v err=%v", ok, err)
	}
}

func TestDeleteByID(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	org := "org-d-" + uniqueSuffix()
	in := s3Input("s")
	in.Name = "d"
	sc, err := st.Create(ctx, org, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.Delete(ctx, org, sc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, _ := st.List(ctx, org)
	if len(list) != 0 {
		t.Fatalf("row should be gone, list=%v", list)
	}
	if err := st.Delete(ctx, org, sc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-delete must return ErrNotFound, got %v", err)
	}
	_ = pool
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
	if _, err := st.Create(ctx, "o", noBucket); err == nil {
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
	org := "org-lfs-" + uniqueSuffix()
	in := UpsertInput{Mode: "localfs", Name: "lfs", PublicPrefix: "/files", UseSSL: true, Enabled: true}
	sc, err := st.Create(ctx, org, in)
	if err != nil {
		t.Fatalf("localfs create: %v", err)
	}
	if sc.Mode != "localfs" || sc.PublicPrefix != "/files" || sc.HasSecret {
		t.Fatalf("localfs result: %+v", sc)
	}
	rs, ok, err := st.ResolveForOrg(ctx, org)
	if err != nil || !ok || rs.Mode != "localfs" || rs.PublicPrefix != "/files" {
		t.Fatalf("resolve localfs: ok=%v err=%v %+v", ok, err, rs)
	}
	_ = pool
}

// github 列复用 round-trip：AccessKeyID=owner, Bucket=repo, Region=branch,
// PublicPrefix=path 前缀, Endpoint=GHE API 根, SecretKey=token (加密入库)。
func TestGithubModeColumnOverload(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	org := "org-gh-" + uniqueSuffix()
	in := UpsertInput{
		Mode: "github", Name: "gh", AccessKeyID: "octo", Bucket: "assets-repo", Region: "prod",
		PublicPrefix: "media", Endpoint: "https://ghe.example.com/api/v3",
		Secret: "ghp_token", Enabled: true,
	}
	sc, err := st.Create(ctx, org, in)
	if err != nil {
		t.Fatalf("github create: %v", err)
	}
	if sc.Mode != "github" || sc.AccessKeyID != "octo" || sc.Bucket != "assets-repo" || !sc.HasSecret {
		t.Fatalf("github create result: %+v", sc)
	}
	rs, ok, err := st.ResolveForOrg(ctx, org)
	if err != nil || !ok {
		t.Fatalf("resolve github: ok=%v err=%v", ok, err)
	}
	if rs.AccessKeyID != "octo" || rs.Bucket != "assets-repo" || rs.Region != "prod" ||
		rs.PublicPrefix != "media" || rs.Endpoint != "https://ghe.example.com/api/v3" || rs.SecretKey != "ghp_token" {
		t.Fatalf("github resolve column overload mismatch: %+v", rs)
	}
	_ = pool
}

func TestMultipleOrgConfigsAndResolution(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	orgID := "org-multi-" + uniqueSuffix()

	// Configure S3 for this org.
	s3In := s3Input("s3sek")
	s3In.Name = "s3"
	s3sc, err := st.Create(ctx, orgID, s3In)
	if err != nil {
		t.Fatalf("create S3: %v", err)
	}

	// Configure GitHub for this org.
	ghIn := UpsertInput{
		Mode: "github", Name: "gh", AccessKeyID: "octo", Bucket: "assets-repo", Region: "prod",
		PublicPrefix: "media", Endpoint: "https://api.github.com",
		Secret: "ghp_token", Enabled: true,
	}
	ghsc, err := st.Create(ctx, orgID, ghIn)
	if err != nil {
		t.Fatalf("create github: %v", err)
	}

	// Verify both configs exist via List.
	list, err := st.List(ctx, orgID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: err=%v len=%d want 2", err, len(list))
	}
	// Both modes present.
	var foundS3, foundGH bool
	for _, c := range list {
		if c.Mode == "s3" {
			foundS3 = true
		}
		if c.Mode == "github" {
			foundGH = true
		}
	}
	if !foundS3 || !foundGH {
		t.Fatalf("expected both modes in list: %+v", list)
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

	// Verify delete by id (github).
	if err := st.Delete(ctx, orgID, ghsc.ID); err != nil {
		t.Fatalf("delete github: %v", err)
	}

	// GitHub config should be gone, S3 config should still exist.
	list, _ = st.List(ctx, orgID)
	for _, c := range list {
		if c.ID == ghsc.ID {
			t.Fatalf("github config should be deleted")
		}
	}
	var stillS3 bool
	for _, c := range list {
		if c.ID == s3sc.ID {
			stillS3 = true
		}
	}
	if !stillS3 {
		t.Fatalf("s3 config should still exist")
	}
	_ = pool
}

// TestResolveAndConfigIDAgreement verifies the fix for the "unreadable cover" bug:
// when an org has multiple enabled configs (different modes), ResolveForOrgAndMode
// and ConfigIDForOrgAndMode MUST resolve to the same row. Without ORDER BY in both
// helper functions, two separate DB queries can return different rows → cover bytes
// land in backend X but the asset's storage_config_id points to backend Y.
//
// The test inserts TWO enabled org-scoped configs (mode="localfs" and mode="s3"),
// then verifies that the id returned by ConfigIDForOrgAndMode matches the config
// that ResolveByID returns (i.e., same mode/bucket as the one ResolveForOrgAndMode
// returned). Mismatch proves the two helpers diverged.
func TestResolveAndConfigIDAgreement(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	org := "org-agree-" + uniqueSuffix()

	// Insert localfs first (created_at earlier).
	lfsIn := UpsertInput{Mode: "localfs", Name: "lfs", PublicPrefix: "/lfs", Enabled: true}
	lfssc, err := st.Create(ctx, org, lfsIn)
	if err != nil {
		t.Fatalf("create localfs: %v", err)
	}

	// Insert s3 second (created_at later → ORDER BY created_at DESC picks this one first).
	s3In := s3Input("agree-sek")
	s3In.Name = "s3"
	s3In.Bucket = "agree-bucket"
	s3sc, err := st.Create(ctx, org, s3In)
	if err != nil {
		t.Fatalf("create s3: %v", err)
	}

	// Sanity: two distinct rows exist.
	if lfssc.ID == s3sc.ID {
		t.Fatalf("expected two distinct config ids, got same: %q", lfssc.ID)
	}

	// ResolveForOrgAndMode with mode="" resolves "any enabled" for the org.
	rs, ok, err := st.ResolveForOrgAndMode(ctx, org, "")
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}

	// ConfigIDForOrgAndMode with mode="" must return the SAME row's id.
	resolvedID, ok, err := st.ConfigIDForOrgAndMode(ctx, org, "")
	if err != nil || !ok {
		t.Fatalf("configIDForOrgAndMode: ok=%v err=%v", ok, err)
	}

	// Confirm the returned id is one of our two rows.
	if resolvedID != lfssc.ID && resolvedID != s3sc.ID {
		t.Fatalf("configIDForOrgAndMode returned unknown id %q (want %q or %q)", resolvedID, lfssc.ID, s3sc.ID)
	}

	// Fetch the full config for the id ConfigIDForOrgAndMode returned.
	refByID, ok, err := st.ResolveByID(ctx, resolvedID)
	if err != nil || !ok {
		t.Fatalf("resolveByID(%q): ok=%v err=%v", resolvedID, ok, err)
	}

	// The core assertion: the two helper functions must agree on which row to pick.
	// If they disagree, a cover written to rs.Bucket would be sought via refByID.Bucket
	// → wrong backend → unreadable cover.
	if refByID.Mode != rs.Mode || refByID.Bucket != rs.Bucket || refByID.Endpoint != rs.Endpoint {
		t.Fatalf("AGREEMENT FAILURE: ResolveForOrgAndMode returned mode=%q bucket=%q endpoint=%q "+
			"but ConfigIDForOrgAndMode resolved to mode=%q bucket=%q endpoint=%q — "+
			"the two helpers diverged; ORDER BY fix missing",
			rs.Mode, rs.Bucket, rs.Endpoint,
			refByID.Mode, refByID.Bucket, refByID.Endpoint)
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
	in.Name = "rbid"
	in.Bucket = "rbid-bucket"
	sc, err := st.Create(ctx, org, in)
	if err != nil {
		t.Fatalf("create: %v", err)
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
	if _, err := st.Update(ctx, org, sc.ID, dis); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, ok, err := st.ResolveByID(ctx, sc.ID); err != nil || ok {
		t.Fatalf("disabled id must be ok=false: ok=%v err=%v", ok, err)
	}
}

// TestResolveByIDForServe 验证 serve 路径解析「不」过滤 enabled：禁用一个配置后，
// ResolveByID → ok=false（写目标排除），但 ResolveByIDForServe → ok=true（历史
// asset 仍按写入时的后端身份可读）。修复「禁用在用存储配置 → 既有资产 404」。
func TestResolveByIDForServe(t *testing.T) {
	pool := testPool(t)
	st := New(pool, testBox(t))
	ctx := context.Background()
	org := "org-rbids-" + uniqueSuffix()
	in := s3Input("sek-rbids")
	in.Name = "rbids"
	in.Bucket = "rbids-bucket"
	sc, err := st.Create(ctx, org, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 禁用该配置。
	dis := in
	dis.Enabled = false
	dis.Secret = ""
	if _, err := st.Update(ctx, org, sc.ID, dis); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// 写目标视图：禁用 → ok=false。
	if _, ok, err := st.ResolveByID(ctx, sc.ID); err != nil || ok {
		t.Fatalf("ResolveByID on disabled must be ok=false: ok=%v err=%v", ok, err)
	}
	// serve 视图：禁用仍 ok=true，且解出后端身份（含解密 secret）完整。
	rs, ok, err := st.ResolveByIDForServe(ctx, sc.ID)
	if err != nil || !ok {
		t.Fatalf("ResolveByIDForServe on disabled must be ok=true: ok=%v err=%v", ok, err)
	}
	if rs.Bucket != "rbids-bucket" || rs.SecretKey != "sek-rbids" || rs.Mode != "s3" {
		t.Fatalf("serve resolve mismatch: %+v", rs)
	}
	// 未知 id → ok=false。
	if _, ok, err := st.ResolveByIDForServe(ctx, "nonexistent-"+uniqueSuffix()); err != nil || ok {
		t.Fatalf("unknown id for serve: ok=%v err=%v", ok, err)
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
	in.Name = "cfgid"
	sc, err := st.Create(ctx, org, in)
	if err != nil {
		t.Fatalf("create: %v", err)
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

// newStore 返回一个连接到测试 DB 的 Store。
func newStore(t *testing.T) *Store {
	t.Helper()
	pool := testPool(t)
	return New(pool, testBox(t))
}

// newStoreAndPool 返回 Store + 底层 pool（供测试直接执行 SQL）。
func newStoreAndPool(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := testPool(t)
	return New(pool, testBox(t)), pool
}

func TestMultiConfig_CreateListSetDefaultDelete(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	org := "org_mc_" + uniqueSuffix()
	a, err := s.Create(ctx, org, UpsertInput{Mode: "s3", Name: "主桶", Bucket: "b1", Endpoint: "https://e", Secret: "x", Enabled: true})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	if !a.IsDefault {
		t.Fatalf("first config must be default")
	}
	b, err := s.Create(ctx, org, UpsertInput{Mode: "s3", Name: "备桶", Bucket: "b2", Endpoint: "https://e", Secret: "x", Enabled: true})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if b.IsDefault {
		t.Fatalf("second config must not be default")
	}
	list, err := s.List(ctx, org)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	if list[0].ID != a.ID {
		t.Fatalf("default should sort first")
	}
	if err := s.SetDefault(ctx, org, b.ID); err != nil {
		t.Fatalf("setdefault: %v", err)
	}
	did, ok, _ := s.DefaultConfigID(ctx, org)
	if !ok || did != b.ID {
		t.Fatalf("default = %q want %s", did, b.ID)
	}
	if err := s.Delete(ctx, org, a.ID); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	list, _ = s.List(ctx, org)
	if len(list) != 1 {
		t.Fatalf("after delete len=%d want 1", len(list))
	}
}

func TestSetDefault_DisabledRejected(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	org := "org_mcd_" + uniqueSuffix()
	c, _ := s.Create(ctx, org, UpsertInput{Mode: "s3", Name: "x", Bucket: "b", Endpoint: "https://e", Secret: "x", Enabled: false})
	if err := s.SetDefault(ctx, org, c.ID); err == nil {
		t.Fatalf("SetDefault on disabled config must error")
	}
}

func TestDelete_GuardedByAssetRef(t *testing.T) {
	s, pool := newStoreAndPool(t)
	ctx := context.Background()
	org := "org_mcg_" + uniqueSuffix()
	c, _ := s.Create(ctx, org, UpsertInput{Mode: "s3", Name: "x", Bucket: "b", Endpoint: "https://e", Secret: "x", Enabled: true})
	pid := "p_" + uniqueSuffix()
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, org_id, name, status, created_by) VALUES ($1,$2,'p','draft','u')`, pid, org); err != nil {
		t.Fatalf("ins proj: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assets (id, project_id, storage_config_id) VALUES ($1,$2,$3)`, "a_"+uniqueSuffix(), pid, c.ID); err != nil {
		t.Fatalf("ins asset: %v", err)
	}
	if err := s.Delete(ctx, org, c.ID); err == nil {
		t.Fatalf("delete must be refused when assets reference the config")
	}
}
