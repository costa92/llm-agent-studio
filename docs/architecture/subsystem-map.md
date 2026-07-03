# Subsystem 全景图（onboarding 用）

> 用途：新人 onboarding 第一站。看完这张图 + Keystones 后能粗略回答"studio 由哪些子系统组成 / 我要改 X 该看哪个包"。
> 范围：所有 `internal/` 包 + `cmd/studiod` + 外部依赖（LLM 提供商 / 对象存储 / 浏览器 SPA）。
> 不适合：跟踪单次请求的完整路径（见 [run-flow.md](./run-flow.md)）。

---

## 项目定位（30 秒版）

`llm-agent-studio` = **"AI Studio — 自定义节点工作流编排平台"**，是 `llm-agent` 生态里的一个 sibling 案例项目（独立仓 / 独立版本）。

把 `llm-agent` + `llm-agent-providers` + `llm-agent-authz` + `llm-agent-otel` 串成一个**端到端 SaaS 形态**的内容生产系统：

> 用户建项目 → 在画布上编排自定义节点工作流（内置节点 + custom:\* 节点）→ 运行工作流生成 todo 图 → worker 租约队列执行 → 产出资产 → 人工 HITL 审核 → 预览 / 导出（PDF/EPUB/ZIP）与计费。

**产品转型（PR #149，2026-07）**：内置绘本（picturebook）管线与"brief → LLM Planner 自动拆解"标准管线已删除，DB 迁移 m23 把全部存量项目收敛为 `kind='custom'`（`internal/storage/storage.go:642`）。**「工作流」是唯一的执行模型**——plan 由用户编排的 DAG 直接生成，不再有 LLM 规划步骤。

---

## 一张图

