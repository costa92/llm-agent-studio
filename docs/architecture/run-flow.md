# Run-flow 调用链（紧凑视图）

> 用途：PR 描述里贴一张图给 reviewer 快速定位"这个改动影响哪一段"。
> 范围：单条 `POST /api/projects/{id}/workflows/{wfId}/run` 请求从 HTTP 入口到 `run_done` 事件的完整路径。
> 适合：reviewer / 故障排查 / 单次 run 行为分析。
> 不适合：横向看 subsystem 关系（见 [subsystem-map.md](./subsystem-map.md)）。

---

## 一图流

```
    POST /api/projects/{id}/workflows/{wfId}/run   body: {"inputs": {...}} 可选
                       │
                authz: proj(roleEditor)
                       ▼
   ┌──────────────────────────────────────────────────────┐
   │ httpapi.runWorkflowHandler (workflowhandlers.go:266) │
   │   加载 workflow 行 → 解析 nodes JSON                 │
   │   ValidateCustomGraph (环检测/白名单, 副作用前 400)  │
   │   HasUnboundCustomNode → 400 (未绑定 custom 节点)    │
   │   runinputs.Validate(inputs_schema, body.inputs)     │
   │   quota gate → SetStatus(planning)                   │
   │   events.Append(planner_started)                     │
   │   Planner.PlanCustom(projectID, workflowID, nodes)   │
   │     ├─ DAG 逐节点翻译 (无 LLM 拆解)                  │
   │     ├─ prompt: promptText > promptId(builtin/库)     │
   │     ├─ varBindings 两遍改写 local id → todo id       │
   │     └─ todos.CreateGraph (DAG: depends_on[])         │
   └─────────────┬────────────────────────────────────────┘
                 │ writes:
                 ▼
   ┌──────────────────────┐    ┌────────────────────────────┐
   │ todos (ready/blocked)│    │ run_events (seq+1)         │
   └─────┬────────────────┘    └──────────▲─────────────────┘
         │                                │ events.Append(...)
   N workers claim:                       │
   SELECT ... FOR UPDATE SKIP LOCKED      │ ┌──────────────────────────┐
   (+ MaxConcurrentGen 软上限子查询)      └─┤ SSE /events/stream       │
         │                                  │  连上先发权威 state 帧,  │
         ▼                                  │  replay 历史 + id:<seq>  │
   ┌──────────────────────────┐             │  + Last-Event-ID 重连    │
   │ dispatch: executors map  │             │  + state 版本变更再推帧  │
   │  • runScript             │             └──────────────────────────┘
   │  • runStoryboard ──┐     │
   │  • runAsset        │     │  ┌──────────────────────────────────┐
   │  • runPrescreen    │     │  │ custom:* fallback → runCustom    │
   │  • custom:* ───────┼─────┼─►│  switch input_json.kind:         │
   └────────┬───────────┘     │  │   llm    → ChatModel 调用        │
            │                 │  │   http   → SSRF-safe fetch       │
            │            ┌────▼──│   script → Starlark 沙箱         │
            │            │fan-out│  {{var}} 取值: expr 引擎（唯一   │
            │            │N×asset│   通道，项目 scoped fail-closed）│
            │            │(每镜头│  产物 → node_outputs (content    │
            │            │ todo) │   + items 双写)                  │
            │            └───────┘└──────────────────────────────────┘
            ▼
   routed.(AsyncGenerator)?
            │
       ┌────┴────┐
       ▼         ▼
    sync       async  (image/audio 均同步真实；async=submit→poll 经 FakeAsync 沙箱活验，无真实 video 适配器)
   (image)    ┌────────────────────────────────────────┐
              │ runAssetAsync (worker.go:1117)         │
              │  short-circuit: submitted+jobid        │
              │    → pollAsync                         │
              │  precondition: asset.Status must be    │
              │    'generating' else fail-fast         │
              │  submit-admission cap (双层 global +   │
              │    per-org) → 满则 reschedule 不扣费   │
              │                                        │
              │  submit → submitTx (单事务):           │
              │   ① pg_advisory_xact_lock(org)        │
              │   ② quota count (24h 硬限)            │
              │   ③ UPDATE assets → submitted         │
              │   ④ INSERT generations ON CONFLICT    │
              │   ⑤ reschedule todo poll_attempts=0   │
              │                                        │
              │  poll (多次短 dispatch):               │
              │   PollDone → fetch (SSRF-safe 512MB)  │
              │     → BlobStore.Put                    │
              │     → SetBlob → pending_acceptance     │
              └────────────────────────────────────────┘
                              │
                              ▼
                  HITL: accept / reject / regenerate
                       (POST /api/assets/{id}/..., admin)
                              │
                              ▼
                    run_done event (advisory-lock 防双发)
                              │
                              ▼
              预览（阅读器/播放）/ 导出：POST /api/projects/{id}/exports
              → export_jobs 队列 → exports.Runner → picturebook 渲染器
                (RenderPDF/RenderEPUB/RenderZip, 复用而非绘本残留)
```

