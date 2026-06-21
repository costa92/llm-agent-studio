# Subsystem 全景图（onboarding 用）

> 用途：新人 onboarding 第一站。看完这张图 + Keystones 后能粗略回答"studio 由哪些子系统组成 / 我要改 X 该看哪个包"。
> 范围：所有 `internal/` 包 + `cmd/studiod` + 外部依赖（LLM 提供商 / 对象存储 / 浏览器 SPA）。
> 不适合：跟踪单次请求的完整路径（见 [run-flow.md](./run-flow.md)）。

---

## 项目定位（30 秒版）

`llm-agent-studio` = **"AI Studio — Multi-Agent 内容生产平台"**，是 `llm-agent` 生态里的一个 sibling 案例项目（独立仓 / 独立版本）。

把 `llm-agent` + `llm-agent-providers` + `llm-agent-authz` + `llm-agent-otel` 串成一个**端到端 SaaS 形态**的内容生产系统：

> 用户提一个项目 brief → LLM Planner 拆解 todo 图 → 文本/图片/视频/音频 agent 流水线生成 → 人工 HITL 审核 → 资产入库与计费。

---

## 一张图

```
                              EXTERNAL
   ┌──────────┐    ┌────────────────┐    ┌────────────────────┐
   │ Browser  │    │ LLM providers  │    │ Object storage     │
   │ (SPA M5) │    │ openai/anthro/ │    │ S3 / OSS / COS /   │
   │ React 19 │    │ google/ollama/ │    │ GitHub / localfs   │
   │ TanStack │    │ runway/kling/  │    │                    │
   │ + Vite   │    │ veo/tts        │    │                    │
   └────┬─────┘    └────────▲───────┘    └─────────▲──────────┘
        │ HTTPS+SSE          │ HTTPS+key            │ HTTPS+sig
        ▼                    │                      │
┌───────────────────────────────────────────────────────────────────────────┐
│                            STUDIOD (single binary)                        │
│  ┌──────────────────────────────────────────────────────────────────┐     │
│  │ httpapi  (mux: METHOD /path)                                     │     │
│  │   middleware chain:                                              │     │
│  │     authOnly  →  scoped(role, scope) [llm-agent-authz]           │     │
│  │     proj / asset / platformAdmin (scope-specific factories)      │     │
│  │   handlers: m1/m2/m3/m4/m8 + platform + member + taskboard + sse │     │
│  └──────────────────┬───────────────────────────────────────────────┘     │
│                     │                                                     │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │ studiosvc  (business orchestration)                                 │  │
│  │  Register / Org / OrgList / Members / Platform / Artifacts /        │  │
│  │  TaskBoard                                                          │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
│                                                                           │
│  ┌──────────────┐    ┌─────────────────────────────────────────────────┐  │
│  │ planner      │    │ worker pool (N goroutines)                      │  │
│  │ LLM 全图     │◄───┤   claim: SELECT ... FOR UPDATE SKIP LOCKED      │  │
│  │ + 回落       │    │   dispatch switch (script/storyboard/asset)     │  │
│  └──────────────┘    │   lease HB (LEASE_RENEW_INTERVAL < WORKER_LEASE)│  │
│         ▲            │   async: AsyncGenerator type assertion          │  │
│         │            │     submit→poll 多次短 dispatch                 │  │
│         │            │     idemKey = sha256(todoID)[:16]               │  │
│         │            │     3 caps: submit-admission / kind / 24h org   │  │
│         │            └─────┬───────────────────────────────────────────┘  │
│         │                  │ uses                                         │
│  ┌──────┴───────┐  ┌───────▼────────┐  ┌─────────────────────────┐        │
│  │ ModelRouter  │  │ agents         │  │ generate (Adapter)      │        │
│  │ per-org BYOK │  │  ScriptAgent   │  │  Fake / FakeAsync       │        │
│  │ → BuildChat  │  │  Storyboard    │  │  image (sync)           │        │
│  │ → BuildMedia │  │  AssetAgent    │  │  video/audio (async)    │        │
│  │              │  │  ReviewAgent   │  │   • Runway/Kling/Veo/TTS│        │
│  └──────┬───────┘  └────────────────┘  │     [M5 stubs key-gated]│        │
│         │                              └─────────────────────────┘        │
│  ┌──────▼───────┐  ┌─────────────────────────────────────────────┐        │
│  │ models       │  │ StorageRouter                               │        │
│  │ (BYOK CRUD)  │  │  resolve: per-org(enabled) → global(enabled)│        │
│  │ api_key_enc  │  │           → localfsDefault                  │        │
│  │ (AES-256-GCM)│  │  ★ PR #24: per-org localfs 拒收             │        │
│  └──────┬───────┘  └──────┬──────────────────────────────────────┘        │
│         │                 │ build                                         │
│         ▼                 ▼                                               │
│  ┌──────────────────────────────────────────┐                             │
│  │ secretbox (AES-256-GCM, stdlib only)     │                             │
│  │   STUDIO_CONFIG_ENC_KEY (base64 32B)     │                             │
│  │   nonce ‖ ct  (无 version/key-id)        │ ⚠ Issue #22 轮换 RFC        │
│  │   keep-or-replace: SQL CASE 原子分支     │                             │
│  └──────────────────────────────────────────┘                             │
│                                                                           │
│  ┌──────────────────────────────────────────────────────────────────┐     │
│  │ blob backends (BlobStore: Put/SignedURL/Delete - 不代理字节)     │     │
│  │   localfs (HMAC, /api/blob/{key}?sig=)  s3 (minio SigV4)         │     │
│  │   oss (OSS 官方签名)  cos (派生 endpoint, 复用 s3)               │     │
│  │   github (Contents API, sha-then-PUT, raw 直链, 公开仓库 only)   │     │
│  └──────────────────────────────────────────────────────────────────┘     │
│                                                                           │
│  ┌───────┐ ┌───────┐ ┌───────┐ ┌───────┐ ┌───────┐ ┌──────────────────┐   │
│  │events │ │ cost  │ │limits │ │ fetch │ │ obs   │ │  todos/project   │   │
│  │ (SSE) │ │ledger │ │ quota │ │SSRF-  │ │ otel  │ │  /assets stores  │   │
│  │+id:seq│ │µs/sec │ │advis  │ │safe   │ │decorat│ │  (GORM handle)   │   │
│  │★PR#17 │ │µs/unit│ │lock   │ │512MB  │ │not hk │ │                  │   │
│  └───────┘ └───────┘ └───────┘ └───────┘ └───────┘ └──────────────────┘   │
└────────────────────────────────┬──────────────────────────────────────────┘
                                 │ GORM (database/sql + pgx stdlib 驱动)
                                 │ ‖ pgxpool 仅存于 storage 地基，供 authz/CLI/测试
                                 ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                              POSTGRES                                    │
│                                                                          │
│  llm-agent-authz tables:  auth_user / auth_org / auth_membership         │
│   ─ sentinel auth_org(id='') 给 platform 角色作 FK 父行                  │
│                                                                          │
│  studio tables:                                                          │
│   projects(id, org_id, status, created_by)                               │
│   plans(project_id, valid, fallback_used, raw_plan_json)                 │
│   todos(id, type, status, depends_on[], attempts, poll_attempts,         │
│         locked_by, locked_until, next_run_at, input_json)                │
│   scripts(project_id, version, content_json)                             │
│   shots(storyboard JSON per shot)                                        │
│   assets(id, type, status, blob_key, external_job_id, submitted_at,     │
│          version, parent_asset_id)  -- assets_todo_uniq partial idx      │
│   generations(asset_id, todo_id, provider, model, cost_micros,           │
│               video_seconds)  -- generations_asset_todo_uniq partial     │
│   run_events(seq BIGSERIAL, kind, payload, project_id)                   │
│   pricing(provider, model, kind, micros_per_unit, micros_per_second)     │
│   model_configs(org_id, provider, model, base_url, api_key_enc BYTEA,    │
│                 is_default, kind)                                        │
│   storage_configs(scope global|org, mode, secret_enc BYTEA, ...)         │
│     -- 2 partial unique idx: global / per-org                            │
└──────────────────────────────────────────────────────────────────────────┘
```

