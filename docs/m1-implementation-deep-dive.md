# AI Studio — M1 实现深潜（文本管线骨架：planner + worker + SSE + RBAC + otel）

- 日期：2026-06-12
- 类型：只读实现分析（READ-ONLY）— 不改任何源码
- 来源：本 session 内 Explore agent 跨文件扫描的深扫报告（chat 沉淀落档）
- 上游设计：M1 是 studio 整个生态的奠基里程碑，所有后续里程碑（M2-M8）都基于 M1 的 todo/worker/SSE 接缝扩展
- 范围：`internal/{planner,worker,todos,agents,events,project,httpapi,studiosvc,obs}` + `cmd/studiod/main.go`

> 所有引用为 `path:line`，路径相对仓库根 `llm-agent-studio/`。本仓 Go 命令需 `GOWORK=off`。
> 文中凡涉及 M2+ 的列、文件、特性（image generator / async submit / pricing / BYOK / regenerate 等）都明确标注"M2+ 扩展"并跳过，专注 M1 原型本身。
> 横向参考：[../architecture/run-flow.md](./architecture/run-flow.md) 单 run 路径、[../architecture/subsystem-map.md](./architecture/subsystem-map.md) 全景图。

---

## 1. 整体架构图（文字版数据流）

HTTP `POST /api/projects/{id}/run` → `httpapi.runHandler`(`internal/httpapi/handlers.go:261`) 调 `PlannerPort.Plan(With)` → `planner.planner.go:68` 调 `coreagents.SimpleAgent.Generate` 一次得 JSON 图，校验后 `todos.CreateGraph`(`internal/todos/store.go:41`) 把图谱一次性写入 `todos` 表，根节点 `status='ready'`、从节点 `status='blocked'`。handler 同步 emit `planner_started` + 每根节点一条 `todo_ready` 事件（`handlers.go:283-305`），把 project 状态切到 `running`。

Worker pool 在 `cmd/studiod/main.go:269` 起 `cfg.Workers` 个 goroutine（每实例一 goroutine，单租户串行）调 `Worker.Run` (`internal/worker/worker.go:168`) 循环 `RunOnce`：先 `claim()`（`SELECT...FOR UPDATE SKIP LOCKED`，`worker.go:211-220`）拿一条 ready 或 stuck 租约，按 `c.typ` 分派到 `runScript` / `runStoryboard` / `runAsset`（dispatch table 实为 switch，`worker.go:288-296`）。

`runScript` 调 `agents.ScriptAgent.RunWith` → `llm.ChatModel.Generate` → 写 `scripts` 表；`runStoryboard` 通过 `todos.depends_on` 反查父 script、写 `shots` + 用 `todos.AddDynamic` 同一事务里 fan-out 出每镜一个 `asset` todo。完成走 `MarkDone` (`todos/store.go:83`) 原子地把 `status='done'` + 升级从节点 → `ready`，emit `todo_finished`，并 `emitNewlyReady` 把"被动升级"的 todo 推 `todo_ready` 事件（`worker.go:712-736`）。

`runAsset` 调 `MediaGenerator.Generate` → 写 `assets` 表 + `blob.Put` + 写 `generations` 账本。`allDone` 判定全部终态后由 `events.AppendRunDone` 写入唯一 `run_done`（`events/store.go:59`，配 `pg_advisory_xact_lock` 防并发双发）。

`GET /api/projects/{id}/events/stream` → `httpapi.streamEventsHandler`（`internal/httpapi/sse.go:39`）先 `events.List(projectID, after, 200)` 一次回放历史，再 500ms 轮询切流；事件名白名单在 `sse.go:22`。

## 2. Planner 深挖

**"动态规划"实际是一次性全图生成**，不是 step-by-step。`planner.go:74-101` 调一次 `agent.Run(ctx, prompt)`，prompt 就是 brief 四元组拼字符串（`planner.go:79`），LLM 必须输出 `{"nodes":[...]}`（system prompt 见 `planner.go:49-51`），校验通过后**整张图一次写库**（`todos.CreateGraph` 在 `planner.go:130`）。后续 todo 之间的执行顺序完全由 `MarkDone` 升级从节点控制（`todos/store.go:104-112`），Planner 不会回头续编。

