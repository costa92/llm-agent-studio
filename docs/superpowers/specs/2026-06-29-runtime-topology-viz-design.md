# 运行态拓扑流程可视化 + 视图内设置面板 — 设计文档

- 日期：2026-06-29
- 范围：`llm-agent-studio` 前端 `web/`
- 载体：`RunCanvas`（ReactFlow 运行只读画布）
- 性质：**纯前端增强，零后端 / 零 DB 迁移**

## 1. 目标

在已有的运行态画布上补四项能力，并配一个**视图内设置面板**控制它们：

1. **数据流向动画** —— 执行前沿的边走流动动画，直观看到「现在进行到哪一步」。
2. **节点耗时 / 进度** —— 每个节点显示运行耗时；running 节点实时跳秒。
3. **自动布局 + 导航** —— 运行态可选自动分层布局，配合 MiniMap / fitView / 平移缩放。
4. **状态过滤 / 高亮** —— 按状态淡化/隐藏节点，失败节点高亮，点节点看详情。

设置以**视图内面板**（画布右上齿轮）呈现，改动实时生效，偏好存 `localStorage`。

## 2. 非目标（YAGNI 边界）

- **不改后端 / 不加 DB 列 / 不做迁移。** 计时纯客户端，从 SSE 事件累积。
- **设置不入库、无独立路由页。** 它们是视图偏好，存 `localStorage` 即可。
- **不动 `GraphView`**（WorkbenchPage 里的 CSS 分层 DAG）。本次只做 `RunCanvas`。
- **不引 `dagre` / `elk`** 等布局库。自动布局复用现成 `layerize()`。
- 不做跨工作流的拓扑总览（那是另一个独立需求）。

## 3. 关键约束与既有事实

来自对现有代码的核实，设计据此成立：

- `GraphNode`（`web/src/lib/projectState.ts:44`，后端 `internal/projectstate/state.go:39`）**无计时字段**。
- `Todo` 只有 `created_at` + `updated_at`；**无干净的 `started_at`**，且 `buildGraph`
  注释明确警告 run 期间 `updated_at` 持续漂移（`internal/projectstate/state.go:419`）。
  ⇒ 计时不能从快照取，**走客户端 SSE 事件累积**。
- 运行态节点 id（`ProjectState.nodes[].id`）是全新 todo UUID，与画布节点 id（如
  `script-1`）**无持久回链**；靠 `(type, 拓扑序序号)` 结构映射，见
  `web/src/features/workflow-canvas/runOverlay.ts`。`RunNodeStatus` 已携带 `todoId`
  —— 这是计时回链画布节点的钥匙。
- `layerize()`（`web/src/lib/graphLayout.ts:6`）已能把 DAG 拓扑分层并自带环防御，
  可直接派生 ReactFlow 坐标。
- `StudioEdge` 是自定义边类型 —— ReactFlow 内置 `animated` 对自定义边无效，
  动画须在 `StudioEdge` 组件内自行实现。
- `RunCanvas`（`web/src/features/workflow-canvas/RunCanvas.tsx`）已有 `MiniMap` /
  `Controls` / `fitView`，但布局用工作流自存 `position`，无自动布局、无边动画、
  无耗时、无过滤。
- SSE 已由 `useProductionTimeline`（`RunCanvas` 已用）解析；`todo_started` /
  `todo_finished` 事件在流里。计时累积从这里接出。

## 4. 架构与数据流

```
SSE 事件 ─┬─► useNodeTiming      (todoId → {startedAt, finishedAt, elapsedMs})
          └─► onState ──────────► ProjectState   (状态权威源，不变)

overlayRunStatus(nodes, state) ─► 画布节点id → RunNodeStatus(.todoId, .status, ...)

计时 join:  nodeId ──(RunNodeStatus.todoId)──► 耗时

useTopologySettings ─► { layout, showTiming, flowAnimation, statusFilter,
                         highlightFailed, fitOnUpdate }

RunCanvas 组装:
  rfNodes = toReactFlow(nodes)
            + 注入 data.run (overlay)
            + 注入 data.timing (计时 join，showTiming 时)
            + 注入 data.highlightFailed
            + 坐标: layout==="saved" ? 自存position : autoLayout(方向)
            + 按 statusFilter 标记 hidden / dimmed
  rfEdges = toReactFlow 边
            + 标记 active (源 done 且 目标 running) → flowAnimation 时动画
```

设计原则：**`ProjectState` 仍是状态唯一权威源**（keystone：状态不由事件 reduce 推导）。
计时是 epheremal 叠加层，与状态正交，不写回 `ProjectState`，不破坏该 keystone。

## 5. 组件清单

### 新增

