# 运行态拓扑流程可视化 + 视图内设置面板 — 设计文档

- 日期：2026-06-29
- 范围：`llm-agent-studio` 前端 `web/`
- 载体：`RunCanvas`（ReactFlow 运行只读画布）
- 性质：**纯前端增强，零后端 / 零 DB 迁移**
- 评审：3 个 agent（架构 / 正确性 / 前端 UX）已审，结论折入本稿（见文末「评审折入记录」）

## 1. 目标

在已有的运行态画布上补四项能力，并配一个**视图内设置面板**控制它们：

1. **数据流向动画** —— 执行前沿的边走流动动画，直观看到「现在进行到哪一步」。
2. **节点耗时（实时降级）** —— 实时观看 run 时，节点显示运行耗时，running 节点跳秒。
   **已完成 / 历史 run 不显耗时**（见 §3 约束与 §7 降级）。
3. **自动布局 + 导航** —— 运行态可选自动分层布局，配合 MiniMap / fitView / 平移缩放。
4. **状态聚焦 / 高亮** —— 一键聚焦失败或进行中、隐藏已完成；失败节点高亮，点节点看详情。

设置以**视图内面板**（画布右上齿轮）呈现，改动实时生效，偏好存 `localStorage`。

## 2. 非目标（YAGNI 边界）

- **不改后端 / 不加 DB 列 / 不做迁移。** 计时纯客户端，从 SSE 事件累积。
  （已知代价见 §3：已完成 run 无耗时。这是用户明确选择的取舍。）
- **设置不入库、无独立路由页。** 视图偏好存 `localStorage`。
- **不动 `GraphView`**（只读 CSS 分层 DAG）。本次只做 `RunCanvas`。
- **不引 `dagre` / `elk`**。自动布局复用现成 `seedPositions` / `layerize`。
- 不做跨工作流的拓扑总览（另一个独立需求）。

## 3. 关键约束与既有事实

来自对现有代码的核实（含评审 agent 二次证伪），设计据此成立：

- **SSE 事件不带时间戳。** `todo_started` payload 仅 `{type}`、`todo_finished` 仅
  `{type, outputRef}`（`internal/worker/worker.go:300,398`）；SSE 帧形状
  `{seq, kind, todoId, payload}`（`internal/httpapi/sse.go:91`、`web/src/lib/types.ts:169`），
  无 `ts`。⇒ 前端只能用**帧到达的客户端 wall-clock**算耗时。
- **已完成 / 终止态 run 只批量回放、不开实时流**（`useProductionTimeline.ts:139`）。
  `fetchAllEvents` 一次性 resolve 后批量 dispatch（`useProductionTimeline.ts:130`），
  全部 `todo_started`/`todo_finished` 同一 tick 到达 ⇒ **回放算出的耗时一律 ≈0，是垃圾值**。
  因此**回放帧绝不用于计时**，耗时仅对实时帧成立（§4 计时规则）。
  > 注：`run_events` 表其实有 `ts` 列（`internal/events/store.go:129` 已在用），暴露它
  > 即可让历史 run 也准确。本期按「零后端」决策**不做**，作为后续可选增强记录在案。
- `GraphNode`（`web/src/lib/projectState.ts:44`）无计时字段；`Todo` 只有 `created_at`/`updated_at`，
  无干净 `started_at`，且 run 期 `updated_at` 漂移（`internal/projectstate/state.go:419`）。
- 运行态节点 id 是全新 todo UUID，与画布节点 id 无持久回链；靠 `(type, 拓扑序序号)` 结构映射
  （`runOverlay.ts`）。`RunNodeStatus.todoId` 在所有 overlay 命中节点上必有值
  （`runOverlay.ts:65`）—— 计时回链画布节点的钥匙。未命中节点被省略、无 todoId（→ 无徽标）。
- `useProductionTimeline` 是**唯一 SSE 连接持有者**（`RunCanvas.tsx:152`），逐帧经其
  回调（`useProductionTimeline.ts:151`）。计时从这里接出口，**不另开连接**。
- **重连从 `after=0` 全量回放**（`useProductionTimeline.ts:9`）。仍 running 的节点
  `todo_started` 会再次到达 ⇒ 计时累积必须幂等（§4）。
