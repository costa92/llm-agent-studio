# Custom Nodes Phase 2B Implementation Plan

**For agentic workers: READ AND FOLLOW THE REQUIRED SUB-SKILL `superpowers:executing-plans` BEFORE STARTING. Execute tasks in order; one commit per task; run the verification command and confirm its output before moving on.**

## Goal

Add an `http` execution **kind** to the Phase 2A custom-node framework, plus an
org-level **named secret store** (`org_secrets`) to authenticate those requests.
`runCustomHTTP` resolves `{{name}}` upstream variables (reusing A's varBindings)
and `{{secret:NAME}}` org-scoped secrets, substitutes them into headers/body,
makes an SSRF-safe outbound request via the existing `internal/fetch` transport,
and lands the result in `node_outputs`. The whole feature is a single deliverable
but B0 (the secret store) ships first and gets an independent security review.

This plan is **security-load-bearing**. Every decision in
`docs/superpowers/specs/2026-06-23-custom-nodes-phase2b-http-design.md` (the
authoritative spec) is preserved verbatim below; it turns that spec into
executable tasks and does NOT redesign. Read the spec's 安全 (Security) section
before starting — the secret-leak oracle reasoning is the reason for the
admin-gate + default body-suppression + opaque-error-allowlist machinery.

## Architecture

- **`http` is a new `kind`** in the existing `runCustom` switch
  (`internal/worker/worker.go:1505-1511`, `default:` returns "unsupported custom
  kind"). Adding `case "http": return w.runCustomHTTP(...)` is the extension
  point — same shape as A's `case "llm"`.
- **Per-kind decode (refactor first).** `runCustom` currently unmarshals the
  whole input into `customInput` (an llm-shaped struct, worker.go:1483-1496).
  B2's first task refactors `runCustom` to decode only `{kind, params:
  json.RawMessage}`, then each `case` re-unmarshals its own typed params struct.
  The llm path gets its own `llmParams` struct so it is behaviorally unchanged
  (guarded by a regression test).
- **`org_secrets` is a NEW named-secret registry**, NOT a 1:1 owner column. It
  mirrors `storageconfig`'s secret DISCIPLINE (keep-or-replace, `HasSecret` DTO,
  `ErrEncUnavailable`, org-scoped reads, INSERT…RETURNING via `db.Raw`) — but it
  has NO delete-in-use guard (spec: `{{secret:NAME}}` is free-text, no
  structured FK; missing secret at run time → opaque failure).
- **Two placeholder channels** (spec §`http` kind 参数):
  - `{{name}}` = upstream variable, reuses A's `varBindings` + `substituteVars`.
    Allowed in **header values + bodyTemplate**; FORBIDDEN in url.
  - `{{secret:NAME}}` = org named-secret reference. SEPARATE substitution channel
    (NOT `substituteVars`), resolved at run time from the trusted run context's
    org. Allowed in **header values ONLY**; forbidden in url/body. Enforced at
    save-time `validate()` AND after substitution.
- **org-scoped secret resolution from trusted context** (spec 裁决 #1): resolve
  `{{secret:NAME}}` with `w.cfg.Projects.OrgIDForProject(ctx, projectID)`
  (worker.go:957/project store.go:205), `WHERE org_id=$1 AND name=$2`. NEVER read
  org from `input_json` / the node.
- **secret-bearing http TYPE create/update requires roleAdmin** (spec 裁决 #2):
  the `custom-node-types` create/update handler parses http params; if any header
  value contains `{{secret:...}}` it additionally requires the caller be
  `AtLeast(roleAdmin)`. Because `RequireScopeRole` (authz middleware.go:54-75)
  does NOT stash the resolved role in ctx — only `UserID(ctx)` is available — the
  handler must call `d.RoleResolver.ResolveRole(ctx, UserID(ctx), org, "org", "")`
  itself, then `eff.AtLeast(roleAdmin)`. Non-admin → 403. Routes stay mounted at
  `roleEditor`; the handler does the conditional upgrade.
- **default response-body suppression** (spec 裁决 #3): `runCustomHTTP` records a
  `secretBearing` bool in the secret-resolution channel (true iff ANY secret was
  injected — not by re-scanning headers). `secretBearing && !allowResponseBody` →
  `node_outputs` stores only `{"status":<code>}` with `format="http-status"`; the
  body/headers/echo are not stored. `allowResponseBody:true` (an org-level type
  param, admin-attested) re-enables body landing. Non-secret requests land body
  normally (`text|json`). `allowResponseBody` is a TYPE param — **NO new per-node
  field**, so T1 (`toStudioNodes`) does NOT change.
- **Reuse `internal/fetch` transport + two hardenings** (spec 必做项 #6): B1 adds
  a general `Do(ctx, Request) (Response, error)` method reusing the private
  `client`/`resolveAndValidate` transport (the existing `Get` is GET-only,
  hardcodes `http.MethodGet`, errors on non-2xx, and formats the raw URL into
  errors — unusable for http kind). Hardenings: NAT64 `64:ff9b::/96` block +
  IPv4-mapped `To4()` normalization in `isBlockedIP`; per-hop redirect host
  re-validation + Authorization/secret-header strip on host change in
  `CheckRedirect`.
- **opaque error allowlist** (spec 必做项 #4): http kind errors are a fixed enum
  (`request_failed`/`host_not_allowed`/`timeout`/`body_too_large`/
  `blocked_destination`). NEVER `%w`/`%v` a resolved secret, url, headers, or
  body — because `fail()` (worker.go:908-921) ships `cause.Error()` to the
  frontend via `todo_failed` SSE + `projectstate.ProblemError.Message`, and
  `node_outputs.content` streams to viewers via `/state`.
- **no logging/OTel of url/headers/body** (spec 必做项 #7): emit only node id,
  method, host (only if allowed), status, byte count, duration.
- **box-disabled → refuse, not silent** (spec 必做项 #8): a secret-needing request
  when `box.Enabled()==false` → opaque execution failure (no unauthenticated
  request sent); a missing secret name → opaque failure (no name leaked).

## Tech Stack

- Backend: Go (stdlib `net/http`, GORM over pgxpool), Postgres. `GOWORK=off` on
  EVERY go command (umbrella `go.work` would mask the standalone studio module).
- Crypto: `internal/secretbox` (AES-256-GCM, env `STUDIO_CONFIG_ENC_KEY`); the
  same `*secretbox.Box` already constructed at `cmd/studiod/main.go:196`.
- SSRF-safe outbound: `internal/fetch` (private transport dials the validated IP;
  scheme allowlist; per-hop redirect re-validation; body cap; content-type
  allowlist — set empty for http kind to allow any type).
- Worker LLM (unchanged llm path): `coreagents.NewSimpleAgent` +
  `agent.Run(ctx, userPrompt)`.
- Frontend: React + TypeScript, ReactFlow (`@xyflow/react`), react-query, vitest.
  Web tests: `cd web && npx vitest run`.
- DB-gated Go tests: skip unless `LLM_AGENT_STUDIO_PG_URL` set; use a FRESH DB and
  `-p 1` per repo convention (parallel migrate race + stale-data uniqueness
  collisions are documented hazards). Use:
  `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_p2b_<rand>?sslmode=disable`
  (pick a fresh `<rand>` per run; `createdb` it first).

---

## File Structure

| File | Create/Modify | Responsibility |
|------|---------------|----------------|
| `internal/storage/storage.go` | Modify | Add `m19Migrations` (`org_secrets`); append to `Migrate`. |
| `internal/orgsecret/store.go` | Create | Org-scoped named-secret CRUD; keep-or-replace; `HasSecret` DTO; `ErrEncUnavailable`/`ErrNotFound`; internal `Resolve` (decrypt). NO delete-in-use guard. |
| `internal/orgsecret/store_test.go` | Create | DB-gated CRUD / org-isolation / name-uniqueness / keep-or-replace / box-disabled / DTO-no-plaintext / Resolve org-scoped tests. |
| `internal/httpapi/orgsecrethandlers.go` | Create | `OrgSecretStore` iface + CRUD handlers (roleAdmin). |
| `internal/httpapi/orgsecrethandlers_test.go` | Create | Handler-shape CRUD + DTO-never-leaks tests (fake store). |
| `internal/httpapi/httpapi.go` | Modify | Add `OrgSecret OrgSecretStore` to `Deps`; register routes at `roleAdmin`; pass `RoleResolver` to custom-node-types create/update handlers. |
| `internal/httpapi/customnodetypehandlers.go` | Modify | http param `validate` + secret-bearing → in-handler `ResolveRole` admin gate. |
| `internal/httpapi/customnodetypehandlers_test.go` | Modify | secret-bearing create/update non-admin→403, admin→ok; non-secret→editor ok. |
| `cmd/studiod/main.go` | Modify | Construct `orgsecret.Store`; wire into `httpapi.Deps`; pass secretbox + a secret resolver into `worker.Config`. |
| `internal/fetch/fetch.go` | Modify | Add `Request`/`Response` + `Do(ctx, Request)`; NAT64 + IPv4-mapped in `isBlockedIP`; per-hop redirect host re-validation + Authorization strip in `CheckRedirect`. |
| `internal/fetch/fetch_test.go` | Modify/Create | `Do` happy path; SSRF block matrix (loopback/RFC1918/169.254/NAT64/IPv4-mapped/decimal); redirect host-change strips Authorization; body cap; non-2xx returns status (no error). |
| `internal/customnodetype/store.go` | Modify | `validKinds["http"]=true`; http param `validate()` (method enum, url no-template, `{{secret}}` headers-only, outputFormat enum). |
| `internal/customnodetype/store_test.go` | Modify | http validate cases. |
| `internal/worker/worker.go` | Modify | Refactor `runCustom` to `{kind, params:RawMessage}`; `llmParams` for llm path; add `runCustomHTTP` + `httpParams` + secret resolver field + opaque-error enum + http-status suppression. |
| `internal/worker/worker_custom_test.go` | Modify | runCustom llm regression; runCustomHTTP substitution/SSRF/redirect-strip/body-policy; secret-never-leaks matrix. |
| `web/src/lib/types.ts` | Modify | `HttpParams` type; extend `CustomNodeType.kind`/`UpsertCustomNodeTypeInput`; `OrgSecret` DTO. |
| `web/src/features/org-secrets/api.ts` | Create | react-query hooks for org_secrets CRUD. |
| `web/src/features/org-secrets/OrgSecretManager.tsx` (+ test) | Create | roleAdmin secret manager (name + write-only value; list shows {name, hasValue}). |
| `web/src/features/custom-node-types/HttpParamForm.tsx` (+ test) | Create | method/url(no-template)/headers(value can insert {{name}}/{{secret:NAME}})/bodyTemplate/outputFormat/allowResponseBody. |
| `web/src/features/custom-node-types/CustomNodeTypeManager.tsx` | Modify | kind switch llm|http; render `HttpParamForm` for http; non-admin disabled for secret-bearing. |
| `web/src/features/workflow-canvas/*` | Modify | http typed nodes coexist with llm (registry same column); run-view suppressed-body label. |

---

## B0 — `org_secrets` store + CRUD + worker secretbox wiring (ships first, independent security review)

### Task B0.1: Migration — `org_secrets` table (m19)

**Files:**
- Modify: `internal/storage/storage.go` (add `m19Migrations` after `m18Migrations` ~line 478; extend `Migrate` ~line 480-483)

- [ ] In `internal/storage/storage.go`, immediately after the `m18Migrations`
  slice (ends ~line 478) add:
  ```go
  // m19Migrations 建 org_secrets (组织级命名密钥注册表)：value_enc 是 AES-256-GCM 密文
  // (secretbox)，永不出服务端。被 http 自定义节点的 {{secret:NAME}} 自由文本引用。
  // 唯一索引 (org_id, name)。无 delete-in-use 守卫 (自由文本引用无结构化 FK；执行时缺
  // 密钥 → 不透明失败，见 spec 安全节)。additive only。
  var m19Migrations = []string{
  	`CREATE TABLE IF NOT EXISTS org_secrets (
  		id TEXT PRIMARY KEY,
  		org_id TEXT NOT NULL,
  		name TEXT NOT NULL,
  		value_enc BYTEA NOT NULL,
  		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
  	)`,
  	`CREATE UNIQUE INDEX IF NOT EXISTS org_secrets_org_name_uniq ON org_secrets (org_id, name)`,
  }
  ```
- [ ] Update the `Migrate` doc comment (line ~480) to append `+ M19`.
- [ ] In `Migrate` (line ~482-483), append `m19Migrations...` to the `all` chain
  (add one more `append(` wrapper and `, m19Migrations...)` at the tail):
  ```go
  	all := append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append(append([]string{},
  		m1Migrations...), m2Migrations...), m3Migrations...), m4Migrations...), m5Migrations...), m6Migrations...), m7Migrations...), m8Migrations...), m9Migrations...), m10Migrations...), m11Migrations...), m12Migrations...), m13Migrations...), m14Migrations...), m15Migrations...), m16Migrations...), m17Migrations...), m18Migrations...), m19Migrations...)
  ```
- [ ] Run: `GOWORK=off go build ./internal/storage/...`
  - Expected: builds clean, no output.
- [ ] Commit: `feat(storage): add org_secrets named-secret table (m19)`

---

### Task B0.2: `internal/orgsecret/store.go` — org-scoped named-secret store

TDD: write the failing DB-gated store test first, then the store. Mirror
`internal/storageconfig/store.go` for the secret DISCIPLINE (keep-or-replace,
`HasSecret` DTO, `ErrEncUnavailable`, org-scoped reads) — but NO delete-in-use
guard.

**Files:**
- Create: `internal/orgsecret/store_test.go`
- Create: `internal/orgsecret/store.go`

- [ ] Create `internal/orgsecret/store_test.go` mirroring
  `internal/customnodetype/store_test.go` harness (skip if `LLM_AGENT_STUDIO_PG_URL`
  unset; open + migrate; return `*gorm.DB`). Use a unique org id per test:
  ```go
  package orgsecret

  import (
  	"context"
  	"crypto/rand"
  	"encoding/hex"
  	"errors"
  	"os"
  	"testing"

  	"gorm.io/gorm"

  	"github.com/costa92/llm-agent-studio/internal/secretbox"
  	"github.com/costa92/llm-agent-studio/internal/storage"
  )

  func testGorm(t *testing.T) *gorm.DB {
  	t.Helper()
  	dsn := os.Getenv("LLM_AGENT_STUDIO_PG_URL")
  	if dsn == "" {
  		t.Skipf("set LLM_AGENT_STUDIO_PG_URL to run org secret store tests")
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
  	return st.GORM()
  }

  func randID(t *testing.T) string {
  	t.Helper()
  	b := make([]byte, 8)
  	_, _ = rand.Read(b)
  	return hex.EncodeToString(b)
  }

  // enabledBox builds a real AES-256-GCM box from a fixed 32-byte key (base64).
  func enabledBox(t *testing.T) *secretbox.Box {
  	t.Helper()
  	// 32 zero bytes, base64. Deterministic key is fine for tests (no secret value asserted plaintext-equal cross-process).
  	box, err := secretbox.New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
  	if err != nil {
  		t.Fatalf("box: %v", err)
  	}
  	return box
  }

  func TestCreateListGet_NoPlaintext(t *testing.T) {
  	db := testGorm(t)
  	org := randID(t)
  	s := New(db, enabledBox(t))
  	sec, err := s.Create(context.Background(), org, UpsertInput{Name: "PARTNER_KEY", Value: "topsecret"})
  	if err != nil {
  		t.Fatalf("create: %v", err)
  	}
  	if sec.Name != "PARTNER_KEY" || !sec.HasValue || sec.OrgID != org {
  		t.Fatalf("bad DTO: %+v", sec)
  	}
  	items, err := s.List(context.Background(), org)
  	if err != nil || len(items) != 1 {
  		t.Fatalf("list: %v len=%d", err, len(items))
  	}
  	// DTO must never carry the plaintext OR ciphertext.
  	if items[0].Value != "" { // OrgSecret has NO Value field; this line documents intent — remove if it won't compile.
  		t.Fatalf("DTO leaked value")
  	}
  }

  func TestKeepOrReplace(t *testing.T) {
  	db := testGorm(t)
  	org := randID(t)
  	s := New(db, enabledBox(t))
  	_, _ = s.Create(context.Background(), org, UpsertInput{Name: "K", Value: "v1"})
  	// Empty value on update keeps the existing ciphertext.
  	if _, err := s.Update(context.Background(), org, "K", UpsertInput{Name: "K", Value: ""}); err != nil {
  		t.Fatalf("update keep: %v", err)
  	}
  	got, err := s.Resolve(context.Background(), org, "K")
  	if err != nil || got != "v1" {
  		t.Fatalf("resolve after keep = %q err=%v (want v1)", got, err)
  	}
  	// Non-empty value replaces.
  	if _, err := s.Update(context.Background(), org, "K", UpsertInput{Name: "K", Value: "v2"}); err != nil {
  		t.Fatalf("update replace: %v", err)
  	}
  	got, _ = s.Resolve(context.Background(), org, "K")
  	if got != "v2" {
  		t.Fatalf("resolve after replace = %q (want v2)", got)
  	}
  }

  func TestOrgIsolation(t *testing.T) {
  	db := testGorm(t)
  	orgA, orgB := randID(t), randID(t)
  	s := New(db, enabledBox(t))
  	_, _ = s.Create(context.Background(), orgA, UpsertInput{Name: "S", Value: "v"})
  	// Cross-org resolve fails (does not leak the name's existence beyond ErrNotFound).
  	if _, err := s.Resolve(context.Background(), orgB, "S"); !errors.Is(err, ErrNotFound) {
  		t.Fatalf("cross-org resolve should be ErrNotFound, got %v", err)
  	}
  	if _, err := s.Update(context.Background(), orgB, "S", UpsertInput{Name: "S", Value: "x"}); !errors.Is(err, ErrNotFound) {
  		t.Fatalf("cross-org update should be ErrNotFound, got %v", err)
  	}
  	if err := s.Delete(context.Background(), orgB, "S"); !errors.Is(err, ErrNotFound) {
  		t.Fatalf("cross-org delete should be ErrNotFound, got %v", err)
  	}
  }

  func TestNameUnique(t *testing.T) {
  	db := testGorm(t)
  	org := randID(t)
  	s := New(db, enabledBox(t))
  	if _, err := s.Create(context.Background(), org, UpsertInput{Name: "DUP", Value: "a"}); err != nil {
  		t.Fatalf("first: %v", err)
  	}
  	if _, err := s.Create(context.Background(), org, UpsertInput{Name: "DUP", Value: "b"}); err == nil {
  		t.Fatalf("duplicate name should fail")
  	}
  }

  func TestBoxDisabled_Refuses(t *testing.T) {
  	db := testGorm(t)
  	org := randID(t)
  	disabled, _ := secretbox.New("") // disabled box
  	s := New(db, disabled)
  	if _, err := s.Create(context.Background(), org, UpsertInput{Name: "N", Value: "v"}); !errors.Is(err, ErrEncUnavailable) {
  		t.Fatalf("box-disabled create should be ErrEncUnavailable, got %v", err)
  	}
  }
  ```
  (Remove the `items[0].Value` assertion line if the `OrgSecret` DTO has no such
  field — the point is enforced by the struct having no value field at all.)
- [ ] Run (expect FAIL — package doesn't compile): `GOWORK=off go test ./internal/orgsecret/... -run TestCreateListGet_NoPlaintext -count=1`
  - Expected: build error `undefined: New` / `no Go files`.
- [ ] Create `internal/orgsecret/store.go`:
  ```go
  // Package orgsecret owns org_secrets CRUD: 组织级命名密钥注册表。value 走 AES-256-GCM
  // 静态加密入库 (value_enc BYTEA)，与 BYOK/storageconfig 同一把 secretbox。永不暴露
  // 明文：公开 DTO 只回 {name, hasValue}；明文仅 Resolve 内部可见 (供 worker 注入 http
  // 请求 header，绝不进 HTTP handler)。组织隔离贯穿全部读写 (WHERE org_id=$N)。无
  // delete-in-use 守卫 ({{secret:NAME}} 自由文本引用无结构化 FK)。需独立安全评审。
  package orgsecret

  import (
  	"context"
  	"crypto/rand"
  	"database/sql"
  	"encoding/hex"
  	"errors"
  	"fmt"
  	"strings"

  	"gorm.io/gorm"

  	"github.com/costa92/llm-agent-studio/internal/secretbox"
  )

  // ErrEncUnavailable 表示请求存储 secret，但加密 box 未启用 (未配置 STUDIO_CONFIG_ENC_KEY)，
  // 无法静态加密，故拒绝 (不静默丢弃/存明文)。
  var ErrEncUnavailable = errors.New("orgsecret: secret storage requires STUDIO_CONFIG_ENC_KEY")

  // ErrNotFound 表示按 org 定位的密钥不存在 (含跨租户访问被拒)。
  var ErrNotFound = errors.New("orgsecret: secret not found")

  // OrgSecret 是 org_secrets 行的公开 DTO。永不暴露 value：只回 name + hasValue。
  type OrgSecret struct {
  	ID       string `json:"id"`
  	OrgID    string `json:"orgId"`
  	Name     string `json:"name"`
  	HasValue bool   `json:"hasValue"`
  }

  // UpsertInput 是 Create/Update 入参。Value 走 keep-or-replace：空=保留既有 value_enc；
  // 非空=重新加密替换 (box 未启用 → ErrEncUnavailable)。
  type UpsertInput struct {
  	Name  string
  	Value string // write-only：空=保留既有；非空=重新加密替换
  }

  // Store persists org_secrets.
  type Store struct {
  	db  *gorm.DB
  	box *secretbox.Box
  }

  // New builds a Store. box 提供 value 的静态加解密；nil/disabled box → 带非空 Value 的
  // Upsert 返回 ErrEncUnavailable，Resolve 返回 ErrEncUnavailable。
  func New(db *gorm.DB, box *secretbox.Box) *Store { return &Store{db: db, box: box} }

  func newID() string {
  	b := make([]byte, 16)
  	_, _ = rand.Read(b)
  	return hex.EncodeToString(b)
  }

  func validateName(name string) error {
  	if strings.TrimSpace(name) == "" {
  		return fmt.Errorf("orgsecret: name required")
  	}
  	return nil
  }

  // encryptValue 返回 keep-or-replace 用的 (replace, enc, err)。Value 非空但 box 未启用
  // → ErrEncUnavailable。
  func (s *Store) encryptValue(value string) (replace bool, enc []byte, err error) {
  	if value == "" {
  		return false, nil, nil
  	}
  	if !s.box.Enabled() {
  		return false, nil, ErrEncUnavailable
  	}
  	ct, err := s.box.Encrypt([]byte(value))
  	if err != nil {
  		return false, nil, fmt.Errorf("orgsecret: encrypt: %w", err)
  	}
  	return true, ct, nil
  }

  func scanSecret(row interface{ Scan(...any) error }) (OrgSecret, error) {
  	var sec OrgSecret
  	if err := row.Scan(&sec.ID, &sec.OrgID, &sec.Name, &sec.HasValue); err != nil {
  		return OrgSecret{}, err
  	}
  	return sec, nil
  }

  // List 返回 org 的全部命名密钥 (名序)。只回 {name, hasValue}，绝不带 value_enc。
  func (s *Store) List(ctx context.Context, orgID string) ([]OrgSecret, error) {
  	rows, err := s.db.WithContext(ctx).Raw(
  		`SELECT id, org_id, name, (value_enc IS NOT NULL) AS has_value
  		 FROM org_secrets WHERE org_id=$1 ORDER BY name ASC`, orgID).Rows()
  	if err != nil {
  		return nil, fmt.Errorf("orgsecret: list: %w", err)
  	}
  	defer rows.Close()
  	out := []OrgSecret{}
  	for rows.Next() {
  		sec, err := scanSecret(rows)
  		if err != nil {
  			return nil, err
  		}
  		out = append(out, sec)
  	}
  	return out, rows.Err()
  }

  // Create 插入一条新命名密钥 (INSERT…RETURNING，纯 $N)。value_enc 必填 (非空 Value)。
  func (s *Store) Create(ctx context.Context, orgID string, in UpsertInput) (OrgSecret, error) {
  	if orgID == "" {
  		return OrgSecret{}, fmt.Errorf("orgsecret: orgID required")
  	}
  	if err := validateName(in.Name); err != nil {
  		return OrgSecret{}, err
  	}
  	if in.Value == "" {
  		return OrgSecret{}, fmt.Errorf("orgsecret: value required on create")
  	}
  	_, enc, err := s.encryptValue(in.Value)
  	if err != nil {
  		return OrgSecret{}, err
  	}
  	const q = `
  		INSERT INTO org_secrets (id, org_id, name, value_enc)
  		VALUES ($1,$2,$3,$4)
  		RETURNING id, org_id, name, (value_enc IS NOT NULL)`
  	row := s.db.WithContext(ctx).Raw(q, newID(), orgID, in.Name, enc).Row()
  	sec, err := scanSecret(row)
  	if err != nil {
  		return OrgSecret{}, fmt.Errorf("orgsecret: create: %w", err)
  	}
  	return sec, nil
  }

  // Update 按 (org, name) keep-or-replace value (空=保留既有 value_enc)。跨租户/不存在
  // → ErrNotFound。
  func (s *Store) Update(ctx context.Context, orgID, name string, in UpsertInput) (OrgSecret, error) {
  	if orgID == "" || name == "" {
  		return OrgSecret{}, fmt.Errorf("orgsecret: orgID+name required")
  	}
  	replace, enc, err := s.encryptValue(in.Value)
  	if err != nil {
  		return OrgSecret{}, err
  	}
  	const q = `
  		UPDATE org_secrets SET
  			value_enc=CASE WHEN $3 THEN $4 ELSE value_enc END,
  			updated_at=now()
  		WHERE org_id=$1 AND name=$2
  		RETURNING id, org_id, name, (value_enc IS NOT NULL)`
  	row := s.db.WithContext(ctx).Raw(q, orgID, name, replace, enc).Row()
  	sec, err := scanSecret(row)
  	if errors.Is(err, sql.ErrNoRows) {
  		return OrgSecret{}, ErrNotFound
  	}
  	if err != nil {
  		return OrgSecret{}, fmt.Errorf("orgsecret: update: %w", err)
  	}
  	return sec, nil
  }

  // Delete 按 (org, name) 删除。无 in-use 守卫 (spec 非目标)。跨租户/不存在 → ErrNotFound。
  func (s *Store) Delete(ctx context.Context, orgID, name string) error {
  	if orgID == "" || name == "" {
  		return fmt.Errorf("orgsecret: orgID+name required")
  	}
  	res := s.db.WithContext(ctx).Exec(`DELETE FROM org_secrets WHERE org_id=$1 AND name=$2`, orgID, name)
  	if res.Error != nil {
  		return fmt.Errorf("orgsecret: delete: %w", res.Error)
  	}
  	if res.RowsAffected == 0 {
  		return ErrNotFound
  	}
  	return nil
  }

  // Resolve 是唯一暴露明文 value 的路径，仅 worker 内部调用 (绝不进 HTTP handler)。按
  // (org, name) 读 value_enc 并解密。不存在/跨租户 → ErrNotFound (绝不在错误里带 name 以外
  // 的信息)；box 未启用 → ErrEncUnavailable。
  func (s *Store) Resolve(ctx context.Context, orgID, name string) (string, error) {
  	if orgID == "" || name == "" {
  		return "", ErrNotFound
  	}
  	var enc []byte
  	err := s.db.WithContext(ctx).Raw(
  		`SELECT value_enc FROM org_secrets WHERE org_id=$1 AND name=$2`, orgID, name).Row().Scan(&enc)
  	if errors.Is(err, sql.ErrNoRows) {
  		return "", ErrNotFound
  	}
  	if err != nil {
  		return "", fmt.Errorf("orgsecret: resolve: %w", err)
  	}
  	if len(enc) == 0 {
  		return "", ErrNotFound
  	}
  	if !s.box.Enabled() {
  		return "", ErrEncUnavailable
  	}
  	pt, err := s.box.Decrypt(enc)
  	if err != nil {
  		return "", fmt.Errorf("orgsecret: decrypt: %w", err)
  	}
  	return string(pt), nil
  }
  ```
- [ ] Run (fresh DB): `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_p2b_<rand>?sslmode=disable GOWORK=off go test ./internal/orgsecret/... -count=1 -p 1`
  - Expected: `ok ... internal/orgsecret` (or SKIP without the env var; ensure it
    passes against a fresh DB).
- [ ] Commit: `feat(orgsecret): org-scoped named-secret store (keep-or-replace, HasValue DTO)`

---

### Task B0.3: `org_secrets` CRUD HTTP handlers (roleAdmin)

TDD: handler test first (in-package fake store; no DB needed for handler-shape).

**Files:**
- Create: `internal/httpapi/orgsecrethandlers.go`
- Create: `internal/httpapi/orgsecrethandlers_test.go`
- Modify: `internal/httpapi/httpapi.go` (Deps + routes)

- [ ] Create `internal/httpapi/orgsecrethandlers.go` mirroring
  `customnodetypehandlers.go` (CRUD; secret value is write-only; DTO never carries
  it). Note `Update`/`Delete` key on `{name}` (the secret's natural key), not an
  opaque id, to match the store signature:
  ```go
  package httpapi

  import (
  	"context"
  	"encoding/json"
  	"errors"
  	"net/http"

  	"github.com/costa92/llm-agent-studio/internal/orgsecret"
  )

  // OrgSecretStore is the org_secrets HTTP surface (satisfied by *orgsecret.Store).
  // It deliberately does NOT expose Resolve — plaintext never reaches an HTTP handler.
  type OrgSecretStore interface {
  	List(ctx context.Context, orgID string) ([]orgsecret.OrgSecret, error)
  	Create(ctx context.Context, orgID string, in orgsecret.UpsertInput) (orgsecret.OrgSecret, error)
  	Update(ctx context.Context, orgID, name string, in orgsecret.UpsertInput) (orgsecret.OrgSecret, error)
  	Delete(ctx context.Context, orgID, name string) error
  }

  type orgSecretBody struct {
  	Name  string `json:"name"`
  	Value string `json:"value"` // write-only：响应里绝不回显
  }

  func (b orgSecretBody) toInput() orgsecret.UpsertInput {
  	return orgsecret.UpsertInput{Name: b.Name, Value: b.Value}
  }

  func listOrgSecretsHandler(s OrgSecretStore) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		items, err := s.List(r.Context(), r.PathValue("org"))
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusInternalServerError)
  			return
  		}
  		if items == nil {
  			items = []orgsecret.OrgSecret{}
  		}
  		writeJSON(w, http.StatusOK, map[string]any{"items": items})
  	}
  }

  func createOrgSecretHandler(s OrgSecretStore) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		var b orgSecretBody
  		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
  			http.Error(w, "bad request", http.StatusBadRequest)
  			return
  		}
  		sec, err := s.Create(r.Context(), r.PathValue("org"), b.toInput())
  		if errors.Is(err, orgsecret.ErrEncUnavailable) {
  			http.Error(w, "未配置加密主密钥 (STUDIO_CONFIG_ENC_KEY)，无法存储密钥", http.StatusBadRequest)
  			return
  		}
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusBadRequest)
  			return
  		}
  		writeJSON(w, http.StatusOK, sec) // sec is {id, orgId, name, hasValue} only
  	}
  }

  func updateOrgSecretHandler(s OrgSecretStore) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		var b orgSecretBody
  		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
  			http.Error(w, "bad request", http.StatusBadRequest)
  			return
  		}
  		sec, err := s.Update(r.Context(), r.PathValue("org"), r.PathValue("name"), b.toInput())
  		if errors.Is(err, orgsecret.ErrNotFound) {
  			http.Error(w, "not found", http.StatusNotFound)
  			return
  		}
  		if errors.Is(err, orgsecret.ErrEncUnavailable) {
  			http.Error(w, "未配置加密主密钥 (STUDIO_CONFIG_ENC_KEY)，无法存储密钥", http.StatusBadRequest)
  			return
  		}
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusBadRequest)
  			return
  		}
  		writeJSON(w, http.StatusOK, sec)
  	}
  }

  func deleteOrgSecretHandler(s OrgSecretStore) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		err := s.Delete(r.Context(), r.PathValue("org"), r.PathValue("name"))
  		if errors.Is(err, orgsecret.ErrNotFound) {
  			http.Error(w, "not found", http.StatusNotFound)
  			return
  		}
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusInternalServerError)
  			return
  		}
  		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
  	}
  }
  ```
- [ ] Create `internal/httpapi/orgsecrethandlers_test.go`: in-package fake
  `OrgSecretStore`; assert `POST` returns `{id, orgId, name, hasValue}` and that
  the response JSON body does NOT contain the submitted value string (`go test`
  marshal the response and `strings.Contains(body, "topsecret")` must be false);
  `Update` mapping `ErrNotFound`→404, `ErrEncUnavailable`→400; `Delete`
  `ErrNotFound`→404, ok→200. Mirror existing `internal/httpapi/*_test.go` request
  construction (httptest.NewRequest + the handler directly — no auth middleware
  in the focused handler test).
- [ ] In `httpapi.go`, add to `Deps` (after `CustomNodeType`, ~line 59):
  ```go
  	OrgSecret OrgSecretStore // org-scoped named-secret registry (roleAdmin); nil in focused unit tests
  ```
- [ ] In `httpapi.go`, after the `custom-node-types` route block (~line 234-239),
  register the secret routes at `roleAdmin` (mirrors model/storage configs —
  secret-bearing org resources):
  ```go
  	if d.OrgSecret != nil {
  		mux.Handle("GET /api/orgs/{org}/secrets", scoped(roleAdmin, orgScope, listOrgSecretsHandler(d.OrgSecret)))
  		mux.Handle("POST /api/orgs/{org}/secrets", scoped(roleAdmin, orgScope, createOrgSecretHandler(d.OrgSecret)))
  		mux.Handle("PUT /api/orgs/{org}/secrets/{name}", scoped(roleAdmin, orgScope, updateOrgSecretHandler(d.OrgSecret)))
  		mux.Handle("DELETE /api/orgs/{org}/secrets/{name}", scoped(roleAdmin, orgScope, deleteOrgSecretHandler(d.OrgSecret)))
  	}
  ```
- [ ] Run: `GOWORK=off go test ./internal/httpapi/... -count=1`
  - Expected: `ok` (focused handler tests; secret-value-absent assertion green).
- [ ] Commit: `feat(httpapi): org_secrets CRUD endpoints (roleAdmin, write-only value)`

---

### Task B0.4: Wire `orgsecret.Store` into main + worker secretbox + secret resolver

**Files:**
- Modify: `cmd/studiod/main.go` (construct store ~line 218; add to `Deps` ~line 364; pass resolver into `worker.Config` ~line 292-314)
- Modify: `internal/worker/worker.go` (`Config` struct ~line 58-100: add `Secrets SecretResolver` field + interface)

- [ ] In `internal/worker/worker.go`, add a `SecretResolver` interface near the
  `Puller` interface (~line 1014) and a `Secrets` field to `Config` (after
  `Router` ~line 74). The interface is satisfied by `*orgsecret.Store` (it has
  `Resolve`) — do NOT import `orgsecret` into worker (keep the worker dependency
  on an interface, mirroring `Puller`):
  ```go
  // (in Config struct, after Router)
  	Secrets SecretResolver // org-scoped named-secret resolver for http custom nodes; nil → secret-bearing http nodes fail opaquely
  ```
  ```go
  // SecretResolver resolves an org's named secret plaintext (satisfied by
  // *orgsecret.Store). The ONLY path that exposes plaintext; worker injects it into
  // http request headers and never logs it. nil/ErrEncUnavailable → opaque failure.
  type SecretResolver interface {
  	Resolve(ctx context.Context, orgID, name string) (string, error)
  }
  ```
- [ ] In `cmd/studiod/main.go`, after `storageStore := storageconfig.New(...)`
  (~line 218), add (and import `"github.com/costa92/llm-agent-studio/internal/orgsecret"`):
  ```go
  	orgSecretStore := orgsecret.New(st.GORM(), encBox)
  ```
- [ ] In the `worker.New(worker.Config{...})` literal (~line 292-314), add to the
  field list (e.g. right after `Router: router,`):
  ```go
  		Secrets: orgSecretStore,
  ```
- [ ] In the `httpapi.Deps{...}` literal (~line 364), add after `CustomNodeType`:
  ```go
  		OrgSecret:      orgSecretStore,
  ```
- [ ] Run: `GOWORK=off go build ./...`
  - Expected: whole module builds clean.
- [ ] Commit: `feat(studiod): wire orgsecret store into httpapi deps + worker secret resolver`

---

## B1 — `internal/fetch` general request method + SSRF hardenings

### Task B1.1: SSRF hardenings — NAT64 + IPv4-mapped normalization (test first)

**Files:**
- Modify: `internal/fetch/fetch.go` (`isBlockedIP` ~line 182-192)
- Modify/Create: `internal/fetch/fetch_test.go`

- [ ] In `internal/fetch/fetch_test.go`, add a table-driven SSRF block test
  (no server needed — call `isBlockedIP` directly since the test is in package
  `fetch`):
  ```go
  func TestIsBlockedIP_Matrix(t *testing.T) {
  	cases := []struct {
  		ip      string
  		blocked bool
  	}{
  		{"127.0.0.1", true},          // loopback
  		{"10.0.0.5", true},           // RFC1918
  		{"192.168.1.1", true},        // RFC1918
  		{"169.254.169.254", true},    // link-local metadata
  		{"100.64.0.1", true},         // CGNAT
  		{"::1", true},                // IPv6 loopback
  		{"64:ff9b::a9fe:a9fe", true}, // NAT64 of 169.254.169.254 (a9fe:a9fe)
  		{"64:ff9b::0a00:0005", true}, // NAT64 of 10.0.0.5
  		{"::ffff:127.0.0.1", true},   // IPv4-mapped loopback
  		{"::ffff:10.0.0.5", true},    // IPv4-mapped RFC1918
  		{"8.8.8.8", false},           // public
  		{"2606:4700:4700::1111", false}, // public IPv6 (Cloudflare)
  	}
  	for _, c := range cases {
  		ip := net.ParseIP(c.ip)
  		if ip == nil {
  			t.Fatalf("bad test ip %q", c.ip)
  		}
  		if got := isBlockedIP(ip); got != c.blocked {
  			t.Errorf("isBlockedIP(%s) = %v, want %v", c.ip, got, c.blocked)
  		}
  	}
  }
  ```
  (Add `"net"` + `"testing"` imports if the test file is new.)
- [ ] Run (expect FAIL — NAT64 + IPv4-mapped cases): `GOWORK=off go test ./internal/fetch/... -run TestIsBlockedIP_Matrix -count=1`
  - Expected: failures on the `64:ff9b::*` and `::ffff:*` rows.
- [ ] In `internal/fetch/fetch.go`, add the NAT64 prefix var beside `cgnat`
  (~line 177):
  ```go
  // nat64 is the well-known NAT64 prefix (RFC 6052, 64:ff9b::/96). An attacker can
  // smuggle an IPv4 metadata target (e.g. 64:ff9b::169.254.169.254) past an
  // IPv4-only check, so embedded IPv4 must be extracted and re-checked.
  var nat64 = &net.IPNet{IP: net.ParseIP("64:ff9b::"), Mask: net.CIDRMask(96, 128)}
  ```
- [ ] Rewrite `isBlockedIP` (~line 182-192) to normalize IPv4-mapped/NAT64 to the
  embedded IPv4 and re-check, keeping the existing rules:
  ```go
  // isBlockedIP returns true for any IP that is NOT a routable public address:
  // loopback, private, link-local (incl. 169.254.169.254 metadata), multicast,
  // unspecified, interface-local, RFC 6598 CGNAT. IPv4-mapped IPv6 and NAT64
  // (64:ff9b::/96) embeddings are normalized to their embedded IPv4 and re-checked
  // (else 64:ff9b::169.254.169.254 / ::ffff:169.254.169.254 would slip past an
  // IPv4-only test).
  func isBlockedIP(ip net.IP) bool {
  	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
  		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
  		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
  		return true
  	}
  	// IPv4-mapped IPv6 (::ffff:a.b.c.d): To4() returns the embedded v4; check it
  	// against CGNAT (the std predicates above already see through ::ffff:).
  	if v4 := ip.To4(); v4 != nil {
  		if cgnat.Contains(v4) {
  			return true
  		}
  		return false
  	}
  	// NAT64 (64:ff9b::a.b.c.d): the last 4 bytes are an embedded IPv4 — extract and
  	// recurse so all v4 rules (loopback/private/link-local/CGNAT) apply.
  	if nat64.Contains(ip) {
  		embedded := net.IPv4(ip[12], ip[13], ip[14], ip[15])
  		return isBlockedIP(embedded)
  	}
  	return false
  }
  ```
  (Note: a full IPv6 `ip` is 16 bytes; `ip[12..15]` are the embedded IPv4 octets
  for a `64:ff9b::/96` address. `net.ParseIP` returns 16-byte form for IPv6.)
- [ ] Run (expect PASS): `GOWORK=off go test ./internal/fetch/... -run TestIsBlockedIP_Matrix -count=1`
  - Expected: all rows green.
- [ ] Commit: `feat(fetch): block NAT64 + IPv4-mapped SSRF bypass vectors`

---

### Task B1.2: General `Do(ctx, Request)` method + redirect Authorization strip

The existing `Get` is GET-only, hardcodes `http.MethodGet`, errors on non-2xx,
and formats the raw URL into errors. Add a general method reusing the SAME private
transport (`f.client`, `resolveAndValidate`). It returns `(status, body,
contentType, err)` where `err` is ONLY transport/SSRF/cap failure — non-2xx is
NOT an error (caller decides). `AllowedContentTypes` is left empty by the http
caller (allow any type).

**Files:**
- Modify: `internal/fetch/fetch.go` (add `Request`/`Response`/`Do`; harden `CheckRedirect`)
- Modify: `internal/fetch/fetch_test.go`

- [ ] In `internal/fetch/fetch_test.go`, add a `Do` happy-path + non-2xx +
  redirect-strip test using `httptest` + `NewLoopbackForTest`:
  ```go
  func TestDo_PostAndStatus(t *testing.T) {
  	var gotMethod, gotAuth, gotBody string
  	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		gotMethod = r.Method
  		gotAuth = r.Header.Get("Authorization")
  		b, _ := io.ReadAll(r.Body)
  		gotBody = string(b)
  		w.WriteHeader(503) // non-2xx must NOT be an error
  		_, _ = w.Write([]byte("backend down"))
  	}))
  	defer srv.Close()
  	f := NewLoopbackForTest(5*time.Second, 1<<20, nil)
  	resp, err := f.Do(context.Background(), Request{
  		Method:  "POST",
  		URL:     srv.URL,
  		Headers: map[string]string{"Authorization": "Bearer xyz", "X-Q": "hi"},
  		Body:    []byte(`{"q":"hi"}`),
  	})
  	if err != nil {
  		t.Fatalf("Do: %v", err)
  	}
  	if resp.Status != 503 {
  		t.Fatalf("status = %d want 503", resp.Status)
  	}
  	if gotMethod != "POST" || gotAuth != "Bearer xyz" || gotBody != `{"q":"hi"}` {
  		t.Fatalf("server saw method=%q auth=%q body=%q", gotMethod, gotAuth, gotBody)
  	}
  }

  func TestDo_RedirectToNewHostStripsAuthorization(t *testing.T) {
  	var secondAuth string
  	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		secondAuth = r.Header.Get("Authorization")
  		w.WriteHeader(200)
  	}))
  	defer second.Close()
  	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  		http.Redirect(w, r, second.URL, http.StatusFound) // 302 to a DIFFERENT host:port
  	}))
  	defer first.Close()
  	f := NewLoopbackForTest(5*time.Second, 1<<20, nil)
  	_, err := f.Do(context.Background(), Request{
  		Method:  "GET",
  		URL:     first.URL,
  		Headers: map[string]string{"Authorization": "Bearer leak"},
  	})
  	if err != nil {
  		t.Fatalf("Do: %v", err)
  	}
  	if secondAuth != "" {
  		t.Fatalf("Authorization leaked across host redirect: %q", secondAuth)
  	}
  }
  ```
  (Add `httptest`/`io`/`time`/`context` imports as needed.)
- [ ] Run (expect FAIL — `Do`/`Request`/`Response` undefined): `GOWORK=off go test ./internal/fetch/... -run TestDo -count=1`
- [ ] In `internal/fetch/fetch.go`, harden `CheckRedirect` (~line 72-77) to
  re-validate the host every hop AND strip Authorization (and any other request
  header) when the host changes. `net/http` already drops sensitive headers on
  cross-host redirects in recent Go, but make it explicit and unconditional for
  defense-in-depth:
  ```go
  		CheckRedirect: func(req *http.Request, via []*http.Request) error {
  			if len(via) >= 3 {
  				return fmt.Errorf("fetch: too many redirects")
  			}
  			if err := validateScheme(req.URL); err != nil {
  				return err
  			}
  			// Per-hop host re-validation: any host change drops Authorization (and
  			// every {{secret}}-bearing header) so a 3xx to an attacker host cannot
  			// exfiltrate the injected credential. IP re-validation still happens in
  			// DialContext on the new connection.
  			if len(via) > 0 && req.URL.Host != via[len(via)-1].URL.Host {
  				req.Header.Del("Authorization")
  				for k := range req.Header {
  					// Drop ALL caller-supplied headers on host change (we re-add none).
  					// Keep only Host/User-Agent which net/http manages.
  					if k == "User-Agent" {
  						continue
  					}
  					req.Header.Del(k)
  				}
  			}
  			return nil
  		},
  ```
  (Reduced max redirects from 5 to 3 per spec 限额. The `Get` path is unaffected
  in behavior beyond the lower hop cap, which is acceptable for asset pulls.)
- [ ] Add `Request`/`Response` types + `Do` near `Get` (~line 124). `Do` mirrors
  `Get`'s validate→build→client.Do→cap shape but: sends method/headers/body, does
  NOT treat non-2xx as error, and returns status. The opaque-error mapping for the
  http kind happens in the WORKER (B2), so `Do` may surface URL-bearing transport
  errors — the worker MUST NOT `%w` them onto the frontend:
  ```go
  // Request is a general SSRF-safe outbound request for the http custom-node kind.
  type Request struct {
  	Method  string
  	URL     string
  	Headers map[string]string
  	Body    []byte
  }

  // Response is the result of Do. Status is the raw HTTP status (non-2xx is NOT an
  // error — the caller decides). Body is the capped response body.
  type Response struct {
  	Status      int
  	Body        []byte
  	ContentType string
  }

  // Do performs a general SSRF-safe request reusing the same validated-IP transport
  // as Get. Unlike Get it: honors Method/Headers/Body, returns the status (non-2xx
  // is not an error), and applies the same body cap + per-hop redirect re-validation
  // + Authorization-strip-on-host-change. err is ONLY a transport / SSRF / cap
  // failure. Callers that inject secrets MUST map err to an opaque enum (NEVER %w
  // the raw error, which embeds the URL) before surfacing it.
  func (f *Fetcher) Do(ctx context.Context, in Request) (Response, error) {
  	u, err := url.Parse(in.URL)
  	if err != nil {
  		return Response{}, fmt.Errorf("fetch: parse url")
  	}
  	if err := validateScheme(u); err != nil {
  		return Response{}, err
  	}
  	method := in.Method
  	if method == "" {
  		method = http.MethodGet
  	}
  	var bodyReader io.Reader
  	if len(in.Body) > 0 {
  		bodyReader = bytes.NewReader(in.Body)
  	}
  	req, err := http.NewRequestWithContext(ctx, method, in.URL, bodyReader)
  	if err != nil {
  		return Response{}, fmt.Errorf("fetch: build request")
  	}
  	req.Header.Set("User-Agent", "llm-agent-studio/custom-http")
  	for k, v := range in.Headers {
  		req.Header.Set(k, v)
  	}
  	resp, err := f.client.Do(req)
  	if err != nil {
  		// NOTE: this error embeds the URL — callers injecting secrets must map it to
  		// an opaque enum before surfacing (the worker does, B2).
  		return Response{}, fmt.Errorf("fetch: do: %w", err)
  	}
  	defer resp.Body.Close()
  	ct := resp.Header.Get("Content-Type")
  	if !f.contentTypeAllowed(ct) {
  		return Response{}, fmt.Errorf("fetch: content-type %q not allowed", ct)
  	}
  	body, err := io.ReadAll(io.LimitReader(resp.Body, f.cfg.MaxBytes+1))
  	if err != nil {
  		return Response{}, fmt.Errorf("fetch: read body")
  	}
  	if int64(len(body)) > f.cfg.MaxBytes {
  		return Response{}, fmt.Errorf("fetch: body exceeds %d byte cap", f.cfg.MaxBytes)
  	}
  	return Response{Status: resp.StatusCode, Body: body, ContentType: ct}, nil
  }
  ```
  (Add `"bytes"` to the import block if not already present.)
- [ ] Run (expect PASS): `GOWORK=off go test ./internal/fetch/... -count=1`
  - Expected: all fetch tests pass (the existing `Get` tests + the new `Do`/SSRF
    tests). If the existing redirect test asserted a 5-hop cap, update it to 3.
- [ ] Commit: `feat(fetch): general Do(ctx, Request) reusing safe transport + redirect auth-strip`

---

## B2 — `runCustom` refactor + `runCustomHTTP` (variable/secret resolution, SSRF, body policy, opaque errors)

### Task B2.1: Refactor `runCustom` to per-kind decode (llm regression-guarded)

The current `runCustom` (worker.go:1500-1511) decodes the whole input into the
llm-shaped `customInput`. Refactor it to decode only `{kind, params:RawMessage}`,
then each `case` re-unmarshals its own typed struct. The llm path moves to an
`llmParams` struct so it is behaviorally identical — guarded by a regression test.

**Files:**
- Modify: `internal/worker/worker.go` (`customInput` ~1483-1496; `runCustom` ~1498-1511; `runCustomLLM` ~1513-1577)
- Modify: `internal/worker/worker_custom_test.go`

- [ ] FIRST add a regression test to `internal/worker/worker_custom_test.go`
  asserting the llm path still works after the refactor. If the 2A test file
  already has `TestRunCustomLLM_TextAndJSON`, ensure it still passes unchanged
  through this task; otherwise add a minimal DB-gated test that seeds a typed llm
  todo with `{kind:"llm", params:{userPrompt:"hi", outputFormat:"text", variables:[]}}`,
  runs with a mock chat model, and asserts a `node_outputs` row is written
  (`format="text"`). (Reuse the 2A worker test scaffolding for the mock
  `Router`/`ScriptedLLM` — `routedChatModel` returns `(nil,false)` if `cfg.Router==nil`,
  so the test MUST construct a `Router`, not just a model field.)
- [ ] Replace the `customInput` struct (worker.go:1483-1496) with a thin envelope
  + a dedicated llm params struct:
  ```go
  // customEnvelope is the kind-agnostic outer shape PlanCustom writes into a typed
  // custom todo's input_json: {kind, params}. runCustom reads kind, then each case
  // re-unmarshals params into its own typed struct. This is the B/C extension seam.
  type customEnvelope struct {
  	Kind   string          `json:"kind"`
  	Params json.RawMessage `json:"params"`
  }

  // customVariable is the post-rewrite variable binding (sourceNodeId already mapped
  // to sourceTodoId by PlanCustom's pass 2). Shared by every kind that reads upstream
  // text outputs ({{name}}).
  type customVariable struct {
  	Name         string `json:"name"`
  	SourceTodoId string `json:"sourceTodoId"`
  }

  // llmParams is the "llm" kind's params (unchanged from the 2A inline struct).
  type llmParams struct {
  	SystemPrompt string           `json:"systemPrompt"`
  	UserPrompt   string           `json:"userPrompt"`
  	Model        string           `json:"model"`
  	Temperature  float64          `json:"temperature"`
  	OutputFormat string           `json:"outputFormat"` // "text" | "json"
  	Variables    []customVariable `json:"variables"`
  }
  ```
- [ ] Rewrite `runCustom` (worker.go:1498-1511) to decode the envelope and switch:
  ```go
  // runCustom dispatches a typed custom todo by its input_json.kind. Each case
  // re-unmarshals params into its own typed struct. A shipped "llm"; B adds "http".
  func (w *Worker) runCustom(ctx context.Context, c claimed) (string, error) {
  	var env customEnvelope
  	if err := json.Unmarshal(c.input, &env); err != nil {
  		return "", fmt.Errorf("worker: custom input unmarshal: %w", err)
  	}
  	switch env.Kind {
  	case "llm":
  		var p llmParams
  		if err := json.Unmarshal(env.Params, &p); err != nil {
  			return "", fmt.Errorf("worker: custom llm params unmarshal: %w", err)
  		}
  		return w.runCustomLLM(ctx, c, p)
  	case "http":
  		var p httpParams
  		if err := json.Unmarshal(env.Params, &p); err != nil {
  			return "", fmt.Errorf("worker: custom http params unmarshal: %w", err)
  		}
  		return w.runCustomHTTP(ctx, c, p)
  	default:
  		return "", fmt.Errorf("worker: unsupported custom kind %q", env.Kind)
  	}
  }
  ```
- [ ] Change `runCustomLLM`'s signature from `(ctx, c, in customInput)` to
  `(ctx, c, in llmParams)` and update its body to read `in.SystemPrompt` /
  `in.UserPrompt` / `in.OutputFormat` / `in.Variables` (drop the `in.Params.`
  prefix). The variable-resolution loop reads `in.Variables` (each a
  `customVariable`). Everything else (substituteVars, routedChatModel, JSON probe,
  node_outputs INSERT) is unchanged.
- [ ] Run: `GOWORK=off go build ./internal/worker/...` (will FAIL until B2.2 adds
  `httpParams`/`runCustomHTTP`). To verify the refactor in isolation, temporarily
  stub: actually, simplest is to land B2.1 and B2.2 in one commit since
  `runCustom` references `httpParams`/`runCustomHTTP`. **Decision: keep B2.1 +
  B2.2 as one commit** (the switch references the http symbols). Build at the end
  of B2.2.
- [ ] (No separate commit — folded into B2.2's commit because the switch
  references `httpParams`/`runCustomHTTP`.)

---

### Task B2.2: `runCustomHTTP` — substitution, SSRF re-check, body policy, opaque errors

**Files:**
- Modify: `internal/worker/worker.go` (add `httpParams`, `httpError` enum, `runCustomHTTP`; imports: `internal/fetch`)
- Modify: `internal/worker/worker_custom_test.go`

- [ ] In `internal/worker/worker.go`, add the http param struct + opaque error
  enum + a `Fetcher` field requirement. Add a `HTTPFetcher` interface to `Config`
  (satisfied by `*fetch.Fetcher`) so tests can inject a loopback fetcher:
  ```go
  // (in Config struct, after Secrets)
  	HTTPFetcher HTTPDoer // SSRF-safe outbound for http custom nodes; nil → http nodes fail opaquely
  ```
  ```go
  // HTTPDoer performs an SSRF-safe outbound request (satisfied by *fetch.Fetcher).
  // The seam lets tests inject a loopback fetcher.
  type HTTPDoer interface {
  	Do(ctx context.Context, in fetch.Request) (fetch.Response, error)
  }

  // httpParams is the "http" kind's params (org-level type behavior). url is a static
  // literal (no {{...}}); header values may carry {{name}} + {{secret:NAME}}; body may
  // carry {{name}} only.
  type httpParams struct {
  	Method            string            `json:"method"`
  	URL               string            `json:"url"`
  	Headers           map[string]string `json:"headers"`
  	BodyTemplate      string            `json:"bodyTemplate"`
  	OutputFormat      string            `json:"outputFormat"`      // "text" | "json"
  	AllowResponseBody bool              `json:"allowResponseBody"` // admin attestation: this endpoint does not echo secrets
  	Variables         []customVariable  `json:"variables"`
  }

  // httpError is the opaque error enum surfaced to the frontend. NEVER wrap a secret,
  // url, header, or body into the error chain — fail() ships cause.Error() to the
  // browser via todo_failed SSE + ProblemError.Message.
  type httpError string

  func (e httpError) Error() string { return string(e) }

  const (
  	errRequestFailed     httpError = "request_failed"
  	errHostNotAllowed    httpError = "host_not_allowed"
  	errTimeout           httpError = "timeout"
  	errBodyTooLarge      httpError = "body_too_large"
  	errBlockedDest       httpError = "blocked_destination"
  )
  ```
  (Add `"github.com/costa92/llm-agent-studio/internal/fetch"` to the worker import
  block. Add a `secretRefRe` package-level regexp for `{{secret:NAME}}`:
  `var secretRefRe = regexp.MustCompile(`\{\{\s*secret:([A-Za-z0-9_\-]+)\s*\}\}`)`.)
- [ ] Add `runCustomHTTP`:
  ```go
  // runCustomHTTP executes the "http" kind: resolve {{name}} upstream variables and
  // {{secret:NAME}} org secrets, substitute into headers/body (url is a static
  // literal), re-validate post-substitution (no {{ residue; secret not in url/body),
  // make an SSRF-safe request via the fetch transport, and land node_outputs per the
  // body policy. ALL errors are opaque (httpError enum) — never embed secret/url/body.
  func (w *Worker) runCustomHTTP(ctx context.Context, c claimed, in httpParams) (string, error) {
  	if w.cfg.HTTPFetcher == nil {
  		return "", errRequestFailed
  	}
  	// 1. Resolve {{name}} upstream variables (same channel as llm).
  	nameVals := map[string]string{}
  	for _, v := range in.Variables {
  		if v.SourceTodoId == "" {
  			continue
  		}
  		var outputRef string
  		if err := w.cfg.DB.WithContext(ctx).Raw(
  			`SELECT COALESCE(output_ref,'') FROM todos WHERE id=$1`, v.SourceTodoId).Row().Scan(&outputRef); err != nil {
  			return "", errRequestFailed // opaque: never leak the variable/source
  		}
  		text, err := w.resolveOutputText(ctx, outputRef)
  		if err != nil {
  			return "", errRequestFailed
  		}
  		nameVals[v.Name] = text
  	}

  	// 2. Resolve org from the TRUSTED run context (never from input_json/node).
  	orgID, err := w.cfg.Projects.OrgIDForProject(ctx, c.projectID)
  	if err != nil {
  		return "", errRequestFailed
  	}

  	// 3. Substitute headers: {{name}} via substituteVars, then {{secret:NAME}} via
  	// the SEPARATE secret channel. secretBearing tracks whether ANY secret was
  	// injected (reliable; not a post-hoc header scan).
  	secretBearing := false
  	resolvedHeaders := make(map[string]string, len(in.Headers))
  	for k, val := range in.Headers {
  		// {{name}} first.
  		val = substituteVars(val, nameVals)
  		// {{secret:NAME}} next.
  		var secErr error
  		val = secretRefRe.ReplaceAllStringFunc(val, func(m string) string {
  			name := secretRefRe.FindStringSubmatch(m)[1]
  			if w.cfg.Secrets == nil {
  				secErr = errRequestFailed
  				return ""
  			}
  			plain, e := w.cfg.Secrets.Resolve(ctx, orgID, name)
  			if e != nil {
  				secErr = errRequestFailed // opaque: missing secret / box disabled → no name leaked
  				return ""
  			}
  			secretBearing = true
  			return plain
  		})
  		if secErr != nil {
  			return "", secErr
  		}
  		resolvedHeaders[k] = val
  	}

  	// 4. Substitute body ({{name}} only — {{secret}} forbidden in body, enforced at
  	// save-time validate(); re-check here post-substitution).
  	body := substituteVars(in.BodyTemplate, nameVals)
  	if secretRefRe.MatchString(body) || strings.Contains(body, "{{secret:") {
  		return "", errRequestFailed
  	}
  	// url is a static literal; re-confirm no template residue anywhere.
  	if strings.Contains(in.URL, "{{") {
  		return "", errRequestFailed
  	}

  	// 5. Make the request. Map every fetch error to an opaque enum (fetch errors
  	// embed the URL — must NOT reach the frontend).
  	resp, ferr := w.cfg.HTTPFetcher.Do(ctx, fetch.Request{
  		Method:  in.Method,
  		URL:     in.URL,
  		Headers: resolvedHeaders,
  		Body:    []byte(body),
  	})
  	if ferr != nil {
  		return "", classifyFetchError(ferr)
  	}
  	if resp.Status < 200 || resp.Status >= 300 {
  		// Non-2xx is an execution failure (worker retries); body NOT fed downstream.
  		return "", errRequestFailed
  	}

  	// 6. Body policy: secret-bearing && !allowResponseBody → store only {status}.
  	var content, format string
  	if secretBearing && !in.AllowResponseBody {
  		content = fmt.Sprintf(`{"status":%d}`, resp.Status)
  		format = "http-status"
  	} else {
  		content = string(resp.Body)
  		format = "text"
  		if in.OutputFormat == "json" {
  			var probe any
  			if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &probe); err != nil {
  				return "", errRequestFailed
  			}
  			content = strings.TrimSpace(content)
  			format = "json"
  		}
  	}

  	// 7. Land node_outputs (INSERT, pure $N).
  	outID := newID()
  	if err := w.cfg.DB.WithContext(ctx).Exec(
  		`INSERT INTO node_outputs (id, project_id, todo_id, type, content, format)
  		 VALUES ($1,$2,$3,$4,$5,$6)`,
  		outID, c.projectID, c.todoID, c.typ, content, format).Error; err != nil {
  		return "", errRequestFailed
  	}
  	return "custom:" + outID, nil
  }

  // classifyFetchError maps a fetch transport error to an opaque enum WITHOUT
  // inspecting its message for secrets/urls. Uses coarse signals (ctx deadline →
  // timeout; "blocked"/"all resolved IPs" → blocked_destination; "cap" → body too
  // large) and defaults to request_failed. NEVER returns the original error.
  func classifyFetchError(err error) error {
  	if errors.Is(err, context.DeadlineExceeded) {
  		return errTimeout
  	}
  	msg := err.Error()
  	switch {
  	case strings.Contains(msg, "blocked IP"), strings.Contains(msg, "are blocked"):
  		return errBlockedDest
  	case strings.Contains(msg, "byte cap"):
  		return errBodyTooLarge
  	case strings.Contains(msg, "not allowed"):
  		return errHostNotAllowed
  	default:
  		return errRequestFailed
  	}
  }
  ```
  (Note: `classifyFetchError` reads only fixed substrings the fetch package itself
  emits — none of which contain the secret/url/body of THIS request beyond the URL
  in `fetch: do:`, which falls through to the opaque `request_failed` default. The
  enum value returned is always one of the fixed strings.)
- [ ] Run: `GOWORK=off go build ./internal/worker/...`
  - Expected: builds clean (now that both B2.1 + B2.2 symbols exist).
- [ ] Wire the fetcher in `cmd/studiod/main.go`: in the `worker.Config{...}` literal
  add `HTTPFetcher: fetch.New(fetch.Config{Timeout: 10 * time.Second, MaxBytes: <cap>})`
  (reuse an existing fetch config constant if one exists for the video fetcher;
  import `internal/fetch` if not already). Spec 限额: timeout 10s, MaxBytes = the
  existing fetch cap. Run `GOWORK=off go build ./...`.
- [ ] Commit: `feat(worker): per-kind custom decode + runCustomHTTP (secret/var substitution, SSRF, body policy, opaque errors)`

---

### Task B2.3: Secret-never-leaks forced-failure matrix test

This is the keystone security test. Under every forced failure (non-2xx, dial
error, timeout, body cap, json parse failure, missing secret, box disabled), the
returned error, the `node_outputs` content, and assertable log output must NOT
contain the resolved secret value.

**Files:**
- Modify: `internal/worker/worker_custom_test.go`

- [ ] Add a fake `HTTPDoer` and fake `SecretResolver` to the worker test file:
  ```go
  type fakeDoer struct {
  	resp fetch.Response
  	err  error
  }
  func (f fakeDoer) Do(ctx context.Context, in fetch.Request) (fetch.Response, error) {
  	return f.resp, f.err
  }

  type fakeSecrets struct{ value string }
  func (f fakeSecrets) Resolve(ctx context.Context, orgID, name string) (string, error) {
  	return f.value, nil
  }
  ```
- [ ] Add `TestRunCustomHTTP_SecretNeverLeaks` (DB-gated — needs a project row for
  `OrgIDForProject` + a todo row + node_outputs). The secret sentinel is a
  high-entropy string asserted absent everywhere:
  ```go
  func TestRunCustomHTTP_SecretNeverLeaks(t *testing.T) {
  	const sentinel = "SUPER-SECRET-SENTINEL-9z8y7x"
  	// ... seed a project (org X) + a typed http todo; construct Worker with
  	//     Secrets: fakeSecrets{value: sentinel}, Projects: real store, DB: gorm ...
  	failures := []struct {
  		name  string
  		doer  HTTPDoer
  		params httpParams
  	}{
  		{"non-2xx", fakeDoer{resp: fetch.Response{Status: 500, Body: []byte("err")}}, httpParams{Method: "GET", URL: "https://api.example.com", Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"}}},
  		{"dial-error", fakeDoer{err: fmt.Errorf("fetch: do: dial https://api.example.com: connection refused")}, httpParams{Method: "GET", URL: "https://api.example.com", Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"}}},
  		{"timeout", fakeDoer{err: context.DeadlineExceeded}, httpParams{Method: "GET", URL: "https://api.example.com", Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"}}},
  		{"body-cap", fakeDoer{err: fmt.Errorf("fetch: body exceeds 1048576 byte cap")}, httpParams{Method: "GET", URL: "https://api.example.com", Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"}}},
  		{"json-parse", fakeDoer{resp: fetch.Response{Status: 200, Body: []byte("not json")}}, httpParams{Method: "GET", URL: "https://api.example.com", OutputFormat: "json", AllowResponseBody: true, Headers: map[string]string{"Authorization": "Bearer {{secret:K}}"}}},
  	}
  	for _, f := range failures {
  		t.Run(f.name, func(t *testing.T) {
  			w.cfg.HTTPFetcher = f.doer
  			ref, err := w.runCustomHTTP(ctx, claimedFor(httpTodo), f.params)
  			if err == nil {
  				t.Fatalf("expected failure for %s", f.name)
  			}
  			if strings.Contains(err.Error(), sentinel) {
  				t.Fatalf("secret leaked into error: %q", err.Error())
  			}
  			// Returned error must be one of the opaque enum values.
  			switch err.Error() {
  			case "request_failed", "host_not_allowed", "timeout", "body_too_large", "blocked_destination":
  			default:
  				t.Fatalf("non-opaque error: %q", err.Error())
  			}
  			_ = ref
  			// No node_outputs row should carry the sentinel.
  			var leaked int
  			w.cfg.DB.Raw(`SELECT count(*) FROM node_outputs WHERE content LIKE '%' || $1 || '%'`, sentinel).Row().Scan(&leaked)
  			if leaked != 0 {
  				t.Fatalf("secret leaked into node_outputs (%d rows)", leaked)
  			}
  		})
  	}
  }
  ```
- [ ] Add `TestRunCustomHTTP_BodyPolicy` (DB-gated): secret-bearing + `allowResponseBody:false`
  with a 200 + JSON body → `node_outputs` row has `format="http-status"` and
  content `{"status":200}` (NOT the body). Same params with `allowResponseBody:true`
  → content == body, `format="text"` (or `"json"` when `outputFormat:"json"`).
  Non-secret request → body always lands.
- [ ] Add `TestRunCustomHTTP_SecretForbiddenInBody` (pure-ish, DB-gated for org
  lookup): `BodyTemplate` containing `{{secret:K}}` → opaque error, no request made
  (assert fakeDoer not called via a call-count flag).
- [ ] Add `TestRunCustomHTTP_OrgScopedSecret` (DB-gated): a fake `SecretResolver`
  that records the `orgID` it was called with → assert it equals the PROJECT's org
  (from `OrgIDForProject`), proving resolution uses the trusted context, not input.
- [ ] Run (fresh DB): `LLM_AGENT_STUDIO_PG_URL=postgres://...studio_p2b_<rand>?sslmode=disable GOWORK=off go test ./internal/worker/... -run TestRunCustomHTTP -count=1 -p 1`
  - Expected: all green.
- [ ] Commit: `test(worker): http secret-never-leaks matrix + body-policy + org-scoped resolution`

---

## B3 — Registry `http` kind validation + secret-bearing admin-gate

### Task B3.1: `customnodetype` http param validation (save-time enforcement)

**Files:**
- Modify: `internal/customnodetype/store.go` (`validKinds` ~line 28; `validate()` ~line 76-87)
- Modify: `internal/customnodetype/store_test.go`

- [ ] In `internal/customnodetype/store_test.go`, add http validate cases (pure —
  `validate` is package-private but tests are in-package). If `validate` is only
  reachable via `Create`/`Update`, add a small in-package test calling it directly,
  or assert through `Create` with a fresh DB. Prefer a direct in-package call:
  ```go
  func TestValidate_HTTP(t *testing.T) {
  	mk := func(params string) UpsertInput {
  		return UpsertInput{Label: "调接口", Kind: "http", Params: json.RawMessage(params)}
  	}
  	ok := `{"method":"POST","url":"https://api.example.com","headers":{"Authorization":"Bearer {{secret:K}}","X-Q":"{{draft}}"},"bodyTemplate":"{\"q\":\"{{draft}}\"}","outputFormat":"json"}`
  	if err := validate(mk(ok)); err != nil {
  		t.Fatalf("valid http params rejected: %v", err)
  	}
  	bad := map[string]string{
  		"bad method":        `{"method":"TRACE","url":"https://x.com"}`,
  		"templated url":     `{"method":"GET","url":"https://{{host}}/x"}`,
  		"secret in url":     `{"method":"GET","url":"https://x.com/{{secret:K}}"}`,
  		"secret in body":    `{"method":"POST","url":"https://x.com","bodyTemplate":"{{secret:K}}"}`,
  		"bad outputFormat":  `{"method":"GET","url":"https://x.com","outputFormat":"xml"}`,
  		"missing url":       `{"method":"GET"}`,
  	}
  	for name, p := range bad {
  		if err := validate(mk(p)); err == nil {
  			t.Errorf("%s should be rejected", name)
  		}
  	}
  }
  ```
- [ ] Run (expect FAIL): `GOWORK=off go test ./internal/customnodetype/... -run TestValidate_HTTP -count=1`
- [ ] In `internal/customnodetype/store.go`, add `"http": true` to `validKinds`
  (line 28):
  ```go
  var validKinds = map[string]bool{"llm": true, "http": true}
  ```
- [ ] Add a package-level secret-ref regexp + http method set near the top of the
  file:
  ```go
  var secretRefRe = regexp.MustCompile(`\{\{\s*secret:`)
  var httpMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
  ```
- [ ] Extend `validate` (line 76-87) so that when `in.Kind == "http"` it parses the
  params and enforces the save-time rules (method enum; url required + no `{{`;
  `{{secret:}}` ONLY in header values, never url/body; outputFormat enum):
  ```go
  func validate(in UpsertInput) error {
  	if strings.TrimSpace(in.Label) == "" {
  		return fmt.Errorf("customnodetype: label required")
  	}
  	if !validKinds[in.Kind] {
  		return fmt.Errorf("customnodetype: invalid kind %q (want llm|http)", in.Kind)
  	}
  	if len(in.Params) == 0 || !json.Valid(in.Params) {
  		return fmt.Errorf("customnodetype: params must be valid JSON")
  	}
  	if in.Kind == "http" {
  		return validateHTTPParams(in.Params)
  	}
  	return nil
  }

  // validateHTTPParams enforces the http kind's save-time rules (spec 必做项 #5):
  // method enum; url required + static literal (no {{...}}); {{secret:}} only in
  // header values (never url/body); outputFormat ∈ text|json.
  func validateHTTPParams(raw json.RawMessage) error {
  	var p struct {
  		Method       string            `json:"method"`
  		URL          string            `json:"url"`
  		Headers      map[string]string `json:"headers"`
  		BodyTemplate string            `json:"bodyTemplate"`
  		OutputFormat string            `json:"outputFormat"`
  	}
  	if err := json.Unmarshal(raw, &p); err != nil {
  		return fmt.Errorf("customnodetype: http params: %w", err)
  	}
  	if !httpMethods[p.Method] {
  		return fmt.Errorf("customnodetype: http method %q invalid (GET|POST|PUT|PATCH|DELETE)", p.Method)
  	}
  	if strings.TrimSpace(p.URL) == "" {
  		return fmt.Errorf("customnodetype: http url required")
  	}
  	if strings.Contains(p.URL, "{{") {
  		return fmt.Errorf("customnodetype: http url must be a static literal (no {{...}} templates)")
  	}
  	if secretRefRe.MatchString(p.BodyTemplate) {
  		return fmt.Errorf("customnodetype: {{secret:...}} not allowed in bodyTemplate (headers only)")
  	}
  	for _, v := range p.Headers {
  		_ = v // {{secret:}} IS allowed in header values; no per-value rejection here.
  	}
  	if p.OutputFormat != "" && p.OutputFormat != "text" && p.OutputFormat != "json" {
  		return fmt.Errorf("customnodetype: http outputFormat %q invalid (text|json)", p.OutputFormat)
  	}
  	return nil
  }
  ```
  (`regexp` is already imported in store.go.)
- [ ] Run (expect PASS): `GOWORK=off go test ./internal/customnodetype/... -run TestValidate_HTTP -count=1`
  - Expected: green.
- [ ] Commit: `feat(customnodetype): http kind + save-time param validation (method/url/secret placement)`

---

### Task B3.2: Secret-bearing http TYPE create/update requires roleAdmin (in-handler ResolveRole)

The routes stay mounted at `roleEditor` (B3's non-secret http types are
editor-creatable). When the params are http AND any header value contains
`{{secret:...}}`, the handler must additionally require the caller be
`AtLeast(roleAdmin)` by calling `ResolveRole` itself (the middleware does not stash
the role).

**Files:**
- Modify: `internal/httpapi/customnodetypehandlers.go` (create/update handlers + a RoleResolver dependency)
- Modify: `internal/httpapi/httpapi.go` (pass `d.RoleResolver` into the two handlers)
- Modify: `internal/httpapi/customnodetypehandlers_test.go`

- [ ] In `internal/httpapi/customnodetypehandlers.go`, add a helper that detects a
  secret-bearing http body and a shared admin-gate using the authz `RoleResolver`.
  Import `authzhttp "github.com/costa92/llm-agent-authz/httpapi"`,
  `authzrole "github.com/costa92/llm-agent-authz/role"`, and `regexp`/`strings`:
  ```go
  var httpSecretRefRe = regexp.MustCompile(`\{\{\s*secret:`)

  // bodyBearsSecret reports whether a custom-node-type body is an http type whose
  // headers reference {{secret:...}} (so create/update needs roleAdmin, spec 裁决 #2).
  func bodyBearsSecret(b customNodeTypeBody) bool {
  	if b.Kind != "http" || len(b.Params) == 0 {
  		return false
  	}
  	var p struct {
  		Headers map[string]string `json:"headers"`
  	}
  	if err := json.Unmarshal(b.Params, &p); err != nil {
  		return false
  	}
  	for _, v := range p.Headers {
  		if httpSecretRefRe.MatchString(v) {
  			return true
  		}
  	}
  	return false
  }

  // requireAdminForSecret enforces caller AtLeast(roleAdmin) when the body bears a
  // secret. The middleware verifies editor; the role itself is NOT in ctx, so we
  // resolve it here (mirrors RequireScopeRole). Returns false + writes 403 on
  // insufficient role; true to proceed.
  func requireAdminForSecret(w http.ResponseWriter, r *http.Request, rr authzhttp.RoleResolver, b customNodeTypeBody) bool {
  	if !bodyBearsSecret(b) {
  		return true
  	}
  	if rr == nil {
  		http.Error(w, "secret-bearing types require admin (role resolver unavailable)", http.StatusForbidden)
  		return false
  	}
  	org := r.PathValue("org")
  	uid := authzhttp.UserID(r.Context())
  	eff, err := rr.ResolveRole(r.Context(), uid, org, "org", "")
  	if err != nil {
  		http.Error(w, "internal error", http.StatusInternalServerError)
  		return false
  	}
  	if !eff.AtLeast(authzrole.RoleAdmin) {
  		http.Error(w, "含密钥引用的 HTTP 类型需要管理员权限", http.StatusForbidden)
  		return false
  	}
  	return true
  }
  ```
- [ ] Change `createCustomNodeTypeHandler`/`updateCustomNodeTypeHandler` to accept
  the `RoleResolver` and call the gate after decoding the body, before the store
  call:
  ```go
  func createCustomNodeTypeHandler(s CustomNodeTypeStore, rr authzhttp.RoleResolver) http.HandlerFunc {
  	return func(w http.ResponseWriter, r *http.Request) {
  		var b customNodeTypeBody
  		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
  			http.Error(w, "bad request", http.StatusBadRequest)
  			return
  		}
  		if !requireAdminForSecret(w, r, rr, b) {
  			return
  		}
  		ct, err := s.Create(r.Context(), r.PathValue("org"), b.toInput())
  		if err != nil {
  			http.Error(w, err.Error(), http.StatusBadRequest)
  			return
  		}
  		writeJSON(w, http.StatusOK, ct)
  	}
  }
  ```
  (Same insertion in `updateCustomNodeTypeHandler` — add `rr` param + the gate
  call right after decode. `listCustomNodeTypesHandler`/`deleteCustomNodeTypeHandler`
  are unchanged.)
- [ ] In `internal/httpapi/httpapi.go` (~line 236-237), pass `d.RoleResolver`:
  ```go
  		mux.Handle("POST /api/orgs/{org}/custom-node-types", scoped(roleEditor, orgScope, createCustomNodeTypeHandler(d.CustomNodeType, d.RoleResolver)))
  		mux.Handle("PUT /api/orgs/{org}/custom-node-types/{id}", scoped(roleEditor, orgScope, updateCustomNodeTypeHandler(d.CustomNodeType, d.RoleResolver)))
  ```
- [ ] In `internal/httpapi/customnodetypehandlers_test.go`, add a fake
  `authzhttp.RoleResolver` returning a configurable role, and tests:
  - http body with `{{secret:K}}` header + resolver returns `RoleEditor` → 403.
  - same body + resolver returns `RoleAdmin` → ok (store create called).
  - http body with NO secret + resolver returns `RoleEditor` → ok.
  - llm body + resolver returns `RoleEditor` → ok (gate is http-only).
  Construct the request with the path value `org` set (httptest + `r.SetPathValue`
  or route through a tiny mux). `UserID(ctx)` can be empty in the focused test;
  the fake resolver ignores uid.
- [ ] Run: `GOWORK=off go test ./internal/httpapi/... -count=1`
  - Expected: `ok`.
- [ ] Commit: `feat(httpapi): secret-bearing http type create/update requires roleAdmin (in-handler ResolveRole)`

---

## B4 — Frontend: http param form + secret manager + canvas/run-view

> **T1 does NOT recur.** `allowResponseBody` is an org-level TYPE param (in
> `custom_node_types.params`), NOT a per-node field. There is NO new node-instance
> field, so `toStudioNodes` (canvasModel.ts) needs NO change in B4 — a reviewer
> should not expect one. http typed nodes ride the SAME `typeId` passthrough A
> already preserves.

### Task B4.1: Frontend types — `HttpParams`, `OrgSecret`, kind union

**Files:**
- Modify: `web/src/lib/types.ts`

- [ ] In `web/src/lib/types.ts`, widen the custom-node-type kind to a union and add
  `HttpParams` + `OrgSecret`:
  ```ts
  // org_secrets DTO（永不含 value）。
  export interface OrgSecret {
    id: string
    orgId: string
    name: string
    hasValue: boolean
  }

  // http kind 参数（组织级类型行为）。url 必须是静态字面量（禁 {{...}}）；
  // header 值可含 {{name}} 与 {{secret:NAME}}；bodyTemplate 仅 {{name}}。
  export interface HttpParams {
    method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE"
    url: string
    headers: Record<string, string>
    bodyTemplate?: string
    outputFormat?: "text" | "json"
    // 仅含密钥类型相关：admin 显式背书才放行响应体（默认抑制带密钥请求的响应体）。
    allowResponseBody?: boolean
  }
  ```
  Update the existing `CustomNodeType.kind` from `"llm"` to `"llm" | "http"` and
  `CustomNodeType.params` / `UpsertCustomNodeTypeInput.params` to `LlmParams |
  HttpParams` (discriminated by `kind`). Update `UpsertCustomNodeTypeInput.kind`
  similarly.
- [ ] Run: `cd web && npx tsc -b --noEmit`
  - Expected: type errors ONLY in call sites that destructure `params` without a
    kind narrow (fix those in B4.3 when the form is added; if the typecheck must be
    green now, add a `kind === "llm"` narrow at the existing llm usage).
- [ ] Commit: `feat(web): HttpParams + OrgSecret types + kind union`

---

### Task B4.2: Org secret manager (roleAdmin) — api + UI

**Files:**
- Create: `web/src/features/org-secrets/api.ts`
- Create: `web/src/features/org-secrets/OrgSecretManager.tsx`
- Create: `web/src/features/org-secrets/OrgSecretManager.test.tsx`

- [ ] Create `web/src/features/org-secrets/api.ts` mirroring an existing
  react-query feature api (e.g. `web/src/features/storage-config/api.ts`):
  `useOrgSecrets(org)`, `useCreateOrgSecret(org)`, `useUpdateOrgSecret(org)`,
  `useDeleteOrgSecret(org)` hitting `GET/POST /api/orgs/{org}/secrets` and
  `PUT/DELETE /api/orgs/{org}/secrets/{name}`; invalidate the list query on
  mutations. Use the repo's existing fetch wrapper.
- [ ] Create `OrgSecretManager.test.tsx` (mock `./api` via `vi.mock`): list renders
  `{name, hasValue}`; 新建 opens a dialog with name + value (password) inputs;
  submit calls create mutate with `{name, value}`; edit submits `{name, value}`
  (empty value keeps existing — note in helper text); delete calls delete mutate;
  the value is NEVER rendered back (no field shows the secret).
- [ ] Create `OrgSecretManager.tsx`: org-scoped page mirroring the storage-config
  manager — list (name chip + 「已设置」badge when hasValue), 新建/编辑 dialog (name
  + write-only value with "留空保留原值" helper on edit), delete with confirm. Surface
  it under org settings alongside storage-config / model-config (find the existing
  org-settings route registration and add a sibling tab/entry). Gate the page on
  admin role in the UI (backend is authoritative).
- [ ] Run: `cd web && npx vitest run src/features/org-secrets/`
  - Expected: all green.
- [ ] Commit: `feat(web): org secret manager (roleAdmin, write-only value)`

---

### Task B4.3: http param form + manager kind switch + non-admin guard

**Files:**
- Create: `web/src/features/custom-node-types/HttpParamForm.tsx`
- Create: `web/src/features/custom-node-types/HttpParamForm.test.tsx`
- Modify: `web/src/features/custom-node-types/CustomNodeTypeManager.tsx`
- Modify: `web/src/features/custom-node-types/CustomNodeTypeManager.test.tsx`

- [ ] Create `HttpParamForm.test.tsx`: render `<HttpParamForm value={...}
  onChange={spy} secretNames={["PARTNER_KEY"]} />`; assert: editing method/url calls
  onChange; entering `{{` in url surfaces a validation hint and the url field is
  flagged invalid; adding a header row with a value referencing `{{secret:PARTNER_KEY}}`
  (via the secret dropdown) marks the form as secret-bearing; `allowResponseBody`
  toggle is VISIBLE only when secret-bearing, with the "我确认此端点不回显密钥" label;
  outputFormat select updates the value.
- [ ] Run (expect FAIL): `cd web && npx vitest run src/features/custom-node-types/HttpParamForm.test.tsx`
- [ ] Create `HttpParamForm.tsx`: controlled form (`value: HttpParams`, `onChange`,
  `secretNames: string[]`). Fields: method (Select), url (Input with a no-`{{`
  validation message), headers (key/value rows; each value can insert `{{name}}`
  or pick a `{{secret:NAME}}` from a dropdown of `secretNames`), bodyTemplate
  (Textarea; hint: `{{secret}}` not allowed), outputFormat (Select text|json),
  allowResponseBody (Checkbox shown only when any header value matches
  `/\{\{\s*secret:/`). Reuse `@/components/ui/*` primitives as in the llm form.
- [ ] Run (expect PASS): same vitest command.
- [ ] In `CustomNodeTypeManager.tsx`: add a kind selector (llm | http) in the
  create dialog; render `LlmParamForm` or `HttpParamForm` by kind; pass
  `secretNames` from `useOrgSecrets(org)` into `HttpParamForm`. When the http type
  is secret-bearing AND the current user is not admin, disable the save button with
  a "需要管理员权限" hint (backend is authoritative — this is UX only; align with spec
  前端 #5). Update `CustomNodeTypeManager.test.tsx` to cover: switching kind to http
  renders the http form; submitting an http type calls create with
  `{label, color, kind:"http", params}`; a 403 from the secret-bearing create
  surfaces an admin-required message.
- [ ] Run: `cd web && npx vitest run src/features/custom-node-types/`
  - Expected: all green.
- [ ] Commit: `feat(web): http param form + manager kind switch + non-admin secret guard`

---

### Task B4.4: Canvas http typed nodes + run-view suppressed-body label

**Files:**
- Modify: `web/src/features/workflow-canvas/*` (palette/picker already list registry types from A; ensure http types render)
- Modify: run-view selected-node panel (`RunCanvas.tsx`/`WorkflowNode.tsx`)
- Test: extend the relevant `*.test.tsx`

- [ ] Confirm (no code change expected) that A's canvas dual-type entry lists ALL
  registry types regardless of kind — http typed types should already appear in the
  palette/picker because they are `custom_node_types` rows. If the palette filters
  by `kind === "llm"` anywhere, widen it to include `"http"`. Add a
  `canvasModel.test.ts` / `NodeTypePicker.test.tsx` case asserting an http registry
  type produces a typed node with its `typeId` set (reusing A's typed-node path).
- [ ] In the run-view selected-node panel (where A renders `run.output` /
  `run.outputFormat`), handle `outputFormat === "http-status"`: render
  「已完成（响应体已按安全策略隐藏）」plus the status code parsed from the
  `{"status":N}` content, instead of dumping the JSON. Add a test case for the
  suppressed-body rendering.
- [ ] Run: `cd web && npx vitest run src/features/workflow-canvas/`
  - Expected: all green.
- [ ] Commit: `feat(web): http typed nodes on canvas + run-view suppressed-body label`

---

### Task B4.5: Full build + test sweep + manual verification

**Files:** none (verification only).

- [ ] Backend build + non-DB unit tests:
  `GOWORK=off go build ./... && GOWORK=off go test ./internal/fetch/... ./internal/customnodetype/... -run 'TestIsBlockedIP_Matrix|TestDo|TestValidate_HTTP' -count=1`
  - Expected: build clean; listed tests pass.
- [ ] DB-gated suites (FRESH DB, single connection):
  `LLM_AGENT_STUDIO_PG_URL=postgres://postgres:pw@172.17.0.3:5432/studio_p2b_<rand>?sslmode=disable GOWORK=off go test ./internal/orgsecret/... ./internal/worker/... ./internal/httpapi/... -count=1 -p 1`
  - Expected: `ok` for all (create the DB first; stale data trips uniqueness
    indices).
- [ ] Web tests: `cd web && npx vitest run`
  - Expected: all suites pass.
- [ ] Manual (`:5173`, see memory `reference_studio-dev-runtime.md` to start
  studiod :8083 + Vite :5173; ensure `STUDIO_CONFIG_ENC_KEY` is set so the box is
  enabled — see `/tmp/studio-enc-key.txt`):
  1. As admin, create an org secret `PARTNER_KEY`.
  2. Create an http custom type with `Authorization: Bearer {{secret:PARTNER_KEY}}`
     → confirm a non-admin user CANNOT create it (403 / disabled save).
  3. Create a non-secret http type → confirm an editor CAN create it.
  4. On the canvas, drop a `script` node + the http typed node, connect
     script→http, bind a `{{draft}}` header/body var to the script → 保存 → 运行.
  5. With `allowResponseBody:false` (secret-bearing) → run-view shows
     「响应体已按安全策略隐藏」+ status; flip the TYPE to `allowResponseBody:true`
     → body lands.
  6. Point an http type at an internal URL (e.g. `http://169.254.169.254/...` or
     `http://127.0.0.1/...`) → confirm the run fails opaquely (blocked), with NO
     secret/url in the error shown.
  7. Pure built-in workflow + the A llm custom node still run (regression).
- [ ] Commit (if any verification-driven fixups): `chore: phase2b verification fixups`

---

## Notes for the executor

- `GOWORK=off` on EVERY go command.
- DB-gated Go tests need a FRESH DB and `-p 1`. Create the DB first (e.g.
  `createdb -h 172.17.0.3 -U postgres studio_p2b_<rand>`), then point
  `LLM_AGENT_STUDIO_PG_URL` at it.
- studio changes land via branch → push → PR → rebase merge (no direct push to
  main; memory `feedback_studio-changes-via-pr.md`). You are on
  `feat/custom-nodes-phase2b`.
- GORM house rules are non-negotiable: INSERT…RETURNING (never `gorm.Create`), no
  `AutoMigrate`, pure `$N` Raw, NULL/BYTEA columns via `[]byte`, multi-statement
  under `db.Transaction`.
- **B0 ships first and gets an independent security review** (same precedent as
  storageconfig/project/worker). Do not start B2's `runCustomHTTP` before B0's
  store + B1's `fetch.Do` are merged and reviewed.
- The authz module in this repo is pinned at `v0.4.1` in `go.mod` (the mod cache
  shows `v0.4.0`'s middleware — the API is identical for our use: `UserID(ctx)`,
  `RoleResolver.ResolveRole(ctx, uid, org, "org", "")`, `role.AtLeast`,
  `authzrole.RoleAdmin`). `RequireScopeRole` does NOT stash the role in ctx — the
  secret-gate handler MUST resolve it itself.

---

## Self-Review

### Spec-coverage checklist (every spec security decision → task)

- [ ] org-scoped secret resolution from TRUSTED context (`OrgIDForProject`, never
  input_json) — B2.2 step 2; B2.3 `TestRunCustomHTTP_OrgScopedSecret`.
- [ ] secret-bearing http TYPE create/update requires roleAdmin via in-handler
  `ResolveRole` — B3.2.
- [ ] default response-body suppression for secret-bearing requests;
  `allowResponseBody` admin attestation (org-level TYPE param, NO per-node field) —
  B2.2 step 6; B2.3 `TestRunCustomHTTP_BodyPolicy`; B4.1 `HttpParams.allowResponseBody`.
- [ ] `secretBearing` tracked in the resolution channel (not a post-hoc header
  scan) — B2.2 step 3.
- [ ] `{{secret}}` headers-only + `{{name}}` forbidden in URL — enforced at
  save-time `validate()` (B3.1) AND post-substitution (B2.2 steps 4-5).
- [ ] reuse `internal/fetch` transport (general `Do` method, no re-built client) —
  B1.2.
- [ ] NAT64 + IPv4-mapped SSRF block — B1.1.
- [ ] redirect per-hop host re-validation + Authorization/secret-header strip —
  B1.2.
- [ ] opaque error allowlist (NO `%w` of secret/url/body; `fail()` ships
  `cause.Error()` to frontend) — B2.2 `httpError` enum + `classifyFetchError`;
  B2.3 secret-never-leaks matrix asserts opaque enum values only.
- [ ] no logging/OTel of url/headers/body — B2.2 (no slog/OTel calls added in
  `runCustomHTTP`; verify none slipped in during review).
- [ ] `org_secrets` DTO never exposes `value_enc`/plaintext — B0.2 (`OrgSecret` has
  only `{id, orgId, name, hasValue}`); B0.3 handler test asserts value absent in
  response body.
- [ ] box-disabled → refuse, not silent — B0.2 `encryptValue`/`Resolve` return
  `ErrEncUnavailable`; B0.2 `TestBoxDisabled_Refuses`; B2.2 secret-needing request
  with disabled box → opaque failure.
- [ ] NO delete-in-use guard on `org_secrets` (spec non-goal) — B0.2 `Delete` is a
  plain org-scoped DELETE.
- [ ] NO reveal endpoint for `org_secrets` (spec non-goal) — `OrgSecretStore` iface
  has no reveal method; only the worker's `SecretResolver.Resolve` decrypts.
- [ ] customInput refactor to `{kind, params:RawMessage}` with llm regression —
  B2.1.
- [ ] limits: non-2xx → failure, no body downstream (B2.2 step 5); timeout 10s +
  MaxBytes cap + ≤3 redirects (B1.2 `CheckRedirect`; B2.2 main wiring).

### Placeholder scan

- No `TODO`/`FIXME`/`...`/`<placeholder>` left in code blocks; every code step
  shows complete Go/TS. The one intentional documentation line in B0.2's test
  (`items[0].Value`) is annotated "remove if it won't compile" — the real
  enforcement is the DTO struct having no value field.

### Type / name consistency (across tasks)

- Store: `orgsecret.Store`, `orgsecret.OrgSecret{ID,OrgID,Name,HasValue}`,
  `orgsecret.UpsertInput{Name,Value}`, methods `New(db,box)`, `List`, `Create`,
  `Update(orgID,name,in)`, `Delete(orgID,name)`, `Resolve(orgID,name)`, errors
  `ErrEncUnavailable`/`ErrNotFound` — used identically in B0.2/B0.3/B0.4.
- HTTP iface: `OrgSecretStore` (no `Resolve`); `customNodeTypeBody`; handler gate
  `requireAdminForSecret`/`bodyBearsSecret` — B0.3/B3.2.
- Worker: `customEnvelope{Kind,Params}`, `customVariable{Name,SourceTodoId}`,
  `llmParams`, `httpParams{Method,URL,Headers,BodyTemplate,OutputFormat,AllowResponseBody,Variables}`,
  `httpError` enum (`errRequestFailed`/`errHostNotAllowed`/`errTimeout`/
  `errBodyTooLarge`/`errBlockedDest`), `SecretResolver.Resolve`, `HTTPDoer.Do`,
  `Config.Secrets`/`Config.HTTPFetcher` — consistent across B2.1/B2.2/B2.3/B0.4.
- Fetch: `fetch.Request{Method,URL,Headers,Body}`, `fetch.Response{Status,Body,ContentType}`,
  `(*Fetcher).Do` — B1.2 matches the worker's `HTTPDoer` interface and B2.2 call
  site exactly.
- customnodetype: `validKinds["http"]`, `validateHTTPParams`, `httpMethods`,
  `secretRefRe` — B3.1.
- Frontend: `HttpParams`, `OrgSecret`, `CustomNodeType.kind: "llm" | "http"` —
  B4.1 consumed by B4.2/B4.3/B4.4.
