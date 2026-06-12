# Run-flow 调用链（紧凑视图）

> 用途：PR 描述里贴一张图给 reviewer 快速定位"这个改动影响哪一段"。
> 范围：单条 `POST /api/projects/{id}/run` 请求从 HTTP 入口到 `run_done` 事件的完整路径。
> 适合：reviewer / 故障排查 / 单次 run 行为分析。
> 不适合：横向看 subsystem 关系（见 [subsystem-map.md](./subsystem-map.md)）。

---

## 一图流

```
            POST /api/projects/{id}/run
                       │
                authz: proj(roleEditor)
                       │
                       ▼
   ┌──────────────────────────────────────────────────┐
   │ httpapi.runHandler         (handlers.go:261)     │
   │   Planner.PlanWith(model, brief)                 │
   │     ├─ LLM 一次性全图 → 校验 → fallback          │ ← M1: "动态" 是结构,不是续编
   │     └─ todos.CreateGraph (DAG: deps_on[])        │
   │   events.Append(planner_started)                 │
   └─────────────┬────────────────────────────────────┘
                 │ writes:
                 ▼
   ┌──────────────────────┐    ┌────────────────────────┐
   │ todos (ready/blocked)│    │ run_events (seq+1)     │
   └─────┬────────────────┘    └──────────▲─────────────┘
         │                                │
   N workers claim:                       │ events.Append(...)
   SELECT ... FOR UPDATE SKIP LOCKED      │
         │                                │ ┌──────────────────────────┐
         ▼                                └─┤ SSE /events/stream       │
   ┌──────────────────────────┐             │  PR #17: Last-Event-ID  │ ★
   │ dispatch switch          │             │  + id:<seq> line         │
   │  • runScript             │             └──────────────────────────┘
   │  • runStoryboard ──┐     │
   │  • runAsset        │     │
   └────────┬───────────┘     │
            │                 │ AddDynamic
            │            ┌────▼─────────────┐
            │            │ fan-out N×asset  │
            │            │  todo (per shot) │
            │            └──────────────────┘
            ▼
   routed.(AsyncGenerator)?
            │
       ┌────┴────┐
       │         │
       ▼         ▼
    sync       async  (M4)
   (image)    ┌────────────────────────────────────────┐
              │ runAssetAsync (worker.go:928)          │
              │                                        │
              │  ┌───────────────────────────────────┐ │
              │  │ short-circuit: submitted+jobid    │ │
              │  │   → pollAsync                     │ │
              │  └───────────────────────────────────┘ │
              │  ┌───────────────────────────────────┐ │
              │  │ ★ PR #20 precondition:           │ │
              │  │   asset.Status=='generating'      │ │
              │  │   else 400 (no provider waste)    │ │
              │  └───────────────────────────────────┘ │
              │                                        │
              │  submit → submitTx (单事务):           │
              │   ① pg_advisory_xact_lock(org)        │
              │   ② quota count (24h硬限)             │
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
                              │
                              ▼
                         run_done event
```

---

## 关键 file:line 锚点（按图自上而下）

