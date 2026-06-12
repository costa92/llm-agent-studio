# AI Studio — M8 实现深潜（BYOK + 存储配置 + 平台超级管理员 + secretbox 加密）

- 日期：2026-06-12
- 类型：只读实现分析（READ-ONLY）— 不改任何源码
- 来源：本 session 内 Explore agent 跨文件扫描的深扫报告（chat 沉淀落档）
- 范围：`internal/{secretbox,models,modelrouter,storageconfig,storagerouter,blob,studiosvc,httpapi}` + `cmd/studiod/main.go`
- 前置阅读：[m1-implementation-deep-dive.md](./m1-implementation-deep-dive.md)（worker / SSE 已覆盖）+ [m4-implementation-deep-dive.md](./m4-implementation-deep-dive.md)（异步引擎已覆盖）

> 所有引用为 `path:line`，路径相对仓库根 `llm-agent-studio/`。本仓 Go 命令需 `GOWORK=off`。
> M8 范围含 post-v0.8.0（main, 未打新 tag）：GitHub blob 后端 + 平台超级管理员 + 用户管理。
> 横向参考：[architecture/run-flow.md](./architecture/run-flow.md) 单 run 路径、[architecture/subsystem-map.md](./architecture/subsystem-map.md) 全景图。

---

## 1. secretbox AES-256-GCM 实现

`internal/secretbox/secretbox.go`