**M1 范围**：`graph.go:17` 的 `m1Types`（命名歧义，注释写"M2 whitelist"）仅含 `script`/`storyboard`/`asset`，`DefaultPipeline()` 始终是 `script→storyboard` 两节点（`graph.go:125-130`）。

**校验三件事**（`graph.go:47-82`）：非空、id 唯一、type 在白名单内、依赖引用的 id 存在、至少一个 script 节点、DFS 检测环（`checkAcyclic`，`graph.go:85-120`）。**没有任何业务规则校验**（"图结构"即拓扑合法性是唯一业务校验）。

**回落策略**（`planner.go:88-101`）：失败三种 case 都把 `graph` 留作 `DefaultPipeline()`，写入 `plans.valid=false, fallback_used=true`。不是"rule-based stub"也不是"固定模板"——直接走兜底的两节点图 `script→storyboard`，LLM 文本照存为 `raw_plan_json` 用于审计。校验失败的 rawText 用 `json.Marshal` 包了一层（`planner.go:104`），但实际是 marshal 一个 string 嵌套成 `"agent error: %v"` 字符串。

**prompt 构造**：inline 字面量常量，**没有 template 系统**。`plannerSystemPrompt` 是 `planner.go:49` 的 `const` raw string，`planner.go:79` 的 user prompt 是 `fmt.Sprintf` 拼四元组。Asset/Script/Storyboard 三个 system prompt 同样都是 `const`（`agents/script.go:42`、`agents/storyboard.go:38`）。**M1 没有任何 prompt 模板引擎**，`internal/prompt` 包是"图片风格后缀库"（`prompt.go:17`），跟 chat prompt 无关。

**与 ChatModel 解耦**：`Planner.model llm.ChatModel`（`planner.go:44`）绑 env-default。`PlanWith(ctx, projectID, model, brief)`（`planner.go:74`）是 BYOK 入口，handler 端用 `modelrouter.Router.ChatModelFor(ctx, orgID)` 解出 org 配置的 chat model 再传入（`handlers.go:289-294` + `modelrouter/router.go:74`）。**"capabilities 是 per-(provider × model)"** 在 `modelrouter.MediaGeneratorFor` (`router.go:99`) 里以 `kind` 维度实现：text → `BuildChat`，image/video/audio → `BuildMedia`，且 `Models` store 按 kind 维护多组配置。

## 3. Worker / 租约队列深挖

**单 worker = 单 goroutine 串行**：每个 `worker.Worker` 实例**只跑一个 goroutine** 在 `Run` 循环里（`worker.go:168-186`），pool 大小由 `cmd/studiod/main.go:269` 的 `cfg.Workers` 控制（启动 N 个 `Worker`）。`RunOnce` → `claim` → `process` 是严格串行，一个 worker 不并行处理多个 todo。

**claim SQL 是 `SELECT ... FOR UPDATE SKIP LOCKED`**（`worker.go:211-220`），加 `ORDER BY next_run_at ASC` 防止饥饿。**还含一个 stuck-reclaim**：`status='running' AND locked_until < now()` 也被纳入候选（同一 SQL 的 OR 第二支），另一个 worker 的卡死 lease 会被夺走。**没有 advisory lock**（advisory 只用在 `submitTx` 与 `AppendRunDone` 这两个需要"跨 tx 序列化"的场景）。

**租约字段**：`todos.locked_by TEXT, locked_until TIMESTAMPTZ, next_run_at TIMESTAMPTZ, attempts INT`（schema 在 `storage.go:79-82`）。`Lease` 默认 **120s**（`worker.go:115-117`），`MaxAttempts` 默认 **3**（`worker.go:113`），`BaseBackoff` 默认 **2s**（`worker.go:73`，但 `cmd/studiod/main.go:287` 传 `cfg.WorkerBackoff`，实际由配置覆盖）。