| 文件 | 职责 | 依赖 |
|---|---|---|
| `web/src/lib/autoLayout.ts` | `autoLayout(nodes, edges, direction)` → `Map<id, {x,y}>`。复用 `layerize` 分层：层 index → 主轴坐标，层内 index → 交叉轴居中。`direction: "TB" \| "LR"`。 | `layerize`, projectState 类型 |
| `web/src/features/workflow-canvas/useTopologySettings.ts` | localStorage 持久化偏好（key `studio.topology.settings`）。zod schema + 默认值 + `safeParse` 坏数据回落。返回 `{settings, setSettings(partial)}`。 | zod |
| `web/src/features/workflow-canvas/TopologySettingsPanel.tsx` | 画布右上齿轮按钮 → Popover/Sheet。控件绑定 `useTopologySettings`，实时生效。复用 `@/components/ui` + studio 主题 token。 | useTopologySettings, radix popover/sheet |
| `web/src/features/workflow/useNodeTiming.ts` | 从 SSE 事件累积 `todoId → {startedAt, finishedAt?, elapsedMs}`。running 节点用 `now - startedAt` 实时跳秒（轻量 interval，仅有 running 时启用）。 | useProductionTimeline 的事件出口 |

### 改动

| 文件 | 改动 |
|---|---|
| `web/src/features/workflow-canvas/StudioEdge.tsx` | `data.active` 时渲染流动虚线动画（CSS keyframes，`motion-safe`）。无 active / 关闭动画时同现状。 |
| `web/src/features/workflow-canvas/WorkflowNode.tsx` | `data.timing` 存在且 `showTiming` 时渲染耗时徽标（running 跳秒 / done 显总耗时）。`data.highlightFailed` 且 failed 时加红环。 |
| `web/src/features/workflow-canvas/RunCanvas.tsx` | 串接全部：接 `useTopologySettings` + `useNodeTiming`；坐标按 `layout` 选自存/autoLayout；`statusFilter` 标记 hidden(`hidden` 属性)/dimmed(透明度)；边标 active；挂齿轮面板；布局变更且 `fitOnUpdate` 时 `fitView`。 |

> `useProductionTimeline` 若未暴露逐事件回调，`useNodeTiming` 取数有两条等价路径，
> 二选一在计划阶段定：(a) 给 `useProductionTimeline` 加一个 `onEvent(evt)` 出口；
> (b) `useNodeTiming` 复用同一 `fetchAllEvents` + SSE 源独立累积。优先 (a)（单一 SSE 连接，
> 不重复连）。

## 6. 设置项（面板内）

| 设置 | 类型 | 默认 | 效果 |
|---|---|---|---|
| 布局 `layout` | `"saved" \| "TB" \| "LR"` | `"saved"` | 自存坐标 / 自动竖向 / 自动横向 |
| 显示耗时 `showTiming` | bool | `false` | 节点耗时徽标开关 |
| 数据流动画 `flowAnimation` | bool | `false` | 执行前沿边动画开关 |
| 状态过滤 `statusFilter` | `Record<GraphNodeStatus, "show"\|"dim"\|"hide">` | 全 `"show"` | 按状态显示/淡化/隐藏 |
| 高亮失败 `highlightFailed` | bool | `false` | 失败节点红环 |
| 布局更新 fitView `fitOnUpdate` | bool | `true` | 布局/方向变更时自动 fitView |

**默认 = 现有行为**（自存坐标、无动画、无耗时、显示全部）⇒ 不开设置时零回归。

## 7. 错误处理与降级

- `localStorage` 解析失败 / schema 漂移 → zod `safeParse` 回落默认值，不抛。
- 计时缺失（节点无 `todo_started`，或冷加载历史 run 回放无时间戳）→ 节点不显徽标，
  优雅降级，不报错。**已知限制：历史 run 的 done 节点耗时可能近似/缺失**——文档注明，
  实时观看 run 时准确。
- `autoLayout` 遇环 → `layerize` 已有环防御（停在已达层，不死循环）。
- 未知 status → 中性渲染（沿用现有 `overlay` 未命中即 pending/中性的约定）。
- 过滤把全部节点隐藏 → 画布空，复用现有「该工作流暂无节点」空态提示或类似文案。

## 8. 测试

- `autoLayout` 单测：层→坐标映射、TB/LR 方向、层内居中、空图、单节点、带环图。
- `useTopologySettings`：默认值、写入持久化、坏 JSON / 缺字段回落默认。
- `useNodeTiming`：`started→finished` 算 `elapsedMs`、running 实时、事件乱序、
  缺 `started` 只有 `finished` 的容错。
- 契约测试：`statusFilter` 键覆盖全部 `GraphNodeStatus` 域（`blocked|pending|running|done|failed`），
  与 `projectState.ts` 漂移守护对齐。
- 回归：设置为默认时，`RunCanvas` 的 rfNodes 坐标 == 自存 position、无 active 边、
  无耗时徽标 —— 与现状逐项一致。

## 9. 执行方式

设计定稿 → 本 spec 入库 → `writing-plans` 出实现计划 → 派 subagent 实现
（一次一个任务、任务间审查）。遵循本仓 PR 纪律：分支 → push → PR → rebase 合，
不直推 main。本设计已在分支 `feat/runtime-topology-viz` 上。