- **加密原语**：纯 stdlib —— `crypto/aes.NewCipher` + `cipher.NewGCM(block)`（`secretbox.go:41-48`），非任何第三方。
- **密钥格式**：`STUDIO_CONFIG_ENC_KEY` 期望 **base64 编码的 32 字节**：`New` 先 `base64.StdEncoding.DecodeString`，再校验 `len(key) != 32` 拒绝（`secretbox.go:34-40`）。空字符串合法，返回 *disabled* box。
- **Nonce**：每次 `io.ReadFull(rand.Reader, nonce)` 全随机 12 字节（`secretbox.go:66-69`），无固定 IV。
- **序列化格式**：`aead.Seal(nonce, nonce, plaintext, nil)` 把密文 append 到 nonce 之后 → `nonce||ciphertext`（GCM tag 在密文末尾自动追加）（`secretbox.go:71`）。**无显式 version 字段**——`Decrypt` 也不解析任何 version（`secretbox.go:75-89`）。
- **Disabled box 强制点**：`Box.Encrypt/Decrypt` 首行 `if !b.Enabled() { return nil, ErrNoKey }`（`secretbox.go:63-64, 76-77`），且 `models.Create/Update` 与 `storageconfig.Upsert*` 在调用 `Encrypt` 前再用 `s.box.Enabled()` 显式 guard，返 `ErrEncUnavailable`（`models/store.go:193-201, 255-263`；`storageconfig/store.go:117-123`）→ handler 映射 400（`httpapi/m2handlers.go:325, 365`；`httpapi/storagehandlers.go:62-65`）。
- **轮换接缝**：**未留**。序列化是裸 `nonce||ct`，不含 key id / version。要做轮换必须先升级格式（`secretbox.go:60-71`）。
  > **Followup**：[Issue #22](https://github.com/costa92/llm-agent-studio/issues/22) 已记录 RFC，三方案待 owner 决策。

## 2. model_configs CRUD 深挖

`internal/models/store.go`

- **Schema**（`internal/storage/storage.go:164-176` + M5 加列 `238-240`）：`id, org_id, kind, provider, model, enabled, is_default, params_json, created_at` + M5 `base_url TEXT`, `api_key_enc BYTEA`（**M5 是 additive ALTER**，无 unique 索引约束单 default）。
- **hasApiKey 投影**：通过 SQL 计算列 `(api_key_enc IS NOT NULL) AS has_api_key`（`store.go:293, 324, 349`）— 非持久化列，store 永不解密出明文回客户端；DTO 字段也只暴露 `HasAPIKey bool`（`store.go:114, 216, 296`）。
- **加密路径**：`Create` 中 `in.APIKey != ""` → `box.Enabled()` 守卫 → `box.Encrypt` → 写 `api_key_enc`（`store.go:191-201`）。
- **"key 留空保留原密钥"**：用 SQL `CASE WHEN $9 THEN $10 ELSE api_key_enc END`（`store.go:280`），$9=`replaceKey`（`APIKey != ""`）—— **DB 原子分支**，免去先 SELECT 后 UPDATE 的 race。
- **Edit/Delete 跨 org 防护**：`WHERE id=$1 AND org_id=$2`（`store.go:282, 310`）；`RowsAffected()==0` → `ErrNotFound` → handler 404（`m2handlers.go:360-362, 381-384`）。
- **级联清理**：`Delete` 仅删 model_configs 行；**未发现**对 `generations`/`assets` 的 model/provider 列做 `SET NULL` 或重写——查询发现无 FK、无触发器（`storage.go:147-162` 中 `generations` 的 provider/model 只是 `TEXT NOT NULL DEFAULT ''`）。M8 范围内不要求。
- **`params_json` 拒收凭据**：`forbiddenParamKeys` 黑名单 `apikey/secret/password/passwd/credential`（`store.go:38`），`isCredentialKey` 在 `secretKeyIn` 递归扫描中触发（`store.go:43-67`）→ 返 `ErrSecretParam (field %q)`。`token` 走词尾匹配避免 `max_tokens` 误伤（`store.go:50`）；`_key` 词尾匹配同样避免普通 count 字段误判。
- **default 原子性**：`Create`/`Update` 在事务里先 `UPDATE model_configs SET is_default=false WHERE org_id=$1 AND kind=$2 [AND id<>$3]`（`store.go:207-212, 269-275`），再 INSERT/UPDATE——**应用层事务**，无 DB unique 约束保证单 default。

## 3. ModelRouter 路由解析

`internal/modelrouter/router.go`

- **依赖注入**：包本身不引具体 provider；`Config.BuildChat/BuildMedia` 是 `func` 由 `cmd/studiod/main.go:601, 649` 注入（`router.go:38-39, 6-7` 注释）。
- **`ChatModelFor(ctx, orgID)`**（`router.go:74-93`）：`ResolveForOrg(ctx, orgID, "text")` 拉默认配置；`rm.APIKey == ""` 或 `buildChat==nil` → 回 `defaultChat`；build 失败记 warn + 回 default。**永不返回 nil**（依赖 defaultChat）。
- **`MediaGeneratorFor(ctx, orgID, kind)`**（`router.go:99-134`）：三层回退：有 per-config key → `buildMedia`；无 key → `registry.Resolve(rm.Provider, rm.Model)`（环境默认 key 走的 adapter）；再失败 → `registry.Default()`。
- **OpenAI 兼容 + Ollama 在 factory 哪里分支**：`buildChatFactory`（`main.go:601-641`）：`case "openai", "openai-compatible"` 共用 `openaiprovider.New`，`baseURL` 经 `openaiprovider.WithBaseURL` 注入（`main.go:608-613`）；`case "ollama"` 走 `ollamaprovider.New`，**无 `WithAPIKey`**——baseURL 缺省由 provider 内部 fallback（`main.go:626-632`；`models/store.go:97-99` 注释 "Ollama 无需 key，base_url 缺省 http://localhost:11434"）。
- **缺省路径**：org 无 default → `ResolveForOrg` 返 `(_, false, nil)` → `ChatModelFor` 落 `defaultChat`（环境 chat model）；`MediaGeneratorFor` 落 `registry.Default()`（环境 image generator）。
- **缓存策略**：**无缓存**。`router.go:53-55` 注释："Building a provider client per call is acceptable for now (low volume) — no cache." `ResolveForOrg` 每次都查 DB + 解密（`models/store.go:345-370`）。**性能 vs 内存驻留**：选明文短驻 + 每次解密（解密后仅在 `ResolvedModel` 上存活，函数返回即 GC）。

## 4. 存储配置两层解析（global + per-org）

`internal/storageconfig/store.go`

- **Schema**（`internal/storage/storage.go:246-264`）：`id, scope ('global'|'org'), org_id, mode, endpoint, region, bucket, access_key_id, secret_enc BYTEA, use_ssl, public_prefix, enabled, created_at, updated_at`。**两条 partial unique index**：
  - `storage_configs_global_uniq ON (scope) WHERE scope='global'` → 强保 global 唯一
  - `storage_configs_org_uniq ON (org_id) WHERE scope='org'` → 强保 per-org 唯一
- **UPSERT**（`store.go:141-156, 171-185`）：`ON CONFLICT` 命中相应 partial index 时 `DO UPDATE` + `secret_enc=CASE WHEN $11 THEN EXCLUDED.secret_enc ELSE storage_configs.secret_enc END`（`store.go:148-149, 178-179`）—— **同 models 的 keep-or-replace**。
- **解析 SQL**（`store.go:248-261`）：先 `WHERE scope='org' AND org_id=$1 AND enabled=true`（`store.go:251`），无则 `WHERE scope='global' AND enabled=true`（`store.go:260`）。
- **`enabled` 语义**：store 层仅取 `enabled=true` 的行；disabled 行存 DB 但**不参与解析**（`store.go:251, 260`）。
- **Secret 加密**：`encryptSecret`（`store.go:117-129`）用**同一 `secretbox.Box`**（`storage.go:199`）。DTO 仅 `(secret_enc IS NOT NULL) AS has_secret` 计算列（`store.go:152, 182, 202, 218`）— 与 models 同模式。
- **API 响应 strip 位置**：`scanConfig` 只把 `has_secret` 进 DTO（`store.go:189-196`）；handler `storageConfigWriteBody.Secret` 是 `write-only`，`writeJSON` 返的是 `StorageConfig`（含 `hasSecret` 不含 `Secret`）（`storagehandlers.go:30, 99, 145`）。
- **GitHub 字段复用**：`validate`（`store.go:103-108`）只校 `Bucket`（repo）和 `AccessKeyID`（owner）；**Token 必填性下放到 `blob/github.New`**（`store.go:105-107` 注释）。其它列复用映射在 `cmd/studiod/main.go:226-229` 完成（`Region`→branch、`PublicPrefix`→PathPrefix、`Endpoint`→APIBase、`SecretKey`→Token、`AccessKeyID`→Owner、`Bucket`→Repo）—— **仅 factory 层映射，schema 是通用的对象存储字段**。

> **Followup**：[PR #24](https://github.com/costa92/llm-agent-studio/pull/24) 在 `putOrgStorageConfigHandler` 加 precondition 拒收 `mode='localfs'`（global 仍允许）—— 因 `buildStorageStore` 对 localfs 强制复用 `localfsDefault`（保持单一回源 server），per-org localfs 写入静默无效，是 UX 陷阱。

## 5. blob 后端 4 实现对照

`internal/blob/*`

| 后端 | 文件 | 客户端 | 签名 | 100MB | URL 形态 | Key 校验/防 traversal |
|---|---|---|---|---|---|---|
| **localfs** | `localfs/localfs.go` | stdlib os | HMAC-SHA256(`key\n+exp`) + `subtle.ConstantTimeCompare`（`localfs.go:81-85, 109`） | N/A（直接落盘） | `/api/blob/{escaped-key}?exp=&sig=` (TTL 10min in handler `m2handlers.go:69`)，回源走同一 studiod（`localfs.go:88-92, 274-298`） | `filepath.Clean("/"+key)` 阻断 `..`（`localfs.go:36-39`）；URL 端 `escapeKeyPath` percent-escape 各段保留 `/`（`localfs.go:98-104`） |
| **s3 / minio** | `s3/s3.go` | `github.com/minio/minio-go/v7` + `credentials.NewStaticV4`（`s3.go:14-15, 40-44`） | AWS SigV4（minio-go 内置） | 5GB 单 PUT | 预签 GET URL 直链 | minio-go 自身处理 |
| **oss** | `oss/oss.go` | `github.com/aliyun/aliyun-oss-go-sdk/oss`（`oss.go:16`） | **OSS 自家签名 ≠ SigV4**（`oss.go:5-7` 注释） | 5GB | `bucket.SignURL` 纯本地签（无 I/O）（`oss.go:61-66`） | alioss 处理 |
| **cos** | 共用 `s3/s3.go` | minio-go | AWS SigV4 | 5GB | 同 s3 | 派生 endpoint 在 **外层 factory**（`cmd/studiod/main.go:216-221`）调 `cosEndpointHost(region, explicit)`：优先显式 `rs.Endpoint`，否则 `cos.<region>.myqcloud.com`（`main.go:703-714`）—— **schema 不存派生公式**，仅在 buildStorageStore 拼。**重要**：`validModes` 包含 `cos`（`storageconfig/store.go:29`），但 `validate` 没把 `cos` 单列分支，落到 `s3/oss/cos` 通用 `bucket+endpoint required` 兜底（`store.go:97-102`）。 |
| **github** | `github/github.go` | **纯 stdlib `net/http` + `encoding/json/base64`**（`github.go:6-7, 14-18`） | GitHub PAT `Authorization: Bearer`（`github.go:101-105`） | **~100MB / file**（`github.go:137` 注释） | `https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{path}`（`github.go:183-185`） | path 用 `strings.Trim("/")` + PathPrefix 拼接（`github.go:87-93`）—— **未做额外 escape**，依赖 Contents API 接受路径 |

## 6. GitHub 后端实现细节

`internal/blob/github/github.go`

- **Put 流程**（`github.go:138-176`）：① `io.ReadAll(r)` 读全部；② `getSHA(path)` 命中既有 → 200 拿 sha；命中 404 → 标记新文件；③ 拼 payload `{message, content(base64), branch, [sha]}`；④ PUT `/repos/{owner}/{repo}/contents/{path}`。**sha-then-PUT 模式**：`existing ? payload["sha"]=sha : 省略`，避免 422。
- **Delete 流程**（`github.go:189-222`）：先 `getSHA` 拿 sha（404 → 返 nil 幂等），再 DELETE 带 `{message, sha, branch}`。
- **公开仓库 raw 直链**（`github.go:183-185`）：`https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{objectPath}`；**ttl 被忽略**（GitHub 无 presigned URL），公开仓库直链即永久。
- **GHE 支持**：`Config.APIBase` 覆盖 API 根（`github.go:36-37, 69-72`），但 `SignedURL` 仍硬编码 `raw.githubusercontent.com`（`github.go:25-27, 184` 注释）—— **GHE raw 直链未实现**（注释明确说"留待后续，不在此猜测"）。
- **100MB 限制**：`Put` 注释明示"GitHub 单文件 ~100MB 上限；资产有界，故整体读入内存"（`github.go:137`），**未硬编码拒绝**——超额由 GitHub API 4xx 返回。
- **Token 隔离**：`apiError` 只读响应体片段（256 字节）拼错误（`github.go:226-229`），token 只在请求头不进响应体；`apiError` 注释明确"绝不含 token"。

## 7. 平台超级管理员（post-v0.8.0）

`internal/studiosvc/platform.go`

- **哨兵 org hack**（`platform.go:15-20, 26-28, 79-88`）：`platformOrgID = ""` 作占位 org_id，因为 `auth_membership.org_id` 带 FK → `auth_org(id)`（authz schema 强制）。`EnsureSentinelOrg` 启动时 `INSERT INTO auth_org (id, name) VALUES ('', '__platform__') ON CONFLICT DO NOTHING`（`platform.go:82-87`）—— **先用空 id 行满足 FK**。`ListAllOrgs` 始终 `WHERE o.id <> ''` 过滤（`platform.go:187, 262`）防泄露到 UI。
- **`PLATFORM_ADMIN_EMAILS` 解析**（`config/config.go:85-88, 151, 177-189`）：`get("PLATFORM_ADMIN_EMAILS", "")` 逗号分隔 → `splitEmails`（trim+小写，丢空项）→ `cfg.PlatformAdminEmails`。
- **启动种子化**（`main.go:120-128`）：`studiosvc.NewPlatform(az, st.Pool())` → `EnsureSentinelOrg` → `SeedFromEmails(ctx, cfg.PlatformAdminEmails)`。`SeedFromEmails`（`platform.go:160-177`）：对每封 email 调 `userIDByEmail`；**已注册 → Grant；未注册（`ErrUserNotFound`）→ continue 非错误**。
- **注册时补授 hook**（`register.go:40-49, 66-72`）：`WithPlatformTopUp(p, emails)` 把 `*Platform` 注入 `Register`；`Create` 建账成功后若 `adminEmails[email] == ok` 调 `platform.Grant(uid)`。**main.go:319 接线**。
- **最后一名守护**（`httpapi/platformhandlers.go:102-129, 169-206`）：`platformRevokeAdminHandler` 与 `platformDeleteUserHandler` 都在写操作前 `ListAdmins` + `if len(admins)<=1 && contains target → 409 "cannot remove the last platform admin"`—— **handler 层守护**，非 store 层。
- **`scope_kind="platform"`**：`Grant` 用 `authz.UpsertMembership(ctx, "", uid, "platform", nil, RoleAdmin)`（`platform.go:95`）。`RequireScopeRole`（`authz/middleware.go:54-75`）查 `auth_membership WHERE user_id=$1 AND org_id=$2 AND scope_kind=$3 AND (scope_id IS NULL OR scope_id=$4)`（`authz/store/memberships.go:21-24`）→ `role.Merge` 求最高。**scope_kind 是 `platform`，scope_id 是 nil**——`platformScope` 在 `platformhandlers.go:29` 返 `("", "")`。
- **`whoami` 端点**（`httpapi.go:182` + `platformhandlers.go:33-43`）：`/api/platform/whoami` 走 `authOnly`（**不**经 `platformAdmin` 门禁），`IsPlatformAdmin(ctx, uid)` → `{isPlatformAdmin: bool}`，供前端判定是否展示平台导航。

## 8. 平台用户管理（post-v0.8.0）

- **列出 + 详情**（`platform.go:213-279`）：`ListUsers` SQL 用 `EXISTS(...)` 判断 `is_admin`（`platform.go:215-220`），`UserDetail` 含 `Orgs` 子列表与每条 `SoleOrgAdmin` 布尔（`platform.go:256-263` —— `(SELECT count(*) WHERE org_admin)=1 AND m.role='org_admin'` 标记唯一 org_admin），供前端在删除/降级前提示。
- **删除**（`platform.go:283-292`）：`DELETE FROM auth_user WHERE id=$1` —— **硬删**。`auth_membership` 经 FK `ON DELETE CASCADE` 一并清除（`platform.go:281` 注释）—— 平台 + org 角色都没了。`generations` / `assets` / `projects` 无 FK → auth_user，故**保留**为匿名（owner/created_by 指向已删 id，UI 视情况显示）。
  > **Followup**：[Issue #23](https://github.com/costa92/llm-agent-studio/issues/23) 已记录三方案对比（FK SET NULL / 软删 / UI 容错）。
- **守护**（`platformhandlers.go:172-206`）：handler 拦三种情形：① `target == caller`（自删）→ 409；② `target ∈ admins && len(admins)<=1` → 409；③ `ErrUserNotFound` → 404。

## 9. 安全姿态总结

- **明文 key 唯一暴露面**：`models.ResolvedModel.APIKey` / `storageconfig.ResolvedStorage.SecretKey`（`models/store.go:120-126, 345-370`；`storageconfig/store.go:50-59, 248-287`）—— 仅 `ResolveForOrg` 内部可见，HTTP 路径无 DTO 含此字段。
- **DTO 投影只回布尔**：`(api_key_enc IS NOT NULL) AS has_api_key` / `(secret_enc IS NOT NULL) AS has_secret`—— SQL 计算列，不经 Go 端任何解密。
- **`write-only` 入参**：`apiKey` / `secret` 在 request body 解码、传进 `CreateInput`/`UpsertInput`，从不进 response DTO（`m2handlers.go:302-312, 341-349`；`storagehandlers.go:24-34`）。
- **`params_json` 拒收凭据**：`secretKeyIn` 递归扫描 + 黑名单 → `ErrSecretParam` → 400（`models/store.go:43-67, 182-189, 243-249`）。
- **日志不打印 key**：`github.go:226-229` `apiError` 仅截响应体 256 字节；`platform.go` / `modelrouter.go` 错误日志只打 `provider/model/org`（`router.go:80, 88, 109, 122, 131`）。
- **HMAC sig 防越权 + nosniff/sandbox**：`localfs.Verify` 用 `subtle.ConstantTimeCompare`（`localfs.go:109`）；blobHandler 设 `X-Content-Type-Options: nosniff` + `Content-Security-Policy: sandbox`（`m2handlers.go:294-295`）。
- **最后一名平台管理员守护**：handler 层 `len(admins)<=1` 拦截（`platformhandlers.go:115-122, 189-196`）。

## 10. M8 留下的接缝 / 已知限制

- **key rotation 不支持**：secretbox 序列化无 version/key-id（`secretbox.go:60-71`）。README 说"无 re-encrypt"是事实——`Decrypt` 没有 key-id 解析路径，要轮换得**先升级密文格式**（加 `version || key-id || nonce || ct`）再批量重加密。
- **CORS / 公开 raw 直链隔离**：`github.go:183-185` 公开仓库的 `raw.githubusercontent.com` 是公开 URL，签发后**任何拿到 URL 的人可读**——TTL 无意义，公开仓库属性如此。私有仓库需 GitHub 走 API 反代，**未实现**。
- **localfs 共享单根**：`buildStorageStore` 对 `mode=localfs` **忽略 per-row root/secret**，统一复用 `localfsDefault`（`main.go:202-205` 注释"保持单一回源 server"）—— 任何 per-org localfs 配置**实际无效**。
  > **Followup**：[PR #24](https://github.com/costa92/llm-agent-studio/pull/24) 已修—— `putOrgStorageConfigHandler` 拒收 `mode=localfs`，避免 UX 陷阱（写入成功但不生效）。
- **CORS/SSRF on GitHub PAT**：PAT 经 authz 后进 outgoing request，若 PAT scope 过大（repo + admin:org + ...）有越权风险，**无最小化校验**。
- **schema 上无 model_configs 单 default 约束**：M5/M6 都未加 unique index（`storage.go:164-176, 246-264`），单 default 仅靠应用层事务维持——并发创建可能两个 default 短暂共存。
- **platform membership 唯一性无 DB 约束**：靠 sentinel org + unique `(org_id, user_id, scope_kind, COALESCE(scope_id,''))`（`authz/store/memberships.go:13`）天然一人一行，无 last-admin 的 DB 约束——handler 守护是唯一防退化线。
- **generations/assets 不级联删 user**：`DELETE FROM auth_user` 只 cascade 掉 `auth_membership`；`assets.created_by`/`generations` 无 FK 指向 `auth_user`—— 历史成本/资产留存为悬空指针。
  > **Followup**：[Issue #23](https://github.com/costa92/llm-agent-studio/issues/23) 已记录三方案待 owner 决策。
- **openai-compatible 误触发**：`Catalog` 注释（`models/store.go:94`）和 `buildChatFactory`（`main.go:608`）把 `"openai-compatible"` 与 `"openai"` 合并到 `case`，**但 Catalog 没列该项**——用户得自己填 provider 字段，UI 层如何引导未在 M8 范围内确认。
- **GHE raw 主机未实现**：`github.go:25-27, 184` 注释明说"留待后续"。

## 11. 独立观察 / 疑问

1. **`mode=cos` 字段复用不彻底**：`validModes` 收 `cos`（`storageconfig/store.go:29`），但 `validate` 走通用 `s3/cos/oss` 分支（`store.go:97-102`）要求 `Bucket + Endpoint`，而 `buildStorageStore` 派生 endpoint（`main.go:706-714`）—— **前端若只填 `region` 不填 `endpoint`，validate 拒收**；前端只填 `endpoint` 显式覆盖则绕过派生。文档与 UI 必对此有说明。

2. **secretbox 无 base64 输入回显**：`New` 错误 `secretbox: master key must be 32 bytes (AES-256), got %d` 含解码后长度，但不含主密钥字节——OK。

3. **default 切换的 race window**：`Create`/`Update` 在事务内 `clear default → insert/update`，无锁——若两个并发请求同时 Create 同 (org, kind) 的 IsDefault=true，理论都可能过 clear 后再 insert，**出现两个 default**（`models/store.go:207-212`）。M8 范围内可接受（同 kind 内 Create 不频繁）。

4. **明文 key 在 `ResolvedModel` 上驻留时长**：`ResolveForOrg` 出栈即丢，但 worker 调用链 `router.ChatModelFor → factory.New(opts...)` 在 `buildChat` 闭包闭变量（`main.go:601-641`）中可能**短期持有**——`New` 完即丢，无长期驻留。

5. **`m2handlers.go:296` `Content-Security-Policy: sandbox`** 对内联 SVG/JS 有效但**与 `<img src>` 渲染冲突**（CSP sandbox 禁用脚本但允许图片——`assetContentHandler` 走 302 redirect 到 SignedURL，浏览器不渲染 CSP 头）；实际生效面是 `/api/blob/{key...}`（`m2handlers.go:274-298`）—— 注释（`m2handlers.go:293-294`）明确这一点。

6. **`Content-Type` 来自 `.ct` sidecar**（`localfs.go:54-55, 67`）—— **不被 mime sniff 信任**，但攻击者 Put 时若伪造 `image/svg+xml`，配合 `X-Content-Type-Options: nosniff`+`sandbox` 仍安全；nosniff 不检查实际字节，**完全信 sidecar**。

7. **M5 是 `ALTER ADD COLUMN`**，但 `base_url` 与 `api_key_enc` 都无 NOT NULL 默认值——`ListByOrg` 用 `COALESCE(base_url,'')` 兜底（`models/store.go:323, 348`），老 row 安全。

8. **`AuthMigrate` 在 `az.Migrate` 之内**（`main.go:111-115`），sentinel org 紧跟其后（`main.go:121`）—— 若 `az.Migrate` 未跑 sentinel 就被插，FK 23503 必现。启动顺序是隐式契约。

9. **平台管理员 seed "已注册者即刻，未注册者后补"是双触发**（`main.go:125` + `register.go:67-71`）：中间窗口期内若 `EnsureSentinelOrg` 失败则 Seed 静默不报——`main.go return nil, nil, err`（`main.go:127`）会**阻断启动**，OK。

10. **secretbox 主密钥未做 key-id 索引**：未来加轮换时，需要扫表 + 重加密 + 写新格式（version+key_id 头），**M8 范围内未规划**。

11. **M8 报告范围外**：未发现 generation / asset 表上对 `model_configs.id` 的 FK（`storage.go:147-162` 中 `generations` 的 `provider/model` 是字符串列，无 FK 到 `model_configs`）—— 删 model_config 不会卡任何历史行，但 UI 列表可能显示"已删除模型"的历史资产。

---

## 横向参考

- 单 run 调用链：[architecture/run-flow.md](./architecture/run-flow.md)
- 全景子系统图：[architecture/subsystem-map.md](./architecture/subsystem-map.md)
- M1 实现剖析：[m1-implementation-deep-dive.md](./m1-implementation-deep-dive.md)
- M2 实现剖析：[m2-implementation-deep-dive.md](./m2-implementation-deep-dive.md)
- M4 实现剖析：[m4-implementation-deep-dive.md](./m4-implementation-deep-dive.md)