```
                              EXTERNAL
   ┌──────────────┐    ┌────────────────┐    ┌────────────────────┐
   │ Browser SPA  │    │ LLM providers  │    │ Object storage     │
   │ React 19 +   │    │ openai/anthro/ │    │ S3 / OSS / COS /   │
   │ ReactFlow 画布│    │ google/ollama  │    │ GitHub / localfs   │
   │ TanStack+Vite│    │ (video/audio 为 │    │                    │
   │              │    │  骨架,必失败)   │    │                    │
   └────┬─────────┘    └────────▲───────┘    └─────────▲──────────┘
        │ HTTPS+SSE             │ HTTPS+key            │ HTTPS+sig
        ▼                       │                      │
┌───────────────────────────────────────────────────────────────────────────┐
│                            STUDIOD (single binary)                        │
│  ┌──────────────────────────────────────────────────────────────────┐     │
│  │ httpapi  (mux: METHOD /path)                                     │     │
│  │   middleware chain:                                              │     │
│  │     authOnly → scoped(role, scope) [llm-agent-authz]             │     │
│  │     proj / asset / export / platformAdmin (scope factories)      │     │
│  │   handlers: workflow CRUD+run / state / SSE / HITL / exports /   │     │
│  │             prompts / custom-node-types / platform / taskboard   │     │
│  └──────────────────┬───────────────────────────────────────────────┘     │
│                     │                                                     │
│  ┌─────────────────────────────────────────────────────────────────────┐  │
│  │ studiosvc  (business orchestration)                                 │  │
│  │  Register / Org / Members / Platform / Artifacts / TaskBoard        │  │
│  └─────────────────────────────────────────────────────────────────────┘  │
│                                                                           │
│  ┌──────────────┐    ┌─────────────────────────────────────────────────┐  │
│  │ planner      │    │ worker pool (N goroutines)                      │  │
│  │ PlanCustom:  │◄───┤   claim: SELECT ... FOR UPDATE SKIP LOCKED      │  │
│  │ 工作流 DAG   │    │   dispatch: executors map                       │  │
│  │ → todo 图    │    │     script/storyboard/asset/prescreen           │  │
│  │ (无 LLM 拆解)│    │     + custom:* → runCustom(llm/http/script)     │  │
│  └──────────────┘    │   lease HB (LEASE_RENEW_INTERVAL < WORKER_LEASE)│  │
│         ▲            │   async: AsyncGenerator type assertion          │  │
│         │            │     submit→poll 多次短 dispatch                 │  │
│  ┌──────┴───────┐    │     idemKey = sha256(todoID)[:16]               │  │
│  │ workflows    │    └─────┬───────────────────────────────────────────┘  │
│  │ (1:N/项目,   │          │ uses                                         │
│  │ nodes JSONB +│   ┌──────▼─────────┐  ┌────────────────────────────┐    │
│  │ inputs_schema│   │ agents         │  │ generate (Adapter)         │    │
│  └──────────────┘   │  ScriptAgent   │  │  Fake / FakeAsync          │    │
│  ┌──────────────┐   │  Storyboard    │  │  image (sync, 可真实)      │    │
│  │customnodetype│   │  AssetAgent    │  │  video/audio (async 骨架,  │    │
│  │ 注册表:      │   │  ReviewAgent   │  │   Submit 必返错, 待实现)   │    │
│  │ llm|http|    │   └────────────────┘  └────────────────────────────┘    │
│  │ script 三kind│   ┌────────────────┐  ┌────────────────────────────┐    │
│  └──────────────┘   │ expr 引擎      │  │ scriptengine (Starlark     │    │
│  ┌──────────────┐   │ $node/$json/   │  │  沙箱: 无 I/O builtins,    │    │
│  │ runinputs    │   │ $binary 表达式 │  │  step/time/output 限额)    │    │
│  │ 运行期输入   │   │ (ExprChannel   │  └────────────────────────────┘    │
│  │ schema 校验  │   │  默认 ON)      │                                    │
│  └──────────────┘   └────────────────┘                                    │
│  ┌──────────────┐  ┌─────────────────────────────────────────────────┐    │
│  │ exports      │  │ ModelRouter (per-org BYOK) → BuildChat/Media    │    │
│  │ export_jobs  │  │ StorageRouter: per-org(enabled)→global(enabled) │    │
│  │ 队列 → 复用  │  │               →localfsDefault                   │    │
│  │ picturebook  │  │ models (BYOK CRUD, api_key_enc AES-256-GCM)     │    │
│  │ 渲染器出     │  │ secretbox (AES-256-GCM, stdlib only)            │    │
│  │ PDF/EPUB/ZIP │  │   STUDIO_CONFIG_ENC_KEY (base64 32B)            │    │
│  └──────────────┘  └─────────────────────────────────────────────────┘    │
│                                                                           │
│  ┌──────────────────────────────────────────────────────────────────┐     │
│  │ blob backends (BlobStore: Put/SignedURL/Delete - 不代理字节)     │     │
│  │   localfs (HMAC, /api/blob/{key}?sig=)  s3 (minio SigV4)         │     │
│  │   oss (OSS 官方签名)  cos (派生 endpoint, 复用 s3)               │     │
│  │   github (Contents API, sha-then-PUT, raw 直链, 公开仓库 only)   │     │
│  └──────────────────────────────────────────────────────────────────┘     │
│                                                                           │
│  ┌───────┐ ┌───────┐ ┌───────┐ ┌───────┐ ┌───────┐ ┌──────────────────┐   │
│  │events │ │ cost  │ │limits │ │ fetch │ │ obs   │ │ projectstate     │   │
│  │ (SSE) │ │ledger │ │ quota │ │SSRF-  │ │ otel  │ │ (后端权威渲染态: │   │
│  │+id:seq│ │µs/sec │ │advis  │ │safe   │ │decorat│ │  Compute → REST  │   │
│  │+state │ │µs/unit│ │lock   │ │512MB  │ │not hk │ │  /state + SSE    │   │
│  │ frame │ │       │ │       │ │       │ │       │ │  state 帧)       │   │
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
│   projects(id, org_id, kind, status, created_by)                         │
│   workflows(project_id, name, nodes JSONB, inputs_schema)  -- 1:N        │
│   plans(project_id, workflow_id, valid, raw_plan_json)                   │
│   todos(id, type, status, depends_on[], attempts, poll_attempts,         │
│         locked_by, locked_until, next_run_at, input_json)                │
│   scripts / shots (script、storyboard 节点产物)                          │
│   node_outputs(todo_id, project_id, content, format, items JSONB)        │
│   custom_node_types(org_id, slug, kind llm|http|script, params)          │
│   assets(id, type, status, blob_key, external_job_id, submitted_at,      │
│          version, parent_asset_id)  -- assets_todo_uniq partial idx      │
│   generations(asset_id, todo_id, provider, model, cost_micros,           │
│               video_seconds)  -- generations_asset_todo_uniq partial     │
│   run_events(seq BIGSERIAL, kind, payload, project_id, workflow_id)      │
│   export_jobs(project_id, plan_id, format, status)  -- 导出队列          │
│   prompts(org_id, ...)  org_secrets(org_id, value_enc BYTEA)             │
│   pricing / model_configs(api_key_enc BYTEA) / storage_configs           │
│     -- storage_configs: 2 partial unique idx: global / per-org           │
└──────────────────────────────────────────────────────────────────────────┘
```

