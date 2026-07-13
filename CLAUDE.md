# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

`llm-agent-studio` = **"AI Studio — 自定义节点工作流编排平台"**，`llm-agent` 生态里的一个独立
sibling 案例仓（独立 git 历史 / 独立版本 / 独立 CI）。把 `llm-agent` +
`llm-agent-providers` + `llm-agent-authz` + `llm-agent-otel` 串成端到端 SaaS：

> 用户建项目 → 画布上编排工作流（内置节点 + `custom:*` 节点）→ 运行 → planner 把 DAG
> 直接翻成 todo 图 → worker 租约队列执行 → 产资产 → HITL 人工审核 → 预览 / 导出（PDF/EPUB/ZIP）+ 计费。

**唯一执行模型是「工作流」**：plan 由用户编排的 DAG 直接生成，**没有 LLM 规划步骤**。
内置绘本管线与 "brief→LLM Planner" 标准管线已在 PR #149 删除，全部项目收敛为 `kind='custom'`。

单二进制 `cmd/studiod`。前端在 `web/`（React 19 + ReactFlow 画布 + TanStack Router/Query + Vite）。

## Commands

所有 Go 命令**必须 `GOWORK=off`**（K9：上层 umbrella 有 `go.work`，不关掉会用 workspace
掩盖 `go.mod` 真实依赖图 / 偏离锁定版本）。入口统一在 `Makefile`（已内置 `GOWORK=off`）：

```bash
make vet build test         # 快速路径：无 PG，DB-gated 测试自动 skip
make test-db                # 全量测试，须先 export LLM_AGENT_STUDIO_PG_URL 指向一个 fresh DB
make web-install web-test web-build   # 前端
make ci                     # CI 全量入口（web-lint 不进 ci：有预存红 baseline）
```

### 跑 DB-gated 测试（关键，最易踩坑）

大量测试（`cost` / `planner` / `worker` / `httpapi` / `studiosvc` / `storage*` …）是
DB-gated 的：无 `LLM_AGENT_STUDIO_PG_URL` 时**静默 skip**（`ok ... 0.00s` 是 skip 不是通过）。

- **一律 `-p 1 -count=1`**：并行迁移有 race，脏库会撞 transient 唯一索引（如 `assets_todo_uniq` / org_mode 唯一索引）。
- **一个包一个全新库**。**多包共享同一库会跨包污染**——`ByOrg` 类广聚合会撞到别包用同一 org id 种子的行 → 假失败（`TestAggregateByOrg` 就这样在共享库假红、各自 fresh 库全绿）。
- 测试自身跑 `st.Migrate`，无需预迁移；起库示例：

```bash
# 172.17.0.3 的 PG 常不可达；用 docker 自起一次性库（daemon 操作在本沙箱需 dangerouslyDisableSandbox）
docker run -d --rm -e POSTGRES_PASSWORD=pw -p 55432:5432 postgres:16-alpine
docker exec <cid> createdb -U postgres costonly     # 一包一库
# 单包 / 单测：
LLM_AGENT_STUDIO_PG_URL="postgres://postgres:pw@localhost:55432/costonly?sslmode=disable" \
  GOWORK=off go test ./internal/cost/... -run TestPerActorByOrg -count=1 -p 1 -v
```

`worker` / `health` 用 `internal/dbtest` 在 `TestMain` 自建 per-package 独立库（全局队列 / 全库扫描，须与兄弟包隔离）。

### 本地 dev runtime

`scripts/dev-restart.sh` 一条命令重编 + 重拉 `studiod`（:8083，真实 deepseek 文本模型模式）。
前置：`DEEPSEEK_API_KEY` 环境变量 + `/tmp/studio-enc-key.txt`（`STUDIO_CONFIG_ENC_KEY`）+
`/tmp/studio-jwt-secret.txt`（`JWT_SECRET`）。前端 Vite :5173 单独 `cd web && pnpm dev`。
dev 无 `/healthz`，就绪信号 = 登录 200。杀旧进程只按精确 PID + exe 真身（重编会把 exe 变
`/tmp/studiod (deleted)`，脚本已剥后缀）——**绝不 `pkill -f studiod`**（会误杀脚本自身）。

## Architecture — 先读这两份文档

架构不要从零重建，**先读**：

- **`docs/architecture/subsystem-map.md`** — 全景图 + Keystones 表 + `internal/` 包索引（onboarding 第一站，「改 X 该看哪个包」）。
- **`docs/architecture/run-flow.md`** — 单次 run 的完整调用链（PR 描述 / 故障排查 / 并发上限速查）。
- 里程碑深扫：`docs/m{1,2,4,8}-implementation-deep-dive.md`。