**Lease renewal（README 标 M4 闭合的 gap）**：`worker.go:265-283` **M1 已有 heartbeat**：dispatch 期间启 ticker 每 `LeaseRenewInterval` 调 `renewLease`（`worker.go:868-878`）做 `UPDATE ... WHERE id=$1 AND locked_by=$3 AND status='running'`，双 guard 让"自 reschedule 之后"心跳自然 0-row 变 no-op。`config.LeaseRenewInterval` 为 0 关闭（`worker.go:265`），但 `cmd/studiod/main.go:284` 总是传 `cfg.LeaseRenewInterval`（来自 `internal/config`），所以**生产里默认开**。spec §5.2b I4 的双 guard 模式是真实的。

**重试**：失败（不是 canceled）走 `worker.fail`（`worker.go:754-787`）：`attempts >= MaxAttempts` 时调 `todos.MarkFailed`（**同时用 CTE 递归把后继全 cancel**，`todos/store.go:124-139, 204-218`），否则 `next_run_at = now() + BaseBackoff * 2^(attempts-1)`、`status='ready'`、`attempts` 留在 SQL 里没回退（注意：`UPDATE ... attempts` 没在 `worker.go:783` 里出现，**不重置 attempts**——这是指数退避，但 attempts 单调累加会让第 N 次退避 = `2^(N-1)*BaseBackoff`，无封顶逻辑，与 M4 `pollBackoff` 的 `>> uint(attempts) + cap` 模式不同）。

**dispatch**：纯 `switch c.typ`（`worker.go:287-296`），不是 map；新增 type 必须改 switch。

**race 防御**：`process` 里的几个 sentinel error 互不相同（`errRescheduled`/`errLostLease`），各有不同副作用（`worker.go:298-315`）；`discardCanceledAsset`（`worker.go:841-856`）用三轮 TransitionStatus 幂等收回资产（`pending_acceptance`/`submitted`/`generating`）。

## 4. Todo 状态机深挖

**状态枚举**（`status TEXT`，无 CHECK 约束）：`pending`（schema 默认，DB 行从未见过此值——`CreateGraph` 直接写 `ready`/`blocked`）/ `ready` / `blocked` / `running` / `done` / `failed` / `canceled`（`todos/store.go:60-67` 创建态用前 4 个，转换经 `MarkDone`/`MarkFailed`/`Cancel`/`fail` 写入后 3 个）。

**核心转换 SQL**：
- 创建 + 升级：`todos.CreateGraph` (`store.go:41-75`)，根→`ready`，从→`blocked`
- claim：`worker.go:228-232` `UPDATE ... SET status='running', locked_by=$2, locked_until=now() + interval, attempts=$4`
- done + 升级原子：`todos.MarkDone` (`store.go:83-114`) `UPDATE ... WHERE status='running'` 守门，0 行 → commit 空 tx 返回 `(false, nil)`；>0 行 → 同 tx 调一个 `UPDATE ... FROM todos t WHERE t.status='blocked' AND NOT EXISTS(SELECT 1 FROM todos d WHERE d.id = ANY(t.depends_on) AND d.status<>'done')` 升从节点
- 失败 + 递归 cancel：`MarkFailed` (`store.go:124-139`) + `cancelDependents` (`store.go:204-218`) 递归 CTE
- 重调度（可重试）：`worker.go:782-786` `status='ready', next_run_at=..., locked_*` 清空
- 项目级 cancel：`project.Cancel` (`project/store.go:179-190`) 把非终态 todo 全置 `canceled`，并 sweep `generating`/`submitted` 资产
- 状态机对外查询：`project.RefreshStatus` (`project/store.go:141-166`) 用 `count(*) FILTER (WHERE status=...)` 一次拿所有计数

**依赖关系**：依赖是 **DAG**，边存为 `todos.depends_on TEXT[]`（schema `storage.go:75`）。`CreateGraph` 接受 `LocalID`→生成 `todoID`→写回 `depends_on`（`store.go:51-69`）。运行时反查父边是 `worker.go:395-399` 的 `JOIN todos t ON t.id = ANY(sb.depends_on)`。