---

## 数据访问层（model + GORM 混合）

所有 `internal/*` store / service 的数据访问都走 **GORM 句柄**（`storage.Storage.GORM()`，`gorm.io/driver/postgres` + pgx stdlib 驱动）。PR #79–#96 把全部 store 包 + 服务层（studiosvc/planner/review/health）从 pgx/pgxpool 迁完。约定：

- **混合**：CRUD / 简单读走 GORM 链式 API；`RETURNING`、事务、`FOR UPDATE`、`pg_advisory_xact_lock`、`ON CONFLICT`、递归 CTE、`FILTER` 聚合、部分唯一索引敏感的写**保留原生 SQL**，在同一 `*gorm.DB` 上以 `Raw/Exec/Transaction` 跑。
- **不对既有表 AutoMigrate**：schema 由 `internal/storage/storage.go` 管理，两层结构——m1…m20 无条件 DDL（`IF NOT EXISTS` 自跳过）+ m21…m24 版本化 Go 迁移步骤（`goSteps`，`storage.go:534-543`，`schema_migrations` 表记录已执行版本）。GORM 模型仅作映射层。
- **写返回落盘行一律 `INSERT...RETURNING`**（不用 `gorm.Create` 回填，避免 Go 时钟纳秒与 DB 微秒漂移）。
- **`pgxpool` 仅存于 `storage` 地基**：`Open` 同时建 pgxpool + GORM 池（同一 DSN），GORM 池已设连接上限（MaxOpen=25/MaxIdle=10/Lifetime=1h/IdleTime=30m）。pgxpool 现仅供外部 `llm-agent-authz`（`authzstore.New(*pgxpool.Pool)`）+ `cmd/secretbox-rotate` + 测试 fixture；彻底退役需 authz 库自身迁移（lockstep）。
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
| K5 | **lease HB ctx 派生自父 ctx，不绑 dispatch ctx** | `worker.go:314` `hbCtx := context.WithCancel(ctx)`，renewLease 双 guard `locked_by` + `status='running'` | `CallTimeout` 触发 dispatch ctx cancel 时不能误杀 heartbeat |
| K6 | **idemKey 确定性** `sha256("studio-submit:"+todoID)[:16]` | `worker.go:1488-1491`，FakeAsync 同模式回显 | 崩溃重启后同 todoID 必算出同 key，让 provider 用 client-token 去重 |
| K7 | **并发上限分软硬**：claim 级 / submit-admission（软）与 org 24h 配额（硬, advisory-lock）性质不同 | 见 [run-flow.md](./run-flow.md#并发上限速查) | billing-sensitive 硬限不能软；本地内存软限避免 race 过严 |
| K8 | **密文经 secretbox AES-256-GCM 加密入库**，DTO 永不回密文 | `(api_key_enc IS NOT NULL) AS has_api_key` SQL 计算列 | secret 永不出 DB；HTTP 响应仅 `hasApiKey/hasSecret` 布尔 |
| K9 | **`GOWORK=off` 跑全部测试** | 项目惯例 + sibling 仓约定 | `go.work` 是 dev only，会让本地 resolve 偏离 go.mod 锁定版本 |

---

## 工作流执行模型（核心概念）

- **工作流是项目的一等子资源（1:N）**：`workflows` 表每行一条 DAG（`nodes` JSONB），每次运行产出一条 `plans` 行（`plans.workflow_id` 关联）。项目级 `POST /run` 是遗留通道（见下"过渡态"）。
- **节点即 `planner.WorkflowNode`**（`internal/planner/planner.go:65-93`）：`{id, type, promptId, promptText, dependsOn, typeId, varBindings, typeVersion, parameters}`。`dependsOn` 边是 DAG 真源。
- **内置节点 4 种**（`internal/builtinnode/catalog.go:15-20`）：`script`（剧本）/ `storyboard`（分镜，完成后按镜头扇出 asset todo）/ `asset`（图像/视频/音频生成）/ `prescreen`（上游文本安全评分）。
- **自定义节点 `custom:*`**：组织级注册表 `custom_node_types` 定义类型，`kind ∈ {llm, http, script}`（`internal/customnodetype/store.go:27`）；`script` kind 跑 Starlark 沙箱（`internal/scriptengine`，无 I/O builtins）。节点实例通过 `typeId` 绑定注册表类型，未绑定的 custom 节点是纯注释、拒绝运行（`planner.HasUnboundCustomNode`，planner.go:195）。
- **plan 生成无 LLM**：`planner.PlanCustom`（planner.go:210）把 DAG 校验（环检测 graph.go:109）后逐节点翻译成 todos（`todos.CreateGraph`），prompt 取值优先级：节点内联 `promptText` > `promptId`（builtin 预设 / org prompt 库）（planner.go:295-308）。
- **变量与表达式**：节点 `varBindings` 绑定上游输出；`ExprChannel` 开启时（默认 ON，`internal/config/config.go:146`）取值走 `internal/expr` 表达式引擎（`$node["id"].json.field` 路径），关闭（`STUDIO_EXPR_CHANNEL=0`）回退 legacy resolveVariables。
- **运行期输入**：workflow 可声明 `inputs_schema`，run 时 body 带 `{"inputs":...}`，`internal/runinputs` 做类型化校验与分流。
- **运行观测**：后端权威渲染态 `projectstate.Compute`（`internal/projectstate/state.go:167`）经 `GET /state`（httpapi.go:185）+ SSE `/events/stream` 的 state 帧下发；前端纯渲染。

### 过渡态（如实标注，勿当 bug 修）

| 项 | 现状 | 位置 |
|---|---|---|
| items 通道 cut-over 未完成 | `loadInputs`（items 通道）是 **ADDITIVE**——执行真源仍是 legacy `depends_on`/`output_ref`/resolveVariables 解析，items 仅供 parity 测试与后续 cut-over | `internal/worker/worker.go:2165`（P2a NOTE）+ `:2168` |
| ExprChannel 已默认 ON | `STUDIO_EXPR_CHANNEL=0` 可回退 legacy 解析；`STUDIO_EXPR_PARITY=1` 开 soak 对照探针 | `internal/config/config.go:145-146` |
| legacy 项目级 run 仍在 | `POST /api/projects/{id}/run` 保留，仅接受项目级嵌入式 custom workflow（`CustomWorkflowEnabled`），否则 400 引导走 `/workflows/{id}/run` | `internal/httpapi/httpapi.go:182` + `handlers.go:470-475` |
| 新项目默认 kind 仍写 `"standard"` | 建项目未传 kind 时落 `"standard"`（待 Phase 1 收敛为 `"custom"`） | `internal/project/store.go:150` |
| video/audio 生成是骨架 | `internal/generate/{video,audio}` 的 Submit/Poll 必返错（`video.go:17-20`），真实 SaaS HTTP 未实现 | `internal/generate/video/`、`internal/generate/audio/` |

---

## 包目录索引

按"我要改 X 该看哪里"组织：

| 想做的事 | 主要包 | 测试位置 |
|---|---|---|
| 加新 HTTP 端点 | `internal/httpapi/` | `httpapi/*_test.go` (单测) |
| 改工作流 CRUD / nodes 存储 | `internal/workflows/` | `workflows/*_test.go`（integration，需 pg） |
| 改 DAG→todo 图翻译 / 图校验 | `internal/planner/`（`PlanCustom` + `graph.go`） | `planner/*_test.go` |
| 加内置节点类型 | `internal/builtinnode/catalog.go` + `internal/worker/`（executors map）+ `internal/planner/` 白名单 | `builtinnode/` + `worker/*_test.go` |
| 改自定义节点注册表 / kind 校验 | `internal/customnodetype/` | `customnodetype/*_test.go` |
| 改 custom 节点执行（llm/http/script） | `internal/worker/worker.go`（`runCustom*`）+ `internal/scriptengine/` | `worker/*_test.go`（integration） |
| 改表达式引擎 | `internal/expr/` | `expr/*_test.go` |
| 改运行期输入校验 | `internal/runinputs/` | `runinputs/*_test.go` |
| 改 todo 状态机 / 加列 | `internal/todos/` + `internal/storage/storage.go` | `todos/*_test.go`（integration，需 pg） |
| 加 worker 新 todo type | `internal/worker/worker.go`（executors map）+ `internal/agents/` | `worker/*_test.go`（integration） |
| 改导出（PDF/EPUB/ZIP） | `internal/exports/`（队列 + runner）+ `internal/picturebook/`（渲染器，非绘本残留） | `exports/` + `picturebook/*_test.go` |
| 改后端权威渲染态 | `internal/projectstate/` | `projectstate/*_test.go` |
| 加 async provider 适配器 | `internal/generate/{video,audio}/` | `generate/*_test.go` |
| 加 blob 后端 | `internal/blob/<name>/` + `cmd/studiod/main.go` (factory) | `blob/<name>/*_test.go` |
| 改 SSE 事件白名单 / state 帧 | `internal/httpapi/sse.go` (`sseEventNames`) | `httpapi/sse_test.go` |
| 加 BYOK 字段 | `internal/models/` + `internal/storage/storage.go` (schema) | `models/*_test.go`（integration） |
| 改 prompt 库 / builtin 预设 | `internal/prompt/` | `prompt/*_test.go` |
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
| workflow-only pivot (main) | — | **PR #149：删绘本 + 标准 LLM-planner 两条管线，m23 收敛全部项目为 kind=custom**；工作流成品呈现（阅读器/TTS/导出，PR #151-#153）；审核流重设计（PR #158） |
| workflow v2 | — | 节点中心化：typed custom nodes（llm/http/script）、expr 表达式引擎、items 通道（过渡中）、运行期输入、ReactFlow 画布 |
| post-v0.8.0 | — | GitHub blob 后端、平台超级管理员、用户管理、数据层 model+GORM 迁移（PR #79–#96） |
| M8 | v0.8.0 | 可配置化 + BYOK + secretbox 加密 + DB 存储配置 |
| M7 | v0.7.0 | OSS/COS blob 后端 |
| M6 | v0.6.0 | 单进程全栈（`WEB_DIR` 静态托管 SPA） |
| M5 | v0.5.0 | React SPA（前端专属，后端零改动） |
| M4 | v0.4.0 | 异步视频/音频引擎（submit→poll） |
| M3 | v0.3.0 | 准生产横切（真实计价 / 模型路由 / 限流 / SSRF / cancel） |
| M2 | v0.2.0 | 图片生成 + HITL + 资产库 + 用量账本 |
| M1 | v0.1.0 | 文本管线骨架（planner + worker + SSE + RBAC + otel） |

---

## 已知决策点 / Issues

| Issue | 性质 | 概述 |
|---|---|---|
| [#21](https://github.com/costa92/llm-agent-studio/issues/21) | 决策 | submit-admission cap 全局 vs per-org → 已落地双层（global + per-org，`worker.go:1233-1240`） |
| [#22](https://github.com/costa92/llm-agent-studio/issues/22) | RFC | secretbox 密文无 version/key-id → 密钥轮换路径设计 |
| [#23](https://github.com/costa92/llm-agent-studio/issues/23) | 决策 | 用户删除后 `created_by` 悬空 → FK SET NULL / 软删 / UI 容错 |

---

## 横向参考

- 单 run 路径详图：[run-flow.md](./run-flow.md)
- 里程碑实现深扫（**历史资料**——M1/M2/M4 写于绘本/标准管线时代，管线描述已过时，运行时机制〔worker 租约 / async submit→poll / BYOK〕仍可参考）：
  - [M1 文本管线骨架](../m1-implementation-deep-dive.md)
  - [M2 图片生成 + BYOK 模型路由 + 成本账本](../m2-implementation-deep-dive.md)
  - [M4 异步视频/音频引擎 (submit→poll + heartbeat)](../m4-implementation-deep-dive.md)
  - [M8 BYOK + 存储配置 + 平台超级管理员 + secretbox 加密](../m8-implementation-deep-dive.md)
- M4 延后项与已知窗口：[../m4-deferred.md](../m4-deferred.md)
- M5 延后项：[../m5-deferred.md](../m5-deferred.md)
- 生态根仓导航：`../../CLAUDE.md`（ecosystem 级别规则）