一句话数据流（细节见上面两份）：
`httpapi`（mux `METHOD /path` + authz 中间件链 `authOnly→scoped(role,scope)`）→ `studiosvc`
（业务编排）/ `planner.PlanCustom`（工作流 DAG→todo 图，纯翻译无 LLM）→ `todos` 表 DAG →
`worker` 池（`SELECT ... FOR UPDATE SKIP LOCKED` 认领 → executors map 分发
script/storyboard/asset/prescreen/`custom:*`；storyboard 扇出成 N 个 asset todo）→ `generate`
适配器出资产 → `projectstate.Compute` 后端权威渲染态经 REST `/state` + SSE state 帧下发前端。
前端**纯渲染后端态**，不自己推导。节点间取值唯一通道是 `expr` 引擎的 `{{$node["id"].json.field}}`（fail-closed）。

## Hard rules — Keystones（违反 = PR block）

完整表在 `subsystem-map.md`。最常踩的：

- **K1 不代理字节**：`BlobStore` 只 `Put/SignedURL/Delete`；前端直连存储取件。
- **K2 capabilities per-(provider × model)** 不是 per-provider。
- **K3 otel 是装饰器 `Wrap(inner)`** 不是 hook（hook 会破坏 `AsyncGenerator` type assertion）。
- **K4 SSE 事件按 `run_events.seq BIGSERIAL` 单调**，`id:<seq>` 支持重连去重。
- **K7 并发上限分软硬**：claim/submit-admission 软限（本地内存，避免 race 过严）vs org 24h 配额硬限（`pg_advisory_xact_lock`，billing-sensitive 不能软）。
- **K8 密钥经 secretbox AES-256-GCM 入库，DTO 永不回密文**：HTTP 响应只出 `hasApiKey/hasSecret` 布尔（`(api_key_enc IS NOT NULL) AS has_api_key` SQL 计算列）。

## 数据层约定（model + GORM 混合）

全部 store/service 走 `storage.Storage.GORM()`（pgx stdlib 驱动），但：

- **写落盘行一律原生 `INSERT ... RETURNING`**（`Raw`/`Exec` 逐字透传 `$N`），**不用 `gorm.Create`**（避免 Go 纳秒时钟与 DB 微秒漂移）。查询用 `gorm Raw` 逐字透传 `$N`。
- **不对既有表 `AutoMigrate`**：schema 全由 `internal/storage/storage.go` 管理。两层结构——
  无条件 DDL 块（`IF NOT EXISTS` 自跳过）+ 版本化 Go 迁移 `goSteps()`（`m21`…，
  `schema_migrations` 表记录已执行版本）。**新迁移追加到 `goSteps` 末尾、forward-only /
  additive / 幂等**，取当前最大编号 +1（合并多分支时若号段撞车，让号保序即可）。迁移函数签名 `func mXX...(ctx, tx pgx.Tx) error`。
- `RETURNING`/事务/`FOR UPDATE`/`ON CONFLICT`/advisory-lock/递归 CTE/`FILTER` 聚合/部分唯一索引敏感的写 **保留原生 SQL**，在同一 `*gorm.DB` 上 `Raw/Exec/Transaction` 跑。事务用 `db.Transaction`（单连接亲和，保 `FOR UPDATE` / advisory lock）。
- `NULL` 列用 `[]byte` / `pq.StringArray` 中转。
- `pgxpool` 仅存于 `storage` 地基（供外部 `llm-agent-authz` 的 `authzstore.New(*pgxpool.Pool)` + `cmd/secretbox-rotate` + 测试 fixture）；studio 自有生产代码 100% GORM，彻底退役 pgxpool 被 authz 库自身迁移阻塞（lockstep）。

## 改动落地流程

- **走 PR，别直推 `main`**：分支 → push → `gh pr create` → **owner 手动 `gh pr merge --rebase`**。本仓**无 CI 自动合并 / 无 auto-merge**——rebase-merge 落为单 commit。
- 提交前先 commit（防会话中断丢工作）；中文 commit message 说清「为什么」。
- 派实现 agent 后**主线程必须独立复验**：不能只信 agent 报告（会话中断 / LSP diagnostics 常 stale），也不能只信 `<new-diagnostics>`（可能是 mid-edit 快照）——以 `GOWORK=off go build/vet` + fresh-DB 实跑测试为准。
- 注释 / 错误提示跟随文件既有语言（多为中文）。前端 lint 有预存红 baseline：验证改动用「改动文件零新增 error + vitest + build」，不是全量 lint 绿。

## Generate 适配器现状（沙箱 vs 真实）

`generate` 适配器：`Fake`/`FakeAsync`（沙箱，image 也可回显 PNG）；`image` 同步真实；
`audio` MiniMax 同步真实；**`video` 无 provider 适配器**。org 若配了真实 model-config
会覆盖 fake（per-kind）。asset 生命周期**无 "done"**，终态用 `accepted`（HITL 采纳）。