| from | to | 触发方 | 守门 | SQL 位置 |
|---|---|---|---|---|
| (none) | ready | CreateGraph | deps=[] | `store.go:60-67` |
| (none) | blocked | CreateGraph | deps≠[] | `store.go:62` |
| ready/running-stuck | running | claim | FOR UPDATE SKIP LOCKED | `worker.go:211-220` |
| running | done | MarkDone | `WHERE status='running'` | `store.go:91-92` |
| blocked | ready | MarkDone | deps 全 done | `store.go:104-112` |
| running | failed | MarkFailed | — | `store.go:131-132` |
| 任意非终 | canceled | MarkFailed cascade / Cancel sweep | CTE 递归 | `store.go:204-218`, `project/store.go:180-184` |
| running | ready (retry) | worker.fail | `WHERE status='running'` | `worker.go:782-786` |

**项目级状态机**（`project/status.go:22-44`）：`planning`(无 todo) → `running`(任一 ready/running/blocked) → `failed`(failed>0) / `canceled`(canceled>0) / `review`(有 `pending_acceptance` 资产) / `completed`(全部 done 且无待审)。

## 5. Agents 深挖

**Script agent** (`internal/agents/script.go`)：单 LLM 调用包装 `coreagents.NewSimpleAgent`（`script.go:55`），system prompt 是 `scriptSystemPrompt` const（`script.go:42-44`），要求严格 JSON `{title,logline,scenes[3字段]}`，user prompt 是 `fmt.Sprintf("Brief: %s\nContent type: %s\nTarget platform: %s\nStyle: %s",...)`（`script.go:58-60`）。输出解析靠 `extractJSONObject`（`agents/jsonparse.go`）去 fence + 找首个大括号 + 平衡匹配（容错 R1 策略），再 `json.Unmarshal` 到 `ScriptOutput{Title, Logline, Scenes[]}`。**业务校验**只有 `Title=="" || len(Scenes)==0` 时报错（`script.go:73-75`）。产物落 `scripts` 表（`worker.go:376-380`）。

**Storyboard agent** (`internal/agents/storyboard.go`)：同模板，input 是上游 script 整段 `content_json`（worker 传 `string(contentJSON)`，`worker.go:423-425`），system prompt 要求 `{shots:[{shotNo,camera,scene,action,prompt,duration}]}`，产物 `[]Shot` 落 `shots` 表 + 同 tx 用 `AddDynamic` fan-out 出 `asset` todo（`worker.go:457-491`）。注意 `shotNo/camera/scene/action/prompt/duration` 是 M1 schema，**不含 `kind` 字段**——M4 才有 `kind: "video|audio"`，M1 fan-out 硬编码 `"kind":"image"`（`worker.go:474-481` 注释明说）。

**ChatModel 抽象**：所有 agent 都用 `coreagents.NewSimpleAgent(model, opts)` 拿一个临时 agent（`script.go:55`、`storyboard.go:51`、`agents/asset.go` 不调 LLM 而是直接调 `MediaGenerator`）。**没有 router 抽象层**——解耦点在 `agents.RunWith(ctx, model, in)` vs `agents.Run(ctx, in)`，worker 通过 `routedChatModel()`（`worker.go:797-806`）解出 org 的 chat model 再走 `RunWith`，未配 BYOK 时退化走 `Run`（`worker.go:366-370`、`428-432`、`820-824` 三处统一模式）。

**Asset agent** (`internal/agents/asset.go`)：不做 LLM 调用，只走 `prompt.Builder.Build` 拼风格后缀（`asset.go:48`）+ `MediaGenerator.Generate`。`RunWith` 接受传入的 `gen` 实现 per-org 路由（`asset.go:47-58`）。

**输出落盘模式**：每 agent 产 object → worker 立即 `INSERT INTO scripts|shots|assets`（不用 outbox / 不用单独 commit cycle）；脚本与 storyboard 在 worker 自己的 tx 里写，asset 的"submitted→pending_acceptance" 在 `worker.SetBlob` 的 status 守门下原子翻状态。

## 6. Events / SSE 深挖

**事件写入**：`events.Store.Append` (`events/store.go:32-48`) 直接 `INSERT INTO run_events ... RETURNING seq`。**无 outbox / 无内存 broker / 无 pubsub**——`run_events` 表就是唯一事实源。事件由 worker + handler 同步写。`sse.go` 的轮询（每 500ms 一次 `List`）才是推送机制。

**seq 生成**：PG `BIGSERIAL PRIMARY KEY`（`storage.go:114`），单库自增。**不是 snowflake / 不是时间戳**。