| 节点 | 文件 | 行号 | 说明 |
|---|---|---|---|
| 路由表 + middleware 装配 | `internal/httpapi/httpapi.go` | 130-196 | `scoped/proj/asset/platformAdmin` 五个 factory |
| HTTP run handler | `internal/httpapi/handlers.go` | 261 | `runHandler`，调 planner + 写 run_events |
| Planner 主入口 | `internal/planner/planner.go` | 74 | `PlanWith(model, brief)` —— BYOK 入口 |
| Planner 全图生成 | `internal/planner/planner.go` | 68-101 | LLM 一次性图 → 校验三件事 → fallback |
| `DefaultPipeline()` 回落 | `internal/planner/graph.go` | 125-130 | 固定 `script→storyboard` 两节点 |
| todos.CreateGraph | `internal/todos/store.go` | 41-75 | 根→`ready`，从→`blocked` |
| events.Append | `internal/events/store.go` | 32-48 | `RETURNING seq`，PG BIGSERIAL |
| SSE handler | `internal/httpapi/sse.go` | 39-90 | replay 200 + ticker 500ms |
| **Last-Event-ID 解析（PR #17）** | `internal/httpapi/sse.go` | 51-60 | 重连支持，错误回退全量回放 |
| Worker 主循环 | `internal/worker/worker.go` | 168 | `Run` 循环 `RunOnce` |
| Worker claim SQL | `internal/worker/worker.go` | 211-220 | `FOR UPDATE SKIP LOCKED`，含 stuck-reclaim |
| dispatch switch | `internal/worker/worker.go` | 287-296 | `switch c.typ` (script/storyboard/asset) |
| runAssetAsync 入口 | `internal/worker/worker.go` | 928 | async path |
| short-circuit poll | `internal/worker/worker.go` | 946 | `submitted + ExternalJobID` |
| **precondition (PR #20)** | `internal/worker/worker.go` | 950-962 | 非 `generating` 状态 fail-fast |
| kindCap | `internal/worker/worker.go` | 988-996 | `MAX_CONCURRENT_VIDEO/AUDIO` |
| idemKey | `internal/worker/worker.go` | 1233-1236 | `sha256("studio-submit:"+todoID)[:16]` |
| submitTx | `internal/worker/worker.go` | 1001-1053 | 单事务（advisory-lock + ledger + reschedule） |
| pollAsync | `internal/worker/worker.go` | 1078 | 多次短 dispatch 状态查询 |
| reaper | `internal/worker/reaper.go` | — | TTL 终态化 stuck `submitted` |
| MediaGenerator.Generate | `internal/generate/{image,video,audio}/` | — | provider adapter |
| SSRF-safe fetch | `internal/fetch/fetch.go` | 116, 141-158 | `LimitReader`+512MB cap，DNS rebind 防御 |
| BlobStore.Put | `internal/blob/{localfs,s3,oss,github}/` | — | 字节落地唯一处 |
| assets.SetBlob | `internal/assets/store.go` | — | `generating`/`submitted` → `pending_acceptance` |
| HITL handlers | `internal/review/` + `internal/httpapi/handlers.go` | — | accept/reject/regenerate（admin 门禁） |
| run_done 写入 | `internal/events/store.go` | 50-88 | advisory-lock 防双发 |

---

## 状态机速查

### todos.status 转换

| from | to | 触发方 | SQL 守门 |
|---|---|---|---|
| (none) | `ready` / `blocked` | `CreateGraph` | deps 空/非空 |
| `ready`/`running-stuck` | `running` | claim | `FOR UPDATE SKIP LOCKED` |
| `running` | `done` | `MarkDone` | `WHERE status='running'` |
| `blocked` | `ready` | `MarkDone` cascade | deps 全 done |
| `running` | `failed` | `MarkFailed` | + 递归 cancel dependents |
| `running` | `ready (retry)` | `worker.fail` | `WHERE status='running'` + `next_run_at=now()+backoff*2^(attempts-1)` |
| 任意非终 | `canceled` | `project.Cancel` / `MarkFailed` cascade | CTE 递归 |

### assets.status 转换（M4 异步）

| from | to | 触发方 |
|---|---|---|
| (none) | `generating` | `GetOrCreateForTodo` |
| `generating` | `submitted` | `submitTx`（含 external_job_id） |
| `submitted` | `pending_acceptance` | `SetBlob`（poll-done 拉回后） |
| `submitted`/`generating` | `failed` | `SetAsyncFailed`（终态分支） |
| `submitted` 超 TTL | `failed` | orphan reaper |
| 任意非终 | `canceled` | `project.Cancel` 扫描 |
| `pending_acceptance` | `accepted` / `rejected` | HITL |

### project.status（聚合判定）

`planning`(无 todo) → `running`(任一 ready/running/blocked) → `failed`(failed>0) / `canceled`(canceled>0) / `review`(有 `pending_acceptance` 资产) / `completed`(全 done 且无待审)。

源码：`internal/project/status.go:22-44`。

---

## 三道并发上限（M4 §6 速查）

| 维度 | 控制谁 | 强度 | 检查位置 |
|---|---|---|---|
| **submit-admission** (`CountInFlightByKind`) | 外部 provider 在途 job | 软（TOCTOU，**跨 org 全局**，见 [issue #21](https://github.com/costa92/llm-agent-studio/issues/21)） | `runAssetAsync` (worker.go:955-962) |
| **`MAX_CONCURRENT_VIDEO/AUDIO`** (claim SQL 子查询) | 本地拉回 512MB 文件的内存堆叠 | 软（FOR UPDATE SKIP LOCKED 不锁 count） | `worker.claim` (worker.go:211-220) |
| **org 24h 配额** (`pg_advisory_xact_lock`) | billing-sensitive 总额 | **硬** | `submitTx` (worker.go:1008-1021) |

---

## 本 session 修复定位（★）

| ★ | PR | 位置 | 一句话 |
|---|---|---|---|
| ★1 | [#17](https://github.com/costa92/llm-agent-studio/pull/17) | `internal/httpapi/sse.go` | SSE 重连支持 `Last-Event-ID`，写 `id:<seq>` 行 |
| ★2 | [#20](https://github.com/costa92/llm-agent-studio/pull/20) | `internal/worker/worker.go:950-962` | runAssetAsync 非 `generating` 状态 fail-fast（防死循环） |
| ★3 | [#24](https://github.com/costa92/llm-agent-studio/pull/24) | `internal/httpapi/storagehandlers.go` | per-org localfs 拒收（防 UX 陷阱） |

---

## 横向参考

- 完整 subsystem 关系：[subsystem-map.md](./subsystem-map.md)
- 里程碑级深度剖析（M2）：[../m2-implementation-deep-dive.md](../m2-implementation-deep-dive.md)
- M4 延后项：[../m4-deferred.md](../m4-deferred.md)
- M5 延后项：[../m5-deferred.md](../m5-deferred.md)