---

## 数据访问层（model + GORM 混合）

所有 `internal/*` store / service 的数据访问都走 **GORM 句柄**（`storage.Storage.GORM()`，`gorm.io/driver/postgres` + pgx stdlib 驱动）。PR #79–#96 把全部 store 包（prompt/mailconfig/models/events/cost/assets/todos/workflows/storageconfig/project/worker）+ 服务层（studiosvc/planner/review/health）从 pgx/pgxpool 迁完。约定：

- **混合**：CRUD / 简单读走 GORM 链式 API；`RETURNING`、事务、`FOR UPDATE`、`pg_advisory_xact_lock`、`ON CONFLICT`、递归 CTE、`FILTER` 聚合、部分唯一索引敏感的写**保留原生 SQL**，在同一 `*gorm.DB` 上以 `Raw/Exec/Transaction` 跑。
- **不对既有表 AutoMigrate**：schema 仍由 `internal/storage/storage.go` 的 `m1..m17` 迁移脚本管理（部分唯一索引等 AutoMigrate 复现不了）。GORM 模型仅作映射层。
- **写返回落盘行一律 `INSERT...RETURNING`**（不用 `gorm.Create` 回填，避免 Go 时钟纳秒与 DB 微秒漂移）。
- **`pgxpool` 仅存于 `storage` 地基**：`Open` 同时建 pgxpool + GORM 池（同一 DSN），`GORM` 池已设连接上限（MaxOpen=25/MaxIdle=10/Lifetime=1h/IdleTime=30m）。pgxpool 现仅供外部 `llm-agent-authz`（`authzstore.New(*pgxpool.Pool)`）+ `cmd/secretbox-rotate` + 测试 fixture；彻底退役需 authz 库自身迁移（lockstep）。
- **DB-backed 测试**：`health` / `worker` 用 `internal/dbtest` 在 `TestMain` 建 per-package 独立库（前者全库扫描、后者全局队列 claim，须与兄弟包隔离）。

