# 创建项目工作流:状态单一权威源重构设计

- 日期:2026-06-15
- 仓库:`llm-agent-studio`
- 状态:设计已确认,待写实现计划
- 作者:costa（brainstorming with Claude）

## 1. 背景与问题

创建项目工作流(create-project workflow)的**执行过程**(后端)与**渲染过程**
(前端)之间的状态逻辑设计不合理:**存在两个互相独立的"工作流当前状态"真相源**。

- **后端**:`internal/project/status.go DeriveStatus(TodoCounts)` 从 todo 计数派生
  一个粗粒度 `project.status`(7 态);同时 handler 里命令式地
  `SetStatus("planning"/"running")`(`internal/httpapi/handlers.go:381,423`)。
- **前端**:`web/src/lib/timeline.ts reduceTimeline` **另起一套**,通过回放整条
  事件流(`fetchAllEvents` + SSE)重建细粒度状态——S1–S5 阶段状态、`runStatus`、
  pip 计数、`pendingAssetCount`、`slateVisible` 等。

两者从不互相校验,导致的具体症状:

1. **徽章自相矛盾 / 闪烁 / 竞态**:`WorkbenchPage.tsx:88-95` 把 `runStatus`(来自
   事件)和 `project.status`(来自轮询)混在一起算徽章;`runStatus` 先到 `done` 而
   `project.status` 轮询还没刷新时,会出现"待审核"与 `running` 并存的瞬间。
2. **状态在两处计算**,无同步保证:后端 `DeriveStatus` vs 前端 `reduceTimeline`。
3. **事件载荷欠规范**:`todo_failed` 缺 `type` 字段(其余 `todo_ready/started/
   finished` 都带),前端被迫用 `frame.todoId` 兜底定位(`timeline.ts` 注释自述)。
4. **枚举两处手写**:`project/status.go` 与 `lib/types.ts` 各写一份,易漂移。
5. **渲染态从事件反推**:`pendingAssetCount` 前端手数 `asset_generated` 事件。

**根因**:前端在用一条"本就不是为渲染契约设计"的事件流,去**重新推导后端已经
知道的执行状态**。

## 2. 目标与非目标

### 目标
- 工作流状态有**唯一权威源**,前端不再自行推导状态。
- 徽章/阶段图/计数全程与后端一致,无闪烁、无自相矛盾、无轮询 vs SSE 竞态。
- 收敛分散的状态计算逻辑到一处,降低出错面与维护成本。

### 非目标
- 不重构执行模型本身(planner / todo 图 / worker 调度保持不变)。
- 不引入新基建(不引 codegen、不引 LISTEN/NOTIFY)。沿用现有 500ms 轮询。
- 后端不接管 UI 表现层(i18n 标签、颜色、S1–S5 编号、排序仍归前端)。

## 3. 核心决策(brainstorming 确认)

| 决策点 | 结论 |
|---|---|
| 真相源位置 | **后端权威 + 推快照**;前端变纯渲染 |
| 快照粒度 | **只管语义状态**;前端保留表现层(标签/颜色/布局/日得文案) |
| 传输方式 | **REST 权威端点 + SSE 推快照**,两者走同一个后端 Compute |
| 变更检测 | 沿用现有 **500ms 轮询 events 表**,不引新基建(状态变更 ≤500ms 延迟,可接受) |

## 4. 架构设计

### 4.1 单一权威状态计算器

新增后端包 `internal/projectstate`,核心是一个**纯函数**(无 I/O):

```go
projectstate.Compute(p Project, todos []Todo, assets AssetCounts, latestPlan *Plan) ProjectState
```

- **全系统唯一**计算"工作流处于什么状态"的地方。
- 收敛现有分散逻辑:
  - `project/status.go DeriveStatus`(整体 `status`)并入。
  - handler 命令式 `SetStatus("planning"/"running")` 改为依赖 Compute 结果
    (注:`Total==0` 时 `DeriveStatus` 本就返回 `"planning"`,可去掉显式置位)。
  - 前端 `reduceTimeline` 的**语义部分**(阶段状态、runStatus、资产计数)搬到这里。
- DB 持久化的 `project.status` 降级为**派生缓存**(由 `RefreshStatus` 写回),不再是
  独立真相。
- **REST 端点与 SSE 推送器都调用同一个 `Compute`**,从根上保证两通道、两时刻
  状态一致。

**关键边界**:`Compute` 只产出**语义角色**(`planner` / `script` / `storyboard` /
`asset×N` / `review` 各自的 status),**绝不产出** S1–S5 编号、颜色、中文标签——
那些是前端表现层。

### 4.2 ProjectState 语义快照 schema

`Compute` 的返回值(也即 REST/SSE 下发的快照):

```jsonc
{
  "projectId": "...",
  "version": 42,              // 单调递增(= events.seq 最大值),用于排序/去重/重连对账
  "status": "running",        // 现有 7 态,由 Compute 派生
  "runStatus": "running",     // idle | running | done — 整体跑动语义
  "plan": { "planId": "...", "valid": true, "fallbackUsed": false },
  "stages": [                 // 语义角色,非 S1-S5;asset 阶段为聚合态(明细看 assets)
    { "role": "planner",    "status": "done",    "todoId": "..." },
    { "role": "script",     "status": "running", "todoId": "..." },
    { "role": "storyboard", "status": "pending", "todoId": "..." },
    { "role": "asset",      "status": "blocked", "todoId": null  },
    { "role": "review",     "status": "blocked", "todoId": "..." }
  ],
  "assets": { "total": 6, "done": 4, "pending": 2 },  // asset 阶段明细:后端权威计数,前端不再数事件
  "error": { "todoId": "...", "role": "asset", "message": "..." }  // 或 null
}
```