---

## 关键 file:line 锚点（按图自上而下）

| 节点 | 文件 | 行号 | 说明 |
|---|---|---|---|
| 路由表 + middleware 装配 | `internal/httpapi/httpapi.go` | 151-192 | `NewMux`(151) + `authOnly/scoped/proj/asset/export/platformAdmin` factory |
| workflow run 路由 | `internal/httpapi/httpapi.go` | 221 | `POST /api/projects/{id}/workflows/{wfId}/run`（唯一 run 入口） |
| runWorkflowHandler | `internal/httpapi/workflowhandlers.go` | 266 | 图校验 + 运行期输入校验 + quota + PlanCustom |
| 运行期输入校验 | `internal/runinputs/` | — | `Validate(schema, inputs)`，纯逻辑无 DB |
| WorkflowNode 结构 | `internal/planner/planner.go` | 61-93 | `{id,type,promptId,promptText,dependsOn,typeId,varBindings,...}` |
| 图校验 / 环检测 | `internal/planner/planner.go` + `graph.go` | 126, 109 | `ValidateCustomGraph` / `checkAcyclic` |
| 未绑定 custom 节点拒跑 | `internal/planner/planner.go` | 190 | `HasUnboundCustomNode` |
| PlanCustom 主入口 | `internal/planner/planner.go` | 205 | DAG → todos，无 LLM 拆解 |
| prompt 取值优先级 | `internal/planner/planner.go` | 291-308 | 内联 promptText > promptId（builtin 预设 / prompts 表） |
| todos.CreateGraph | `internal/todos/store.go` | 41 | 根→`ready`，从→`blocked` |
| events.Append | `internal/events/store.go` | 32 | `RETURNING seq`，PG BIGSERIAL |
| SSE handler | `internal/httpapi/sse.go` | 45 | 连上先发 state 帧 → replay → poll 推送 |
| SSE 事件白名单 | `internal/httpapi/sse.go` | 23 | `sseEventNames`（state 帧不走白名单） |
| Last-Event-ID 重连 | `internal/httpapi/sse.go` | 63 | 错误回退全量回放 |
| 后端权威 state 计算 | `internal/projectstate/state.go` | 167 | `Compute`；REST 版在 httpapi.go:206 `GET /state` |
| Worker 主循环 | `internal/worker/worker.go` | 208, 222 | `RunOnce` / `Run` |
| Worker claim SQL | `internal/worker/worker.go` | 245-300 | `FOR UPDATE SKIP LOCKED`，含 stuck-reclaim + MaxConcurrentGen 软上限 |
| lease HB (K5) | `internal/worker/worker.go` | 323 | `hbCtx` 派生自父 ctx，不绑 dispatch ctx |
| dispatch（executors map） | `internal/worker/worker.go` | 170-185, 344-360 | script/storyboard/asset/prescreen（`w.executors`）+ `custom:*` fallback |
| runScript / runStoryboard | `internal/worker/worker.go` | 450, 488 | storyboard 完成后 `AddDynamic` 扇出（:577） |
| runAsset（sync/async 分叉） | `internal/worker/worker.go` | 617, 690 | `AsyncGenerator` type assertion；sync 路径扇出中 quota 复查（:704-711） |
| runPrescreen | `internal/worker/worker.go` | 978 | 上游文本安全评分，写回 asset prescreen 字段 |
| runCustom 分发 | `internal/worker/worker.go` | 1714 | switch `input_json.kind`：llm(:1757) / http(:1821) / script(:1961) |
| {{var}} 取值（expr 引擎，唯一通道） | `internal/worker/expr_resolver.go` | — | `resolveVariablesExpr`：$node 路径，项目 scoped + 直接 depends_on + fail-closed；legacy 双通道与 flag 已删（items cut-over PR-C） |
| Starlark 沙箱 | `internal/scriptengine/engine.go` | — | 无 I/O builtins；错误分类为不透明枚举 |
| items 通道（权威） | `internal/worker/worker.go` + `items_canonical.go` | — | `loadInputs`/`itemsForDep`/`loadInputsByDep`——执行期输入的唯一通道；`itemsForDep` 保留 output_ref 投影回退（在途 run 兼容，★M-4） |
| runAssetAsync 入口 | `internal/worker/worker.go` | 1117 | async path |
| short-circuit poll | `internal/worker/worker.go` | 1135 | `submitted + ExternalJobID` |
| submit precondition | `internal/worker/worker.go` | 1150 | 非 `generating` 状态 fail-fast（防死循环） |
| submit-admission cap | `internal/worker/worker.go` | 1222, 1228 | `submitCapHeld`：双层 global(:1223) + per-org(:1228)（issue #21 落地） |
| idemKey (K6) | `internal/worker/worker.go` | 1483 | `sha256("studio-submit:"+todoID)[:16]` |
| submitTx | `internal/worker/worker.go` | 1242 | 单事务（advisory-lock 硬配额 + ledger + reschedule） |
| pollAsync | `internal/worker/worker.go` | 1316 | 多次短 dispatch 状态查询 |
| reaper | `internal/worker/reaper.go` | 14 | TTL 终态化 stuck `submitted` |
| MediaGenerator.Generate | `internal/generate/{image,audio}/` | — | image=同步真实；audio=MiniMax T2A 同步真实（`audio/minimax.go`）；无 video 适配器；async 引擎经 `FakeAsync`（`fake_async.go`）沙箱活验 |
| SSRF-safe fetch | `internal/fetch/fetch.go` | — | `LimitReader`+512MB cap，DNS rebind 防御 |
| BlobStore.Put | `internal/blob/{localfs,s3,oss,cos,github}/` | — | 字节落地唯一处 |
| assets.SetBlob | `internal/assets/store.go` | 165 | `generating`/`submitted` → `pending_acceptance` |
| HITL 路由 | `internal/httpapi/httpapi.go` | 265-267 | accept/reject/regenerate（admin 门禁），实现在 `internal/review/` |
| run_done 写入 | `internal/events/store.go` | 59 | `AppendRunDone`，advisory-lock 防双发 |
| 导出端点 | `internal/httpapi/httpapi.go` | 243-246 | create/list/get + `/api/exports/{id}/content` |
| 导出 runner | `internal/exports/runner.go` | 99-102, 165 | `renderers` 映射 picturebook.RenderZip/PDF/EPUB；队列 `RunOnce` |