---

## Keystones / 不变式

读代码前先读这张表。**违反任何一条都是 PR 评审 block 项**。

| # | 不变式 | 强制点 | 为什么 |
|---|---|---|---|
| K1 | **不代理字节** —— studiod 出 `SignedURL`，前端直连存储取件 | `BlobStore` 接口仅 `Put/SignedURL/Delete` | 大文件不走 studiod 内存 |
| K2 | **capabilities per-(provider × model)**，不是 per-provider | `models.Catalog` + `MediaGeneratorFor(kind)` | 同 provider 不同 model 能力差异巨大（gpt-4o vs gpt-3.5） |
| K3 | **otel 是装饰器**（`Wrap(inner)`），不是 hook | `obs.WrapModel` / `obs.WrapAgent` / `obs.WrapGenerator` | hook 模式让 type assertion 失效（K6 案例：otel wrap 必须保留 `AsyncGenerator` 接口） |
| K4 | **streaming 事件按 seq 单调** | `run_events.seq BIGSERIAL`，SSE `id:<seq>` | 重连去重 + 顺序保证 |
| K5 | **lease HB ctx 派生自父 ctx，不绑 dispatch ctx** | `worker.go:266` `hbCtx := context.WithCancel(ctx)`，双 guard `WHERE locked_by=$3 AND status='running'` | `CallTimeout` 触发 dispatch ctx cancel 时不能误杀 heartbeat |
| K6 | **idemKey 确定性** `sha256("studio-submit:"+todoID)[:16]` | `worker.go:1233-1236`，FakeAsync 同模式回显 | 崩溃重启后同 todoID 必算出同 key，让 provider 用 client-token 去重 |
| K7 | **3 道并发上限性质不同**：submit-admission(软,跨 org) / kind(软,本地 OOM) / 24h(硬, per-org, advisory-lock) | 见 [run-flow.md](./run-flow.md#三道并发上限m4-§6-速查) | billing-sensitive 硬限不能软；本地内存软限避免 race 过严 |
| K8 | **密文经 secretbox AES-256-GCM 加密入库**，DTO 永不回密文 | `(api_key_enc IS NOT NULL) AS has_api_key` SQL 计算列 | secret 永不出 DB；HTTP 响应仅 `hasApiKey/hasSecret` 布尔 |
| K9 | **`GOWORK=off` 跑全部测试** | 项目惯例 + sibling 仓约定 | `go.work` 是 dev only，会让本地 resolve 偏离 go.mod 锁定版本 |

---

## 包目录索引

按"我要改 X 该看哪里"组织：

| 想做的事 | 主要包 | 测试位置 |
|---|---|---|
| 加新 HTTP 端点 | `internal/httpapi/` | `httpapi/*_test.go` (单测) |
| 改 LLM 全图规划逻辑 | `internal/planner/` | `planner/*_test.go` |
| 改 todo 状态机 / 加列 | `internal/todos/` + `internal/storage/storage.go` | `todos/*_test.go`（integration，需 pg） |
| 加 worker 新 todo type | `internal/worker/worker.go` (switch) + `internal/agents/` | `worker/*_test.go`（integration） |
| 加 async provider 适配器 | `internal/generate/{video,audio}/` | `generate/*_test.go` |
| 加 blob 后端 | `internal/blob/<name>/` + `cmd/studiod/main.go` (factory) | `blob/<name>/*_test.go` |
| 改 SSE 事件白名单 | `internal/httpapi/sse.go` (`sseEventNames`) | `httpapi/sse_test.go` |
| 加 BYOK 字段 | `internal/models/` + `internal/storage/storage.go` (schema) | `models/*_test.go`（integration） |
| 加平台管理面板端点 | `internal/httpapi/platformhandlers.go` + `internal/studiosvc/platform.go` | `httpapi/platformhandlers_test.go` |
| 改前端 | `web/src/features/<feature>/` | `web/src/features/<feature>/*.test.tsx` (vitest) |
| 改加密原语 | `internal/secretbox/` | `secretbox/*_test.go` |
| 加 SSRF 防御 | `internal/fetch/fetch.go` | `fetch/*_test.go` |

---

## 测试形态速查

| 类型 | 怎么跑 | 何时 |
|---|---|---|
| 后端单测（无 DB 依赖） | `GOWORK=off go test ./internal/<pkg>/ -count=1` | 改 handler / pure func / 接口 |
| 后端集成测（需 PG） | `LLM_AGENT_STUDIO_PG_URL=postgres://... GOWORK=off go test ./internal/<pkg>/ -count=1` | 改 store / worker / cross-pkg 流程 |
| 后端全套 | `GOWORK=off go test ./... -count=1` | PR 前最后一道 |
| 前端单测 | `pnpm -C web test` | 改 web/src/features/ |
| 前端构建 | `pnpm -C web build` | PR 前 |
| 前端 lint | `pnpm -C web lint` | PR 前 |
| 起本地全栈（含 pg/minio/otel） | `docker compose up` | 端到端手测 |

测试约定来自 `CLAUDE.md`（生态根） + `README.md`（本仓）。

---

## 里程碑 / 演进史

按时间顺序（最近 = 顶）：

| 里程碑 | Tag | 关键交付 |
|---|---|---|
| post-v0.8.0 (main) | — | GitHub blob 后端、平台超级管理员、用户管理、**数据层 model+GORM 迁移**（PR #79–#96，全量完成） |
| M8 | v0.8.0 | 可配置化 + BYOK + secretbox 加密 + DB 存储配置 |
| M7 | v0.7.0 | OSS/COS blob 后端 |
| M6 | v0.6.0 | 单进程全栈（`WEB_DIR` 静态托管 SPA） |
| M5 | v0.5.0 | React SPA（前端专属，后端零改动） |
| M4 | v0.4.0 | 异步视频/音频引擎（submit→poll） |
| M3 | v0.3.0 | 准生产横切（真实计价 / 模型路由 / 限流 / SSRF / cancel） |
| M2 | v0.2.0 | 图片生成 + HITL + 资产库 + 用量账本 |
| M1 | v0.1.0 | 文本管线骨架（planner + worker + SSE + RBAC + otel） |

各里程碑详细范围见 README §里程碑。

---

## 已知决策点 / Issues

| Issue | 性质 | 概述 |
|---|---|---|
| [#21](https://github.com/costa92/llm-agent-studio/issues/21) | 决策 | `MAX_CONCURRENT_VIDEO/AUDIO` 是跨 org 全局还是 per-org？文档化 vs 改 SQL |
| [#22](https://github.com/costa92/llm-agent-studio/issues/22) | RFC | secretbox 密文无 version/key-id → 密钥轮换路径设计 |
| [#23](https://github.com/costa92/llm-agent-studio/issues/23) | 决策 | 用户删除后 `created_by` 悬空 → FK SET NULL / 软删 / UI 容错 |

---

## 横向参考

- 单 run 路径详图：[run-flow.md](./run-flow.md)
- 里程碑实现深扫：
  - [M1 文本管线骨架](../m1-implementation-deep-dive.md) — planner + worker + SSE + RBAC + otel
  - [M2 图片生成 + BYOK 模型路由 + 成本账本](../m2-implementation-deep-dive.md)
  - [M4 异步视频/音频引擎 (submit→poll + heartbeat)](../m4-implementation-deep-dive.md)
  - [M8 BYOK + 存储配置 + 平台超级管理员 + secretbox 加密](../m8-implementation-deep-dive.md)
- M4 延后项与已知窗口：[../m4-deferred.md](../m4-deferred.md)
- M5 延后项：[../m5-deferred.md](../m5-deferred.md)
- 生态根仓导航：`../../CLAUDE.md`（ecosystem 级别规则）
