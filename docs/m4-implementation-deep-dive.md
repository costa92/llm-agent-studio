# AI Studio — M4 实现深潜（异步视频/音频生成引擎：submit→poll + heartbeat + 三道并发上限）

- 日期：2026-06-12
- 类型：只读实现分析（READ-ONLY）— 不改任何源码
- 来源：本 session 内 Explore agent 跨文件扫描的深扫报告（chat 沉淀落档）
- 范围：`internal/{worker,todos,assets,generate,limits,cost,fetch,obs}` + `cmd/studiod/main.go`
- 前置阅读：[m1-implementation-deep-dive.md](./m1-implementation-deep-dive.md)（M1 worker / claim / todo 基础已覆盖，本文专注 M4 新增/扩展面）

> 所有引用为 `path:line`，路径相对仓库根 `llm-agent-studio/`。本仓 Go 命令需 `GOWORK=off`。
> 文中凡涉及 M5+ 的特性（Canceler HTTP 真实实现 / 流式拉回 / 真实 SaaS 接线）都明确标注"延后 M5"并跳过。
> 横向参考：[architecture/run-flow.md](./architecture/run-flow.md) 单 run 路径、[architecture/subsystem-map.md](./architecture/subsystem-map.md) 全景图。

---

## 1. 异步路径完整调用链

`worker.claim` (`worker.go:191`) → dispatch switch (`worker.go:287-296`) → `runAsset` (`worker.go:510`) → 路由到 AsyncGenerator (`worker.go:575`) → `runAssetAsync` (`worker.go:928`)

**submit 分支**：`runAssetAsync` (928) → kind cap 检查 (955-962) → `ag.Submit` (967) → `submitTx` (979) → `tx.Commit` (1052) → `asset_submitted` SSE (982) → 返回 `errRescheduled` (984)

**poll 分支**：`pollAsync` (1078) → `ag.Poll` (1083) → 终态分支 (1104-1116) → PollDone 走 `completeAsync` (1113) → `puller.Get` 经 SSRF-safe fetcher (1177) → `bs.Put` (1193) → `SetBlob` (1214) → `UpdateGenerationByAssetTodo` (1224) → `asset_generated` SSE (1226) → 返回 `"asset:<id>"` (1228)

**reclaim 短路**：`runAssetAsync` 在 `asset.Status=="submitted" && ExternalJobID!=""` (946) 直接走 `pollAsync`，跳过 Submit。

## 2. submit→poll 状态机深挖

**schema 改动** (`internal/storage/storage.go:208-233` m4Migrations)：
- `todos.poll_attempts INT NOT NULL DEFAULT 0` (209)
- `assets.external_job_id TEXT NOT NULL DEFAULT ''` (210)
- `assets.submitted_at TIMESTAMPTZ` (211)
- `assets_extjob_idx` 部分索引 (`WHERE external_job_id <> ''`) (213)
- `assets_todo_uniq` 部分唯一索引 (`todo_id WHERE todo_id <> ''`) (216)
- `generations_asset_todo_uniq` 部分唯一索引 (`(asset_id, todo_id) WHERE asset_id <> '' AND todo_id <> ''`) (219-220)
- `pricing.micros_per_second BIGINT NOT NULL DEFAULT 0` (222) + 6 行种子价格 (225-232)

**多次短 dispatch 实现**：每次 submit/poll 都是一个完整 `process()` 循环（claim→dispatch→errRescheduled 退出），`reschedulePoll` (897-911) 把 todo 重排为 `ready`，clears `locked_by`/lease，bump poll_attempts=0/1，attempts 重置为 0（注释 I6:879-883）；下一次 dispatch 重新 claim。

**双列物理隔离**：`attempts`（失败重试预算）vs `poll_attempts`（轮询预算）是**两个独立列**（`worker.go:880-883, 1054-1050`）。`reschedulePoll` 把 `attempts=0` 显式重置为 0（`reschedulePoll` SET 句与 `submitTx` SET 句均写 `attempts=0, poll_attempts=...`）；失败路径的 `fail()` (754) 仅 bump `attempts` 不动 `poll_attempts`。