- `seedPositions`（`canvasModel.ts:41`）已是一份 TB 自动布局（`layerize` + `{x:j*240,y:i*140}`）。
  autoLayout 应复用它、仅泛化 LR，**不另造第三份分层逻辑**。
- 边方向：`toReactFlow` 建 `{source: dep, target: n.id}`（`canvasModel.ts:68`），dep 是上游。
  「执行前沿」= 源 done 且 目标 running，方向正确。但 `StudioEdge` 当前**不读 `data`**
  （`StudioEdge.tsx:14`）、`toReactFlow` 边**无 `data`** ⇒ 需 RunCanvas 注入 `edge.data.active`。
- `web/src/components/ui/` **无 `popover.tsx`**（仅 `sheet.tsx`）；`radix-ui ^1.5.0` 已在
  `package.json`，可加 Popover 包装层，无新依赖。
- 三主题 token 齐全（`web/src/index.css`）；**禁硬编码颜色**。无 dim token，opacity 是
  既定降透明做法（`WorkflowCanvas.tsx:922` 等）。`amber` 是 running 语义色，耗时数字**不得占用**。

## 4. 架构与数据流

```
SSE 帧 ─┬─► useNodeTiming   (仅实时帧 → todoId → {startedAt, finishedAt?, elapsedMs})
        └─► onState ────────► ProjectState   (状态权威源，不变)

overlayRunStatus(nodes, state) ─► 画布节点id → RunNodeStatus(.todoId, .status, ...)
计时 join:  nodeId ──(RunNodeStatus.todoId)──► 耗时（仅实时观测到的节点有值）

useTopologySettings ─► { layout(每项目), showTiming, flowAnimation, focus, hideCompleted, fitOnUpdate }

RunCanvas 组装:
  坐标   = layout==="saved" ? 自存position : autoLayout(WorkflowNode[], dependsOn, dir)
  rfNodes= toReactFlow + data.run(overlay) + data.timing(showTiming 且有实时耗时)
           + 按 focus/hideCompleted 标 hidden / dimmed（状态取 overlay.status ?? "pending"）
  rfEdges= toReactFlow + data.active(源done&目标running) + 端点 hidden→边也 hidden / dim
  fitView= layout 变更且 fitOnUpdate 时，useReactFlow().fitView() 命令式（坐标 commit 后）
```

**计时规则（核心，解决「回放假耗时」+「重连回跳」）：**
- `startedAt[node]` = 该节点**首个实时（非回放）** `todo_started` 帧的客户端到达时刻。
  **回放帧永不写 startedAt**；首个实时 started 落定后不被后续同类帧覆盖（幂等）。
- `finishedAt[node]` = 实时 `todo_finished` 到达时刻。`elapsedMs` = finished−started（done）
  或 now−started（running，跳秒）。
- 无实时 `startedAt` ⇒ 无耗时徽标（覆盖全部历史 run / 回放 / 刷新前已完成的节点，诚实降级）。
- 路线：给 `useProductionTimeline` 加 `onEvent(frame, {isReplay})` 出口（路线 a，单连接）;
  `useNodeTiming` 据 `isReplay` 决定是否记 startedAt。`planId` 变更 / 卸载时清累积 Map +
  清 interval；跳秒 interval 仅在「有实时 running 节点」时存活，terminal run 一律不跳秒。

设计原则：`ProjectState` 仍是状态唯一权威源（keystone）。计时是 ephemeral 叠加，不写回。

## 5. 组件清单

### 新增