**服务端去重**：靠 PG 唯一约束 + 序列号，不是应用层去重。但有两个**并发安全陷阱**被显式处理：
- `AppendRunDone` (`events/store.go:59-88`)：用 `pg_advisory_xact_lock(hashtext($1))` 串行化同一 project 的 run_done 写入，第二个 worker 的 `NOT EXISTS` 才看得见第一个的提交
- 事件名出 SSE `event:` 头前过白名单 `sseEventNames`（`sse.go:22-32`），防伪造 kind 注入头

**Last-Event-ID / 重连去重**：**M1 未实现 Last-Event-ID 头解析**——`sse.go:39-90` 的 `streamEventsHandler` 只读 path value，**never 读 `r.Header.Get("Last-Event-ID")`**。客户端若用 EventSource API 自带重连，浏览器会带 `Last-Event-ID` 头但被忽略。GET 列表端点 `listEventsHandler`（`handlers.go:325-340`）倒是支持 `?afterSeq=` query param（`handlers.go:328-332`）做手成分页。

> **Followup**：[PR #17](https://github.com/costa92/llm-agent-studio/pull/17) 已修复——`sse.go:51-60` 解析 `Last-Event-ID` 头，每条事件加 `id:<seq>` 行供 fetch-event-source 自动捕获并在重连时透传。

**回放续接实时的接缝**：`sse.go:74` 第一次 `emit()` 不带 ticker——先一次性回放历史（`reader.List(projectID, 0, 200)`），**没看到 run_done 就 ticker 500ms 切轮询**。`emit` 内维护闭包变量 `after`（`sse.go:51`）持续向前推——新一次 List 只会 `seq > after` 拉差量。所以**回放是"先 list 一次拿到一个 block，然后 list 拉差量直到命中 run_done"**，不是 cursor 持续推 + 后台 notifier。

## 7. Authz / RBAC 集成深挖

**M1 完全不自己实现 RBAC**——纯靠 `llm-agent-authz`（`httpapi.go:11-13` import）。中间件实现在库 `authzhttp.Authenticate` / `RequireScopeRole`（`llm-agent-authz/httpapi/middleware.go:24, 54`），studio 这边只做**三件事**：
1. 定义 scope 解析器（`httpapi.go:66-91`）：`assetScope` / `projectScope` / `orgScope` / `platformScope`（后两者其实在 `platformhandlers.go:29`）
2. 拼装 `authOnly` / `scoped` / `proj` / `asset` / `platformAdmin` 五个闭包工厂（`httpapi.go:106-125`）
3. 把每个路由挂上对应工厂

**角色枚举**（`llm-agent-authz/role/role.go:9-15`）：`RoleNone` (rank 0) / `RoleViewer` (1) / `RoleEditor` (2) / `RoleAdmin` (3) / `RoleOrgAdmin` (4)。`AtLeast` 用 rank 比较（`role.go:34`），`Merge` 取最高（`role.go:37-45`）——这是 org⊕scope 合并代数，但 M1 的 `RequireScopeRole` 是**单 scope 解析**（不 merge 多个 scope），注释提到"scope_id IS NULL OR scope_id=$4"暗示多 scope merge 留给 store 层（`httpapi.go:78`）。

**scope_kind** 三种：
- `"org"`（`httpapi.go:112`）：org-level 成员管理，scope_id=NULL
- `"platform"`（`httpapi.go:123`）：平台超级管理员，固定 `("","")`（`platformhandlers.go:29`）
- （asset 仍走 `"org"` scope，**没有独立 `asset` scope_kind**——`assetScope` 只把 orgID 解出来传 `RequireScopeRole(_, "org", ...)`，**`httpapi.go:65-74` 注释里说的 `scope_kind="asset"` 其实不存在**——这是个文档与实现脱节点）

**权限矩阵**（仅列 M1 实际挂的路由，`httpapi.go:130-196`）：

| 路由 | 最低角色 | scope | factory |
|---|---|---|---|
| `POST /api/orgs` | authOnly | — | `authOnly` |
| `GET /api/orgs` | authOnly | — | `authOnly` |
| `POST /api/orgs/{org}/projects` | editor | org | `scoped(editor, orgScope)` |
| `GET /api/orgs/{org}/projects` | viewer | org | `scoped(viewer, orgScope)` |
| `GET /api/orgs/{org}/tasks` | viewer | org | `scoped(viewer, orgScope)` |
| `GET /api/projects/{id}` | viewer | project (orgID) | `proj(viewer)` |
| `POST /api/projects/{id}/run` | editor | project | `proj(editor)` |
| `POST /api/projects/{id}/cancel` | editor | project | `proj(editor)` |
| `GET .../events[ /stream]` | viewer | project | `proj(viewer)` |
| `GET .../todos\|script\|shots\|assets` | viewer | project | `proj(viewer)` |
| `GET /api/assets/{id}` | viewer | asset (orgID) | `asset(viewer)` |
| `POST /api/assets/{id}/{accept,reject,regenerate}` | admin | asset | `asset(admin)`（M2+） |
| `GET /api/platform/*` (除 whoami) | admin | platform | `platformAdmin` |
| `GET /api/orgs/{org}/cost*`, `.../generations` | admin | org | `scoped(admin, orgScope)`（M2+） |
| `GET /api/blob/{key...}` | (无) | HMAC sig | 无 auth |

外加 `withUserLimit`（`httpapi.go:207-215`）在 auth 之后用 `limits.Guard` 做 per-user 速率限制。

## 8. Otel 集成深挖

**M1 复用 `llm-agent-otel`**（`obs.go:10-12` import `otelmodel`、`otelagent`、`otelexport`），不自己加 trace hook。**K3 keystone 说的"必须用 `otelmodel.Wrap(inner)`"** 在 `obs.WrapModel`（`obs.go:39-41`）和 `obs.WrapAgent`（`obs.go:44-46`）两个工厂里实现，`cmd/studiod/main.go:138` 在构造默认 chat model 之后 `model = obs.WrapModel(model, tp)`。**Worker 端不调 otelmodel/otelagent 包装 agent**——它直接用 `cfg.Tracer` 自己开 span（`worker.go:246-250` 的 `studio.todo.<typ>`），这是 worker 级的 span 命名。

**span 命名规范**（统一前缀 `studio.`）：
- 模型调用：`otelmodel` 内部命名（不直接可见）
- 生成调用：`studio.generate.<kind>`（`obs.go:74`，含 `studio.{provider,model,image_count,tokens,latency_ms}` 属性）
- 异步提交/轮询：`studio.generate.submit.<kind>` / `studio.generate.poll.<kind>`（`obs.go:100, 117`，含 `studio.external_job_id`、`studio.poll_status`）
- Worker todo：`studio.todo.<typ>`（`worker.go:246`，含 `studio.{project_id,todo_id,attempts}`）
- Worker ctx fallback：`studio.worker`（`worker.go:122`）

**attribute keys**：统一 `studio.<snake>` 风格；`events` 写在 span events 上（`worker.go:303 "async.rescheduled"`、`worker.go:313 "async.lease_lost"`）。

**统一 tracername**：`tp.Tracer("studio.worker")`（`main.go:289`）、`tp.Tracer("llm-agent-studio/generate")`（`obs.go:59`）——tracer name 略不一致（`studio.worker` vs `llm-agent-studio/generate`），可观察后端得用不同 filter。

**`platformScope` 在 platformhandlers.go 而非 httpapi.go**：scope_kind 解析器散落两处。

## 9. M1 留下的设计接缝（被后续里程碑利用的点）

1. **`todos.depends_on TEXT[]` + `AddDynamic(ctx, tx, ...)` 的"传入 tx"签名**（`todos/store.go:164-177`）——M1 是为 storyboard fan-out asset 而生（`worker.go:490`）。M2+ 复用同一签名在 storyboard fan-out 写 shot→asset 的同时落 shots 行；M3+ 加 `kind/duration` 字段；M4 async 视频/音频亦基于此签名扩展。
2. **`output_ref` 字符串格式 `<type>:<id>`**（`worker.go:381 "script:"`、`:502 "shots:"`、`:672 "asset:"`）——M1 script/storyboard 用来做依赖反查（`worker.go:395-399` 的 `LIKE 'script:%'`）；M2+ asset 模式让反查逻辑能跨多代脚本版本精确定位上游，**比"最新 created_at DESC"更稳**（`worker.go:407-412` 的 fallback 是 M1 carry 的临时方案）。
3. **`tracedAsyncGenerator` 嵌入 `tracedGenerator`**（`obs.go:94-128`）——M1 视频/音频 async 还没真接，但 M4 直接套这个 wrapper 就保留了 `AsyncGenerator` 接口（`obs.go:60-63` 的 type assertion 保留），B1 注释说"otherwise the worker's routed.(AsyncGenerator) assertion is stripped"——这是给 M4 准备的接缝。
4. **`models.Store.CreateInput.IsDefault` + `DefaultForOrg(ctx, orgID, kind)`**（worker_test.go:64-69 演示，main.go:243-249 装配）——M1 用它做 text 模型 BYOK 路由（`modelrouter.ChatModelFor`），M3+ 直接把它扩到 `MediaGeneratorFor`，M4 视频/音频 per-kind 模型也吃同一 store。
5. **`events.AppendRunDone` 用 `pg_advisory_xact_lock(hashtext(projectID))` 配 `NOT EXISTS` + planner_started 边界**（`events/store.go:50-88`）——M1 是为了"两个 worker 同事 allDone"防双发设计的；M2+ asset fan-out 后 done 计算集变大，这个"窗口"约束靠 planner_started 切 run，让重跑（重规划）能开新窗口。
6. **`scripts` 表 `version INT` + `assets` 表 `version INT` + `parent_asset_id`**——M1 scripts.version 始终 1（`worker.go:378`），M2+ HITL 接受/重新生成 v2 用这套字段做版本链；M3+ prompt editing 沿同一链。
7. **`Planner.PlanWith(ctx, projectID, model, brief)`**（`planner.go:74`）——M1 几乎只用 `Plan`，但 `PlanWith` 把 chat model 注入点开了，M2+ BYOK 路由直接走 PlanWith 而不改 Planner。

## 10. 独立观察 / 疑问

1. **`graph.go:17` 的 `m1Types` 命名歧义**——变量叫 `m1Types`，但 map 里包含 `asset`；注释自我纠正说"是 M2 whitelist"。读代码的人会被这个变量名误导（搜索"m1"会以为 M1 = {script,storyboard}）。M1 真正在跑的 dispatch type 是 `worker.go:294-296` switch 的三个分支。

2. **`worker.fail` 不会把 `attempts` 重置回 0**（`worker.go:782-786`）——`attempts` 单调累加 + 退避 `BaseBackoff * (1 << (attempts-1))`，**第 3 次退避 = 4*BaseBackoff，3 次后就 mark failed，次数本来就够小**所以现实不会爆，但**没有上限保护**，MaxAttempts 改大时退避可能秒级飞涨。同时 `MarkDone` 不更新 attempts（`store.go:91`），但 `claim` 时 SQL `attempts=$4` 写回新值（`worker.go:231`）——这里 `attempts` 实际是"本 todo 的累计尝试次数"，`next_run_at` 是重调度时间。

3. **`httpapi.go:65-74` 注释里说"assetScope... scope_kind='asset'"**——但 `assetScope` 实际是把 `orgID` 解出来传给 `RequireScopeRole(_, "org", ...)`（`httpapi.go:146-148`），不存在 `scope_kind="asset"`。**注释与实现脱节**。如果未来真有 per-asset RBAC 需求（比如 asset-level viewer），需要新增 `scope_kind="asset"` 而不是 `org`。

4. **SSE 不读 `Last-Event-ID` 头**（`sse.go`）——浏览器 `EventSource` 自动重连带 `Last-Event-ID`，M1 服务端忽略。`?afterSeq=` 是 GET 列表端点（`handlers.go:328`）的参数，但 SSE 端点没接；客户端如果想"重连到 last seen seq"必须改用 fetch + 自管。
   > **Followup**：[PR #17](https://github.com/costa92/llm-agent-studio/pull/17) 已修。

5. **claim 的 soft cap 注释**（`worker.go:204-210`）坦白说"FOR UPDATE SKIP LOCKED 不真锁 count，READ COMMITTED 下并发 worker 各自看到旧 count，可能 overshoot by Workers-1"——这是有意识的工程取舍（`评审修复 M5` 标记），但**没有同等可观测的 cap 也无 hard lock**——M2+ 真要硬隔离得走 `pg_advisory_xact_lock`（async submit 路径就是这么做的，`worker.go:1009-1010`），sync 路径没加。

6. **`worker.process` 里 lease-renew goroutine 的 cancel 顺序**（`worker.go:265-283`）——`defer func() { hbCancel(); hbDone.Wait() }()` 等所有心跳 goroutine 退出再返回。**但 `defer cancel()` 在 `CallTimeout` 的 `defer cancel()` 之后注册**（`worker.go:255-259` 与 `:282` 的相对顺序）——Go 的 defer 是 LIFO，所以**心跳 cancel 先于 CallTimeout cancel 触发**。这本身不 bug，但读起来反直觉（先关"长寿"的，再关"短寿"的）。rename 或显式顺序都能提可读性。

7. **`runAsset` 的 `cctx = context.WithoutCancel(ctx)`**（`worker.go:536`）——detach 取消但保留 ctx values，理由是 "CallTimeout fire 后还得写 asset 状态"，这在 sync 路径是合理的；**但 `cctx` 也被传到 `runAssetAsync`（`worker.go:976` 的 `cctx`）用于 `submitTx`**——async 路径在 detach ctx 上跑 advisory lock、insert generation、reschedule todo，**这意味着 worker ctx 被 cancel 后，submit 的最后一段事务仍会跑完**。看起来这是有意的（不能让 submit 半成功），但 `runAssetAsync` 内部调 `w.pollAsync(ctx, cctx, ...)` 却又把原 `ctx` 传 Poll——Poll 仍然受 CallTimeout 限制（不然一次 Poll 跑 1h 浪费 lease）——这个**ctx 双重性**在 M4 async 关闭的整段（worker.go:1078-1117）需要后续读者仔细分辨。

8. **`tracer name` 不一致**——`studio.worker`（`main.go:289`、`worker.go:122`）vs `llm-agent-studio/generate`（`obs.go:59`）。后端 filter 写 dashboard 时得知道两个 tracer scope 才能拼出完整调用链。M3+ 建议统一到 `studio.<subsystem>` 风格。

9. **没有 plan 级重试/重跑语义**——`Planner.Plan` 总是 INSERT 新 `plans` 行（`planner.go:106-108`）；一个 project 跑多次会累积多行 plans，但 todo 表用 `plan_id` 列保留血缘（`store.go:65`）。`plannerStarted` 事件的 "重规划开新 dedup 窗口"（`events/store.go:75-76` 的 `seq > COALESCE(max planner_started)`）暗示有"重跑"概念，但**没有 UI 端按钮，也没有"重跑 vs 重规划" 的清晰区分**——这两件事在 M1 混在同一条 `POST /run` 上，行为差异是隐式的（重规划 = 插入新 plans 行 + 新 todos，但 `MarkDone` 守门不阻止旧 todo 继续完成）。

10. **`Planner.Plan` 失败时 rawText 包成 `json.Marshal` 字符串**（`planner.go:104` `rawJSON, _ := json.Marshal(rawText)`）——`rawText` 本来是 `agent error: %v` 字符串，被 marshal 成 `"agent error: ..."`（带外层引号的 JSON 字符串字面量），落库到 `plans.raw_plan_json` (JSONB)。后续读取的"raw plan"实际上是双重 JSON 编码——审计时如果直接 `content_json->>...` 拿到的会是带转义的字符串，**不是干净的 LLM 文本**。这是一个 audit UX 障碍。

---

## 横向参考

- 单 run 调用链：[architecture/run-flow.md](./architecture/run-flow.md)
- 全景子系统图：[architecture/subsystem-map.md](./architecture/subsystem-map.md)
- M2 实现剖析：[m2-implementation-deep-dive.md](./m2-implementation-deep-dive.md)
- M4 实现剖析：[m4-implementation-deep-dive.md](./m4-implementation-deep-dive.md)
- M8 实现剖析：[m8-implementation-deep-dive.md](./m8-implementation-deep-dive.md)