**退避计算**：`pollBackoff` (914-920) 公式 `PollBackoff << uint(pollAttempts)`（指数左移），`<=0` 或 `> MaxPollBackoff` 走 cap。**注意**：与 `fail` 的 `BaseBackoff * (1 << (attempts-1))`（`worker.go:780`）形态不同——poll 退避没有显式 cap guard 在 `MaxPollBackoff` 内的硬上界处理 overflow（左移极多次会变 0，自动走 cap 分支）。`submitTx` 写 `next_run_at = clock + PollBackoff`（`worker.go:1049`），`rescheduleOrCancel` 写 `next_run_at = clock + pollBackoff(pollAttempts)`（`worker.go:1143`）。

**终态失败分支**（`pollAsync`, `worker.go:1084-1116`）：
- `terminalFail` closure (1084-1094)：`SetAsyncFailed` + `MarkFailed` + `todo_failed` event + 触发 `allDone`/`AppendRunDone`；**统一返回 `errRescheduled`**（不是 `MarkDone`）以阻止 `process()` 重复收口（注释 IMPORTANT2:1068-1069）。
- PollFailed (1111)：直接 `terminalFail("provider reported failure: ...")`。
- PollPending 预算耗尽 (1107)：`terminalFail("poll budget exhausted (job still pending)")`。
- transient error (1096-1103) **不调** `SetAsyncFailed`，仅 burn `pollAttempts`，重排回 `ready`（让外部 job 继续跑）。
- 未知 status (1115) 走 terminalFail。

**`SetAsyncFailed` vs `MarkFailed`**（两个不同维度）：
- `assets.SetAsyncFailed` (`assets/store.go:357-364`)：把 `assets.status` 从 `generating` OR `submitted` 翻到 `failed`，**无 todo 联动**。
- `todos.MarkFailed` (`todos/store.go:124-139`)：把 `todos.status='failed'` 并递归 cancel dependents (`cancelDependents:204-218`)。
- 异步路径两件都做；同步 image 路径只走 `assets.SetBlob(...,'failed')`（`worker.go:626, 638, 642, 652`）+ todo 走 `fail()`/`MarkFailed`。

## 3. Lease 续约 heartbeat 深挖

**M4 究竟新增了什么**（注释证据 `worker.go:261-264` "M3 deferred gap closed"）：M1 注释 "M1 carry: no lease renewal"（`worker.go:244`），M3 deferred；M4 实装 `renewLease` (`worker.go:868-878`) + heartbeat goroutine (`worker.go:265-283`) + 配置项 `LeaseRenewInterval` (`Config:69`) + env 校验 (`config.go:160-163`)。