| 文件 | 职责 |
|---|---|
| `web/src/components/ui/popover.tsx` | shadcn/radix Popover 包装层，对齐 `sheet.tsx` 的 `data-slot` + token 风格。**交付物**（仓内此前没有）。 |
| `web/src/lib/autoLayout.ts` | 泛化 `seedPositions`：`autoLayout(nodes: WorkflowNode[], direction)` → `Map<id,{x,y}>`。复用 `layerize`（边由 `dependsOn` 建 `GraphEdge{from:id,to:dep}`）；TB 等价现有 seedPositions，LR 交换主/交叉轴。`seedPositions` 重构为调用 `autoLayout(_, "TB")`，避免两份分层漂移。 |
| `web/src/features/workflow-canvas/useTopologySettings.ts` | localStorage 持久化偏好。**`layout` 按项目键** `studio.topology.layout.<projectId>`；其余偏好全局键 `studio.topology.settings`。zod schema + 默认值 + `safeParse` 坏数据回落。 |
| `web/src/features/workflow-canvas/TopologySettingsPanel.tsx` | 齿轮按钮（`aria-label="视图设置"`+`title`）→ Popover。控件用既有 `label`/`checkbox`/`select`，实时生效。落点**并入运行控制簇** `RunCanvas.tsx:377` 的 `right-[280px] top-[56px]` 浮层，避免与右栏/MiniMap/Controls 撞位。 |
| `web/src/features/workflow/useNodeTiming.ts` | 据 §4 计时规则从 `onEvent(isReplay)` 累积 `todoId → 耗时`；running 跳秒（轻量 interval，仅有实时 running 时启用，planId 变更/卸载清理）。 |

### 改动

| 文件 | 改动 |
|---|---|
| `useProductionTimeline.ts` | 新增 `onEvent(frame, {isReplay})` 出口（透传逐帧 + 回放/实时标志），不另开连接。 |
| `StudioEdge.tsx` | 读 `data.active`；为真时渲染流动虚线动画（CSS keyframes，`motion-safe`，stroke 用 `var(--amber)`，禁硬编码）。无 active 同现状。 |
| `WorkflowNode.tsx` | `data.timing` 且 `showTiming` 时渲染耗时 chip：节点右上角**绝对定位**小 chip（不挤压现有两行文案），`font-mono tabular-nums` 固定宽防跳动，色用 `text-2` + `bg-bg-raised`，**容器不带 `aria-live`**。`focus==="failed"` 命中失败时加 `ring-2 ring-danger`。 |
| `RunCanvas.tsx` | 串接：接 `useTopologySettings`+`useNodeTiming`；坐标按 layout 选自存/autoLayout；按 focus/hideCompleted 标 hidden/dimmed（含悬挂边）；逐边算 `active` 注入 `edge.data`；挂齿轮面板；layout 变更且 fitOnUpdate 时命令式 fitView；新增「全被过滤」空态（见 §7）。 |

## 6. 设置项（面板内，实时生效）

| 设置 | 类型 | 默认 | 持久化 | 效果 |
|---|---|---|---|---|
| 布局 `layout` | `"saved"\|"TB"\|"LR"` | `"saved"` | **每项目** | 自存坐标 / 自动竖向 / 自动横向 |
| 布局更新 fitView `fitOnUpdate` | bool | `true` | 全局 | 布局/方向变更时命令式 fitView |
| 显示耗时 `showTiming` | bool | `false` | 全局 | 节点耗时 chip 开关（仅实时观测到的节点有值） |
| 数据流动画 `flowAnimation` | bool | `true` | 全局 | 执行前沿边动画。历史 run 无 running 节点 → 无 active 边 → 不动画（对历史 run 零视觉影响，motion-safe 守护） |
| 聚焦 `focus` | `"none"\|"failed"\|"running"` | `"none"` | 全局 | 聚焦失败（非 failed 淡化、failed 红环）/ 聚焦进行中（running 高亮、余淡化）/ 不聚焦 |
| 隐藏已完成 `hideCompleted` | bool | `false` | 全局 | done 节点隐藏（含其悬挂边） |

> **简化说明**：原稿 5 状态×3 态=15 控制点过度。收敛为「聚焦下拉 + 隐藏已完成开关」覆盖
> 真实高频诉求（看到哪了 / 只看失败 / 降噪）。`highlightFailed` 并入 `focus="failed"`。
> 若日后确需逐状态精控，作为 Popover 内「高级」折叠（radix Collapsible）追加，本期不做。

**零回归**：`layout="saved"`+`showTiming=false`+`focus="none"`+`hideCompleted=false` 时，
rfNodes 坐标/data 与现状逐项一致；`flowAnimation=true` 对**历史 run 无视觉影响**（无 active 边），
仅在**实时 run**新增 motion-safe 流动动画（评审建议的可发现性默认，非破坏性）。

## 7. 错误处理与降级

- **计时降级（核心）**：无实时 startedAt 的节点不显 chip —— 覆盖全部历史 run、回放节点、
  刷新前已完成节点。**不显示 0、不显示错误值**。文档与 UI tooltip 注明「耗时仅实时观看时可见」。