---

## 过渡态标注（读图必看）

| 项 | 现状 | 锚点 |
|---|---|---|
| **items/expr 已是唯一通道** | items cut-over 完成（PR-C）：执行期输入解析全部走 `loadInputs`/`itemsForDep`/`resolveVariablesExpr`（项目 scoped、fail-closed）；legacy 双通道、两个 flag 与 parity 探针已删除。`itemsForDep` 的 output_ref 投影回退保留（在途 run 兼容） | `docs/specs/items-cutover.md` §3 PR-C |
| **媒体生成器非骨架** | image=同步真实；audio=MiniMax T2A 同步真实（`audio/minimax.go` 显式不实现 `AsyncGenerator`）；video 无 provider 适配器。async submit→poll 引擎仍在，经 `FakeAsync` 沙箱条目活验 | `internal/generate/audio/minimax.go`、`fake_async.go:11` |

---

## 状态机速查

### todos.status 转换

| from | to | 触发方 | SQL 守门 |
|---|---|---|---|
| (none) | `ready` / `blocked` | `CreateGraph` (todos/store.go:41) | deps 空/非空 |
| `ready`/`running-stuck` | `running` | claim | `FOR UPDATE SKIP LOCKED` |
| `running` | `done` | `MarkDone` (:81) | `WHERE status='running'` |
| `blocked` | `ready` | `MarkDone` cascade | deps 全 done |
| `running` | `failed` | `MarkFailed` (:126) | + 递归 cancel dependents (:203) |
| `running` | `ready (retry)` | `worker.fail` | `WHERE status='running'` + `next_run_at=now()+backoff*2^(attempts-1)` |
| 任意非终 | `canceled` | `project.Cancel` / `MarkFailed` cascade | CTE 递归 |