前端拿到后做**纯映射,零状态推导**:
- `role → S1-S5` 布局映射表
- `status → 颜色`
- `status → 中文标签`
- `pendingAssetCount` 直接读 `assets.pending`

枚举对应关系:
- `status`(project):`draft|planning|running|review|completed|failed|canceled`(7 态,现状)
- `runStatus`:`idle|running|done`
- `stage.status`:`blocked|pending|running|done|failed`
- `role`:`planner|script|storyboard|asset|review`

## 5. 传输设计

### 5.1 REST 权威端点

```
GET /api/projects/{id}/state  →  ProjectState
```

内部 = 读 todos/assets/plan → 调 `projectstate.Compute` → 返回。用途:初始加载、
重连兜底、mutation 后对账。任何时候拉它都拿到完整一致快照。

### 5.2 SSE 推快照

现有 `GET /api/projects/{id}/events` 拆为职责清晰的两件事:
- **连接时**立即下发一条 `state` 事件 = 全量 `ProjectState`(免前端回放全史)。
- 之后后端 500ms 轮询检测到 `version`(= events.seq 最大值)变化时,重新 `Compute`
  并推一条新的 `state` 事件。**整快照替换**(不做 patch——快照小,full-replace
  最简、最不易错;`version` 单调递增用于丢弃乱序/重复帧)。
- **原始事件照旧推**(`todo_started` 等),但前端**只用于追加左侧日得**,不再用来
  推导状态。

两通道都过 `Compute`,因此 REST 拉到的与 SSE 推到的永远同一套逻辑算出。

## 6. 前端改造

`web/src/lib/timeline.ts` 从"状态机"退化为"事件→日得文案的纯函数 + 一组映射表":

- **删除**所有状态推导:S1–S5 status、`runStatus`、`doneAssetCount`/
  `pendingAssetCount`、`slateVisible` 不再从事件算。
- **保留为纯表现**:`role → S1-S5` 映射、`status → 颜色`、`status → 中文标签`、
  日得文案 `logFor`(继续从原始事件生成文案,属表现层,合理)。
- `WorkbenchPage` 数据来源换为:`useProjectState(id)`(TanStack Query 读 REST 端点)
  + SSE `state` 事件直接 `setQueryData` 覆盖缓存 → 组件永远渲染后端权威 `ProjectState`。
- 徽章、阶段图、pip 网格、"待审核 · N" 全部读 `ProjectState` 字段。
- **前端不再有"自己算的状态"与"后端给的状态"两份。**

## 7. 契约对齐与事件载荷修复

- **枚举单一来源**:由后端 Go 定义,前端 TS 类型对齐 `ProjectState` 形状。最小做法
  =手工保持一致(沿用现状)+ 一个**契约一致性测试**:前端 `lib/types.ts` 联合类型
  vs 后端枚举,任一侧新增态而另一侧没跟则测试红。**不引 codegen**。
- **修事件载荷**:`todo_failed` 补 `type/role` 字段(对齐 `todo_ready/started/
  finished`),统一 `worker.go` 两处发射点;删除 `timeline.ts` 里靠 `todoId` 兜底的
  workaround。注意:此修复主要服务**日得文案**,状态正确性已由 `Compute` 保证。

## 8. 迁移顺序与验证

按"先建权威源,再切前端,最后清理"推进,每步可独立验证、可回滚:

| 步 | 改动 | 验证 |
|---|---|---|
| 1 | 后端新增 `projectstate.Compute` + 单测(覆盖 7 态 + 各阶段组合) | `GOWORK=off go test ./internal/projectstate/... -count=1` 全绿 |
| 2 | 加 `GET /api/projects/{id}/state`(调 Compute) | handler 测试:造数据断言快照字段 |
| 3 | SSE 连接发全量 `state` 事件 + 变更推 `state`(`version` 去重) | `sse_test` 断言首帧 state + 变更后再推 |
| 4 | `todo_failed` 补 `type/role`;handler 命令式 `SetStatus` 收敛为依赖 Compute | worker/handler 测试 |
| 5 | 前端 `useProjectState` + SSE 覆盖缓存;`WorkbenchPage` 徽章/阶段图改读 `ProjectState` | `workflow.test.tsx` 改造,断言渲染来自快照而非 reduce |
| 6 | `timeline.ts` 删状态推导,只留日得文案 + 映射表;删 workaround | timeline 单测改为只测文案;跑通 e2e 一条龙 |
| 7 | 契约一致性测试 | 故意删一个枚举值,测试应红 |

**整体成功标准**:跑一个完整 create-project 工作流,徽章/阶段/计数全程与后端
`GET /state` 一致、无闪烁无自相矛盾;前端再无任何"自己推导状态"的代码路径。

## 9. 受影响文件清单(便于实现计划)

后端:
- `internal/projectstate/`(新增)
- `internal/project/status.go`(`DeriveStatus` 并入 Compute)
- `internal/httpapi/handlers.go`(`runHandler` 收敛 `SetStatus`;新增 state handler)
- `internal/httpapi/workflowhandlers.go`(同上,run 路径)
- `internal/httpapi/sse.go`(连接发全量 state + 变更推 state)
- `internal/worker/worker.go`(`todo_failed` 补 `type/role`)

前端:
- `web/src/lib/timeline.ts`(瘦身:删状态推导,留文案 + 映射)
- `web/src/lib/types.ts`(新增 `ProjectState` 等类型 + 契约一致性测试)
- `web/src/features/workflow/WorkbenchPage.tsx`(改读 `ProjectState`)
- `web/src/features/workflow/api.ts`(`useProjectState` + SSE 覆盖缓存)