- `localStorage` 解析失败 / schema 漂移 → zod `safeParse` 回落默认值，不抛。
- **「全被过滤」空态**：`hidden` 属性不改 `rfNodes.length`，现有 `rfNodes.length===0`
  空态（`RunCanvas.tsx:330`）不触发。新增判定「可见节点数=0 且总数>0」→ 专属文案
  （如「当前过滤隐藏了所有节点」+ 一键清除过滤）。
- **悬挂边**：源/目标任一 hidden 的边一并置 hidden；dim 节点的入/出边同步降透明，保持「淡化」连贯。
- `autoLayout` 遇环 → `layerize` 已有环防御。未知/未命中 status → 取 `?? "pending"`，
  过滤口径与 `WorkflowNode` 渲染回落一致（`WorkflowNode.tsx:33`）。
- **v2 / 自定义节点**：`(type,序号)` 结构映射在「画布定义与 run 同构」假设下成立
  （`runOverlay.ts:6`）。定义漂移时可能错配 → 保持「未命中即省略」的保守渲染，错配仅影响
  视觉叠加、不影响数据；spec 注明此限制。

## 8. 测试

- `autoLayout`：层→坐标、TB 等价旧 `seedPositions`（快照不变）、LR 交换轴、层内居中、空/单节点、带环。
- `useTopologySettings`：默认值、layout 每项目隔离（项目 A 改不影响 B）、全局偏好跨项目共享、
  坏 JSON / 缺字段回落、layout 与全局键互不污染。
- `useNodeTiming`（重点）：
  - **回放帧不计时**（批量 isReplay 帧 → 节点无 startedAt → 无耗时）。
  - **重连全量回放幂等**（实时 startedAt 落定后，重连回放的同 todoId started 不覆盖、不回跳）。
  - 实时 started→finished 算 elapsedMs；running now−started 跳秒。
  - planId 变更 → 累积 Map 重置、旧 interval 清理；卸载 → 无泄漏。
  - 乱序 / 只有 finished 无 started → 无耗时，不抛。
- 边动画：`active` 仅在 源done&目标running 时为真（方向不反）；StudioEdge 读 `data.active`。
- 过滤：`focus`/`hideCompleted` 的 hidden/dim 标记；悬挂边联动 hidden；「全被过滤」空态触发 + 文案。
- 契约：`focus` 与 `statusFilter` 口径覆盖全部 `GraphNodeStatus`（blocked|pending|running|done|failed）。
- 回归：默认设置下 rfNodes 坐标==自存 position、历史 run 无 active 边、无耗时 chip。
- a11y：齿轮 `aria-label`；耗时 chip 容器无 `aria-live`；面板键盘可达。

## 9. 执行方式

设计定稿 → 本 spec 入库 → （可选）codex 外部二审 → `writing-plans` 出实现计划 →
派 subagent 实现（一次一个、任务间审查）。遵循本仓 PR 纪律：分支 → push → PR → rebase 合，
不直推 main。本设计在分支 `feat/runtime-topology-viz` 上。

---

## 评审折入记录（2026-06-29，3 agent）

- **决策（用户）**：节点耗时坚持零后端 → 降级为「仅实时观看可见」，历史 run 不显（不显 0）。
- **must-fix 已折入**：① 回放帧不计时 + 重连幂等（§4 计时规则、§8）；② 全被过滤空态 + 悬挂边
  （§7）；③ 新增 `popover.tsx` 交付物（§5）；④ 齿轮 `aria-label` + 落点并入控制簇（§5）。
- **should-fix 已折入**：状态过滤简化为聚焦下拉+隐藏已完成（§6）；autoLayout 复用/重构
  seedPositions（§5）；边 `data.active` 注入 + stroke 用 amber（§3/§5）；fitOnUpdate 命令式
  （§4/§5）；耗时 chip 防抖动+不占 amber+无 aria-live（§5）；interval 生命周期（§4）；
  layout 每项目持久化（§5/§6）。
- **nit 已注明**：undefined status→pending 口径（§7）；v2/custom 漂移保守渲染（§7）；
  flowAnimation 实时默认 ON 的可发现性取舍（§6）。