### assets.status 转换（async 路径）

| from | to | 触发方 |
|---|---|---|
| (none) | `generating` | `GetOrCreateForTodo` |
| `generating` | `submitted` | `submitTx`（含 external_job_id） |
| `submitted` | `pending_acceptance` | `SetBlob`（poll-done 拉回后；sync image 路径为 `generating`→`pending_acceptance`） |
| `submitted`/`generating` | `failed` | `SetAsyncFailed`（终态分支） |
| `submitted` 超 TTL | `failed` | orphan reaper (reaper.go:14) |
| 任意非终 | `canceled` | `project.Cancel` 扫描 |
| `pending_acceptance` | `accepted` / `rejected` | HITL |

（注意：assets 没有 `done` 终态——采纳即 `accepted`。）

### project.status（聚合判定）

`planning`(无 todo) → `running`(任一 ready/running/blocked) → `failed`(failed>0) / `canceled`(canceled>0) / `review`(有 `pending_acceptance` 资产或在途 regenerate) / `completed`(全 done 且无待审)。

源码：`internal/project/status.go:30`（`DeriveStatus`）；前端渲染用的逐节点态由 `internal/projectstate/state.go:167`（`Compute`）叠加计算。

---

## 并发上限速查

| 维度 | 控制谁 | 强度 | 检查位置 |
|---|---|---|---|
| **claim 级 `MAX_CONCURRENT_GENERATIONS`** | 同时 `running` 的 asset todo 数 | 软（claim SQL 子查询，READ COMMITTED 下可瞬时超出 ≤ Workers-1） | `worker.claim` (worker.go:245, 子查询 254-263) |
| **submit-admission**（`CountInFlightByKind` / `...Org`） | 外部 provider 在途 job（双层：global `MAX_CONCURRENT_VIDEO/AUDIO` + per-org） | 软（TOCTOU；cap-hold reschedule 不扣 attempts/poll 预算） | `submitCapHeld` (worker.go:1222, 1228) |
| **org 24h 配额**（`pg_advisory_xact_lock`） | billing-sensitive 总额 | **硬** | `submitTx` (worker.go:1242+)；sync image 扇出中复查 (worker.go:704-711) |

---

## 横向参考

- 完整 subsystem 关系：[subsystem-map.md](./subsystem-map.md)
- 里程碑级深度剖析（历史资料，管线描述属绘本时代）：[../m2-implementation-deep-dive.md](../m2-implementation-deep-dive.md)
- M4 延后项：[../m4-deferred.md](../m4-deferred.md)
- M5 延后项：[../m5-deferred.md](../m5-deferred.md)