> **校对补注**：M1 深度剖析（[m1-implementation-deep-dive.md §3](./m1-implementation-deep-dive.md#3-worker--租约队列深挖)）也已观察到 worker.go:265-283 的 heartbeat 在 M1 代码层已存在；M4 的新增可能是从"未在路径上调用"到"实装入路径"的接缝完成，而非首次写代码。具体 git blame 视提交历史确认。

**`LEASE_RENEW_INTERVAL` 强制 < `WORKER_LEASE` 在哪里 enforce**：`internal/config/config.go:160-163`，启动时校验；不满足直接返回 error，不会进 runtime。

**heartbeat 与 dispatch ctx 关系**：`hbCtx, hbCancel := context.WithCancel(ctx)` (`worker.go:266`)，**派生自 process() 入口 ctx（不是 dctx）**——即不绑定 `CallTimeout`。`process` 的 `defer hbCancel(); hbDone.Wait()` (`worker.go:282`) 保证 dispatch 返回后 heartbeat 必退出。dispatch 走 `defer cancel()` (dctx cancel, `worker.go:258`) 也不影响 heartbeat（因为 hbCtx 派生自父 ctx）。

**双 guard `WHERE id=$1 AND locked_by=$3 AND status='running'`**（`worker.go:875`）：0 行 = no-op（heartbeat 静默吞），被 cancel/reclaim/reschedule 任意一者翻转后心跳自然无效；注释 I4 (`worker.go:864-867`) 明确说明这一闭合。

## 4. AsyncGenerator 接缝深挖

**接口位置 + 签名**（`internal/generate/generate.go:73-96`）：
- `Submit(ctx, GenRequest, idempotencyKey string) (SubmitResult, error)` —— SubmitResult 含 `ExternalJobID/Provider/Model/EstSeconds` (47-53)
- `Poll(ctx, jobID string) (PollResult, error)` —— PollResult 含 `Status` 枚举 `PollPending/PollDone/PollFailed` + `Result GenResult` (55-71)
- `AsyncGenerator` 接口嵌入 `MediaGenerator` (84-88)，但 `Generate()` 是 "submit then block-poll" 的便利形态，**worker 不调用**（注释 `generate.go:76-78`）
- `Canceler` 接口 (90-96) 独立可选；M4 未被任何 worker 代码消费

**image 适配器零改原因**（`internal/generate/image/image.go:25-45`）：`Adapter` 只实现 `MediaGenerator`（`Generate` 单一方法），**不实现** `AsyncGenerator`；worker 通过 type assertion（`worker.go:575`）分流。

**worker 分支判断逻辑**（`worker.go:575-578`）：
```go
if ag, ok := routed.(generate.AsyncGenerator); ok {
    return w.runAssetAsync(ctx, cctx, c, in.AssetID, kind, in.Duration, ..., ag)
}
```
**唯一依据**是 type assertion（不是 capability 字段）。image Adapter 不实现 → 落到 M3 sync 路径（`worker.go:580-673`）。

**tracedAsyncGenerator 维持接缝**（`obs.go:55-64`）：`WrapGenerator` 检测 inner 是否是 AsyncGenerator，是则返回 `tracedAsyncGenerator`（嵌入 `tracedGenerator`）—— 注释 B1 显式警告"the worker's routed.(AsyncGenerator) assertion is stripped by the wrapper and the async engine is never reached"（`obs.go:51-54`），即 otel wrap 必须维持接缝否则 video/audio 永远走不到 async 路径。

**同 MediaGenerator 实例被 sync/async 两条路径同时用** 的问题：代码里**不会出现**——type assertion 是 `if ok`，async 走 runAssetAsync，sync 走 runAsset 后续的 `RunWith(routed)`。同 instance 在同一 todo 生命周期内只走一条路径。

## 5. 幂等性 / 崩溃恢复深挖

**idemKey 算法**（`worker.go:1233-1236`）：`idempotencyKey(todoID)` = `hex.EncodeToString(sha256("studio-submit:" + todoID)[:16])`（SHA-256 截前 16 字节）。FakeAsync 收到同 key → `jobIDFor(key)` = `hex.EncodeToString(sha256("fakeasync:" + key)[:8])` 拼 `"fakejob_"` 前缀（`fake_async.go:41-44`），同 key 必同 jobID（`fake_async_test.go:37-58` `TestFakeAsyncIdempotentSubmit` 验证）。

**idemKey 传递路径**：worker 算（`worker.go:966`）→ 传 `tracedAsyncGenerator.Submit`（`obs.go:102` "B1: forward the idem key"）→ 真适配器承诺转 provider client-token（注释 `video.go:53-54`），骨架返回 notConfigured。

**`assets_todo_uniq` SQL**（`storage.go:216`）：`CREATE UNIQUE INDEX IF NOT EXISTS assets_todo_uniq ON assets (todo_id) WHERE todo_id <> ''`；partial 因为 regenerate 路径写 `todo_id=''`（`assets/store.go:314` 注释 "regenerate carries todo_id=''"），普通 image 同步路径 `createAsset` 仍会标 todo_id（`worker.go:686`）；partial 避免 "many regenerate rows with todo_id=''" 撞唯一。

**`GetOrCreateForTodo` 实现**（`assets/store.go:315-337`）：先 SELECT（无锁），无 → `Create`（带 UNIQUE INDEX），错 → re-SELECT 胜者；不靠事务。

**submit 单事务结构**（`worker.go:1001-1053 submitTx`）：
1. `pg_advisory_xact_lock(hashtext($orgID))` (1009) —— 同 org 串行
2. count generations by org since 24h (1013-1017)；超 quota → 报错
3. `UPDATE assets SET status='submitted', external_job_id, provider, model, submitted_at=now() WHERE id AND status='generating'` (1027-1029)
4. `INSERT INTO generations ... ON CONFLICT (asset_id, todo_id) WHERE asset_id<>'' AND todo_id<>'' DO UPDATE SET id=generations.id` (1038-1042) —— 注释 1033-1037 显式说"predicate 必须 COPY-PASTE VERBATIM from T3 migration's index，否则 ON CONFLICT fails"
5. `UPDATE todos SET status='ready', next_run_at, attempts=0, poll_attempts=0, locked_by='', locked_until=NULL` (1045-1050)

**reclaim 路径**（`worker.go:946-948`）：`runAssetAsync` 入口先读 asset，`Status=='submitted' && ExternalJobID !=''` → 直接走 `pollAsync` 跳过 Submit + 跳过 submitTx。`pollAsync` 内部 `SELECT poll_attempts FROM todos WHERE id=$1` (1080) 读当前预算后用 guarded UPDATE 原子 bump（I4 注释 1071-1077 + reschedulePoll:898-911），locked_by guard 0 行 → `rescheduleOrCancel` 报 `errLostLease` (1138-1163) → 区分 cancel (1157) vs reclaim (1155) 仅打日志。

## 6. 三道并发上限对比

| 维度 | submit-admission 在途上限 | `MAX_CONCURRENT_VIDEO/AUDIO` 软上限 | org 配额 硬上限 |
|---|---|---|---|
| 检查位置 | `runAssetAsync` (`worker.go:955-962`) 仅 submit 分支；不在 claim 里 | `worker.claim` (`worker.go:211-220`) 进 SQL 子查询 | `submitTx` (`worker.go:1008-1021`) |
| SQL 形态 | `SELECT count(*) FROM assets WHERE status='submitted' AND type=$1` (`assets/store.go:372`) | `SELECT count(*) FROM todos WHERE type='asset' AND status='running' AND locked_until > now()` (`worker.go:216-217`) | `SELECT count(*) FROM generations g JOIN projects p ... WHERE p.org_id=$1 AND g.created_at >= $2` (`worker.go:1013-1015`) |
| 串行化 | **无**（count-then-act TOCTOU 窗口；与 M3 同性质） | **无**（FOR UPDATE SKIP LOCKED 只锁被 claim 的行，不锁计数） | **`pg_advisory_xact_lock(hashtext($orgID))`** 同 org 串行 (`worker.go:1009`) |
| 竞争资源 | 外部 provider 在途 job 数 | 本地 running asset todo 数（拉回瞬间 OOM 天花板） | 24h 滚动 generations 计数（billing-sensitive） |
| 防御目标 | 滥用面收窄（"我方控制不了 provider 排队长度"） | 拉回 512MB 视频/音频在内存中堆叠 | org 24h 配额硬限 |
| 0 行为 | 不 burn poll budget，`reschedulePoll(bumpPoll=false)` (`worker.go:957`) | claim 不命中该 todo | 同 tx 内报错退出，submitTx 整体回滚 |
| 软/硬 | 软/近似（TOCTOU） | 软/近似（FOR UPDATE SKIP LOCKED 不锁 count） | 硬（advisory-lock 串行） |

**为什么 submit 分支检查、poll 不受限**（`worker.go:950-962` 注释 B2 + I3）：poll 是已提交 job 的状态查询（IO 廉价），限 poll 会让"满在途"的 provider 永远轮询不收敛；submit 是发新 job（IO 昂贵 + 触发 provider 排队），只限 submit。

**`MAX_CONCURRENT_*` 是软上限**（`worker.go:203-210` 注释 "SOFT/approximate cap"）：FOR UPDATE SKIP LOCKED 只锁所 claim 的行，不锁 count；并行 claim 在 READ COMMITTED 下各自看到旧 count，最多瞬时越过上限 Workers-1 次。

## 7. FakeAsync generator 深挖

**位置**：`internal/generate/fake_async.go`（94 行）

**确定性来源**：
- `jobIDFor` = `sha256("fakeasync:" + idemKey)[:8]` (`fake_async.go:41-44`) → 纯哈希，无 sleep、无随机
- `pollsToDone` 计数决定 Pending/Done（`fake_async.go:60-66`），`f.jobs[jobID]++` 单调增 → 完全确定
- `Submit` 把 `DurationSeconds` 原值回 `EstSeconds` (`fake_async.go:53`)

**零网络**：FakeAsync 自身不发起任何 HTTP；result 是构造时注入的 `GenResult`（`fake_async.go:30-35`），常以 `URL: "https://example/v.mp4"` 测试（`fake_async_test.go:10`），但**不**在 Submit/Poll 阶段拉取——拉取由 worker `completeAsync` → `puller.Get` 在 SSRF-safe fetcher 里完成。`fetcher` 注入用 `NewLoopbackForTest` (`fetch.go:84-86`) 走 httptest。

**测试场景覆盖**（`fake_async_test.go`）：
- `TestFakeAsyncSubmitPollLifecycle` (8-35)：pending→done 状态序列
- `TestFakeAsyncIdempotentSubmit` (37-58)：同 key 必同 jobID
- `TestFakeAsyncIsAsyncGenerator` (60-65)：编译期接口断言

**PollFailed / transient error 模拟**：FakeAsync 不内置——`asset_test.go` 通过设 `PollOmitsProviderModel`（F3 仿真"真 provider 不回 provider/model"）+ 调两遍 `completeAsync` 仿真竞态（`asset_test.go:1014-1058 "TestCompleteAsyncWinnerLoserArbiter"`）+ 写 `status='submitted'` 行并查 ttl 仿真孤儿 (`asset_test.go:888`) 覆盖各路径。

## 8. Key-gated 真实适配器骨架

**注册策略**（`cmd/studiod/main.go:547-594 registerVideoGenerators / registerAudioGenerators`）：
- 遍历 `models.Catalog()` 过滤 `e.Kind=="video"|"audio"`
- 按 provider switch，`cfg.RunwayAPIKey/KlingAPIKey/TTSAPIKey` 为空 → `continue`（**不注册** → `Registry.Resolve` 落到 default → 默认 fake video）
- Veo 复用 `cfg.GoogleAPIKey`（`main.go:564-568` "key reuses GoogleAPIKey"；README "Veo 复用 GOOGLE_API_KEY 的接缝"）
- `obs.WrapGenerator` 包一层 (`main.go:572, 588`) → 触发 tracedAsyncGenerator 分支

**4 个适配器完整性表**（`video.go:20-64`, `audio.go:22-57`）：

| 适配器 | Kind | Submit | Poll | Cancel | Generate |
|---|---|---|---|---|---|
| Runway (`video.go:27-29`) | "video" | skeleton: returns notConfigured (54-56) | skeleton: notConfigured (59-61) | no-op `return nil` (64) | notConfigured (48-50) |
| Kling (`video.go:32-34`) | "video" | 同上 | 同上 | no-op | notConfigured |
| Veo (`video.go:37-39`) | "video" | 同上 | 同上 | no-op | notConfigured |
| OpenAI TTS (`audio.go:29-31`) | "audio" | 同上 (47-49) | 同上 (52-54) | no-op (57) | notConfigured (41-43) |

骨架**所有**方法都是 receiver stub：`Submit`/`Poll`/`Generate` 返回 `notConfigured()` error；`Cancel` 直接 `return nil`。

**`// TODO(m5)` 分布**（grep 6 处全在 video/audio 包）：
- `video.go:53, 58, 63`（Submit 真实 HTTP, Poll 真实 HTTP, Cancel 真实 HTTP）
- `audio.go:46, 51, 56`（同上）

**Veo 复用 GOOGLE_API_KEY 接缝**：`registerVideoGenerators` (`main.go:564-568`) `case "google": if cfg.GoogleAPIKey == "" { continue }`，共用 chat 路由同把 key（Veo 注释 `video.go:36` "key reuses GoogleAPIKey at the call site"）。

## 9. 资产类型 / 计费 / 账本深挖

**assets 新增字段**（`storage.go:210-211`）：`external_job_id` + `submitted_at`。其他 M3 已有的 video/audio 字段：`type`（"video"/"audio"，catalog 决定）、`provider`、`model`、`status`、`blob_key`/`url`。

**`pricing.micros_per_second`**：schema 列 (`storage.go:222`) + Price struct 字段 (`cost/store.go:105`) + `PriceFor` 读取 (`cost/store.go:113`) + `ComputeCostMicros` 加项 (`cost/store.go:128`)。`image` 用 `MicrosPerImage` (per-image) + `MicrosPer1kTokens`；`video`/`audio` 用 `MicrosPerSecond * seconds`（按帧时长/音频时长，Q3 复用同列）。`RecordPriced` 路径只在 `cost_micros=0` 时调 `PriceFor` (`cost/store.go:136-141`)——M3 同步路径用 `RecordPriced`，M4 异步预登记用 `UpsertSubmittedGeneration`（不走 `RecordPriced` 避免 `cost_micros=0` 时的价格补算）。

**ledger upsert（submit 预登记）**（`cost/store.go:267-292 UpsertSubmittedGeneration` + `worker.go:1038-1042 submitTx` 内联）：
- `INSERT ... ON CONFLICT (asset_id, todo_id) WHERE asset_id <> '' AND todo_id <> '' DO UPDATE SET id = generations.id`
- `RETURNING id` —— 同 (asset_id, todo_id) 必返已有行 id（注释 268-273 "submit-insert / poll-update hit the same row"）
- 在途行立即可被 `CountByOrgSince` 计入（`worker.go:1013-1015` SQL 读 `g.created_at` 不限 status），避免 quota 击穿

**`UpdateGenerationByAssetTodo` 幂等回填**（`cost/store.go:297-304`）：`UPDATE generations SET video_seconds=$3, cost_micros=$4 WHERE asset_id=$1 AND todo_id=$2`——不带其他条件，二次执行同值幂等；按 (asset_id, todo_id) 定位（不传 in-memory id，注释 295-296 "no in-memory id passing"）。`worker.go:1222-1225` 在 completeAsync 里调（紧跟 `SetBlob` won=true 之后）。

**regenerate video 的 duration 留 0**（README "M4 已知限制"）：`runAssetAsync` 内 `regenAssetID != ""` 路径 (`worker.go:932-939`) 用 `Get` 取已有 row，`duration` 来自 caller（main input），没看到精化。

## 10. SSRF-safe 拉回 + 孤儿 reaper

**防御三件套**（`internal/fetch/fetch.go`）：
- **Scheme 限定**：`validateScheme` (167-174) 限 `http`/`https`；`CheckRedirect` (72-77) 每跳重验（限 5 跳）
- **DNS 重检 + 拨号防 TOCTOU**：`resolveAndValidate` (141-158) 解析所有候选 IP，**只对未 blocked 的 IP 返回**；`DialContext` (52-62) 直接 dial 那个 IP（`net.JoinHostPort(ip.String(), port)`），**绕过 hostname 二次解析** → DNS rebinding 无窗口
- **IP 黑名单**：`isBlockedIP` (182-192) 含 loopback / private / link-local（169.254.169.254 拿 metadata）/ multicast / unspecified / interface-local multicast / RFC 6598 CGNAT（100.64.0.0/10）
- **content-type allowlist** (126-137) + **MaxBytes+1 防 silent truncate** (116-122)

**512MB 全量入内存**：`io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))` (`fetch.go:116`)；这是 `LimitReader` + 读一字节超 cap 拒收；不是 `MaxBytesReader`（http body 类型）。`cmd/studiod/main.go:260-264` 装 worker.VideoFetcher：`MaxBytes: cfg.VideoFetchMaxBytes`（env `VIDEO_FETCH_MAX_BYTES`，默认 536870912）。

**孤儿 reaper**（`internal/worker/reaper.go`）：
- 周期：`cmd/studiod/main.go:301-302 cfg.MaxPollBackoff`（默认 30s）扫一次
- TTL：`2 * MaxPollAttempts * MaxPollBackoff`（`main.go:301`）—— 仅在"远超合法 poll 窗口"时才 reaper
- 终态化 SQL（`assets/store.go:381-388`）：`UPDATE assets SET status='failed' WHERE status='submitted' AND submitted_at IS NOT NULL AND submitted_at < $1`
- 启动位置：`main.go:298-303` `go func() { ... worker.RunOrphanReaper(workerCtx, assetStore, ...) }()` 与 worker pool 同 ctx（`workerCtx`），`ctx.Done()` 自动退出

**`SetAsyncFailed` 与 reaper 关系**：`SetAsyncFailed`（status IN ('generating','submitted')）由 pollAsync 终态分支 + completeAsync fetch 失败路径调（`worker.go:1085, 1179, 1190, 1194`）；reaper 仅 reaps `submitted`（不含 generating）—— 重复防御无冲突（仅顺序无关，reaper 跑的 SQL 也是 UPDATE submitted→failed）。

## 11. M4 留给 M5+ 的接缝

- **`Canceler` 接口**（`generate.go:90-96`）：已定义；骨架 `video.go:64` / `audio.go:57` 实现 `return nil` no-op；**整个仓库 M4 范围内无任何调用方**（grep "Canceler" 只在 `generate.go` 定义、video/audio 的方法实现、README + m4-deferred.md 文档）。
- **`// TODO(m5)` HTTP 接线点**：6 处全在 `video.go:53,58,63` / `audio.go:46,51,56`。
- **流式拉回**：`fetch.go` 仍 `io.ReadAll`（不流式）；m4-deferred.md 列入 M5。
- **M3 catalog now carries video/audio entries** (`main.go:506` 注释 "M3 catalog now carries video/audio entries (M4); skip them here") — catalog 数据已含 video/audio，但 registerImageGenerators 不再处理。
- **`image` 同步 image 适配器新增 `DurationSeconds`/`Voice` 字段** (`generate.go:20-22`) — `Adapter.Generate` (`image.go:48`) 不读这两个字段，注释 "image 适配器忽略"。
- **`recordPriced` 在 M4 异步路径不被使用**：异步改走 `UpsertSubmittedGeneration`（`cost/store.go:267`）—— `RecordPriced` 是 M3 同步 image 的 costing chokepoint（`cost/store.go:135`）。
- **同步短 TTS 路径**（README "TTS 暂以 AsyncGenerator 交付 I5 刻意分歧"）：`audio.go:41-43` 注释 "synchronous short-TTS is deferred to M5"。

## 12. 独立观察 / 疑问

1. **`worker.go:1154` 的 reclaim/cancel 区分仅靠 status 字符串而非 status='canceled'**：`if status == "running" && lockedBy != w.cfg.WorkerID` → 走 reclaim 分支；`else` → cancel 分支。若 cancel 之后又被 stuck-reclaim 抢跑，状态可能进入"running with new locked_by"——`errLostLease` 两种情况都返回，行为相同（仅日志不同），但**reclaim 一支打印 "stale worker stops" 不区分"健康 vs 不健康"**；后续若加更细粒度 health check 可能需要再读。

2. **`CountInFlightByKind` 的 0 默认值**（`assets/store.go:370-376`）：`SELECT count(*) FROM assets WHERE status='submitted' AND type=$1` 没有任何 AND 限定 org —— 跨 org 全表扫；与 `worker.kindCap` 配合时**不区分 org**，"我方 in-flight 30 video" 可能被另一 org 的 30 video 触发 hold。`worker.kindCap` 注释（`worker.go:987-996`）未说明这是 per-org 还是 global；从 SQL 看是 global。
   > **Followup**：[Issue #21](https://github.com/costa92/llm-agent-studio/issues/21) 已记录，请 owner 在三方案中决策（文档化 / 改 per-org / 双层）。

3. **`perSecondMicros` 失败回 0**（`worker.go:1056-1064`）：pricing 查不到 → `cost = estSeconds * 0 = 0` 写入 ledger；`UpdateGenerationByAssetTodo` 后仍可能回填 0 实际值（再次查表同样 fail）。M3 已知 "unpriced = 0" 决策，但 M4 把这个回 0 的估算当 pre-registration 推给 `CountByOrgSince` 计入 quota —— 配 out-of-catalog 适配器会进 quota 但不入账。**建议追踪**：未在 `CountByOrgSince` 与 `cost_micros=0` 之间设防御。

4. **`runAssetAsync` 的 `regenAssetID` 路径**（`worker.go:932-939`）：regenerate 走 `Get(regenAssetID)`，**不调** `GetOrCreateForTodo`；regenerate 走 HITL 已预创建的 v2 asset，asset.status 已是 `generating`（review.Regenerate 写的，路径在 httpapi 不在 worker）；但若 v2 row 因前次失败已 `failed`，worker 进入时 `asset.Status != "submitted"` 也不在 `generating` 短路 → 走 submit 路径 → `submitTx` 的 `UPDATE assets SET status='submitted' ... WHERE id AND status='generating'` (`worker.go:1027-1029`) **0 行不报错**（PostgreSQL UPDATE 无 error 但 RowsAffected=0）—— submitTx 仍 `Commit` 成功，但资产状态卡在 failed 永远不动。**疑问**：该 0 行路径无 `won` 检测，提交后 todo 仍 ready 下次 re-poll 仍 0 行……（**这是一个潜在死循环**）。
   > **Followup**：[PR #20](https://github.com/costa92/llm-agent-studio/pull/20) 已修——`runAssetAsync` 在 short-circuit 之后、kindCap 之前加 precondition：`asset.Status != 'generating'` 直接 fail-fast 返 error，由 `worker.fail()` 按 MaxAttempts 终态化。

5. **`tracedAsyncGenerator.inner` 字段冗余**（`obs.go:96`）：`tracedGenerator` 已 embed `inner MediaGenerator`（`obs.go:66-69`），tracedAsyncGenerator 又单写 `inner generate.AsyncGenerator` —— 两个 inner 字段语义重叠；`Submit`/`Poll` 用 `t.inner.Submit`（`obs.go:102, 119`），`Generate` 走 embed 的 `tracedGenerator.Generate`（`obs.go:73`）→ `t.inner.Generate`（`obs.go:76`）走 `MediaGenerator.inner`。两个 `inner` 类型不同（`MediaGenerator` vs `AsyncGenerator`），是必须的（类型约束），但语义上"inner 同一个适配器的两个视图"。

6. **M3 carry 的 `RunWith` vs M4 异步**：`runAsset` 在 sync 路径用 `w.cfg.Asset.RunWith(ctx, routed, ...)`（`worker.go:620`），async 路径**直接** `ag.Submit/...`（`worker.go:967, 1083`）—— AssetAgent 不参与 async 路径的 prompt 构造，但 prompt 还是经 `w.cfg.Asset.BuildPrompt(...)`（`worker.go:965`）。

7. **`upsert` 的 DO UPDATE SET id=generations.id**（`cost/store.go:284`, `worker.go:1041`）：SQL 写"不改任何字段"——这是一个空操作的反直觉写法，作用是让 ON CONFLICT 走 DO UPDATE 分支使 RETURNING 返回冲突行的 id（DO NOTHING 不会 RETURNING）。`UpsertSubmittedGeneration` 与 `submitTx` 内联 INSERT 都用此模式，**两处都应保持 byte-identical ON CONFLICT predicate**（`worker.go:1033-1037` 注释警告）。

8. **assets_todo_uniq 测试断言 duplicate must violate**（`storage_test.go:188`）：`storage_test.go:140-160` 校验 M4 schema 列和 partial index 实际存在——是 M4 落地的端到端可观测证据。

9. **TODO 数量级**：grep "TODO(m5)" 6 处（全部在 video/audio 骨架），无 M6+ 留痕。

---

## 横向参考

- 单 run 调用链：[architecture/run-flow.md](./architecture/run-flow.md)
- 全景子系统图：[architecture/subsystem-map.md](./architecture/subsystem-map.md)
- M1 实现剖析：[m1-implementation-deep-dive.md](./m1-implementation-deep-dive.md)
- M2 实现剖析：[m2-implementation-deep-dive.md](./m2-implementation-deep-dive.md)
- M8 实现剖析：[m8-implementation-deep-dive.md](./m8-implementation-deep-dive.md)
- M4 延后项与已知窗口：[m4-deferred.md](./m4-deferred.md)
