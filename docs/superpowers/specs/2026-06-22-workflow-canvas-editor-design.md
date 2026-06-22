# 工作流可视画布编辑器 设计（Workflow Canvas Editor）

> 参考 beequant 工作流编辑器，用 @xyflow/react (ReactFlow v12) 把现有「勾选式表单」工作流编辑升级为拖拽式可视 DAG 画布。

## 目标
在独立全屏路由上，用拖拽画布替换 `web/src/features/projects/WorkflowNodesEditor.tsx`（勾选表单）编辑工作流 DAG，并持久化节点位置，同时**保留旧编辑器全部能力**：节点类型、提示词选择器（含行内新建 `__create__` / 自定义 `__custom__`）、节点 id 改名级联、删除级联、dependsOn、标准管线、环检测。

## 锁定决策
1. 全拖拽画布编辑器（@xyflow/react v12），**替换**勾选表单。
2. **持久化 x/y**。后端 `Nodes` 为 JSON 列（`internal/workflows/store.go` 原样透传；planner 反序列化时忽略未知字段）→ position 仅是节点 JSON 里多出的字段，**后端零改动**（已核验 create/update/get 往返 + planner 忽略）。
3. 第三方库 `@xyflow/react`（web 无 stdlib-only 约束）。
4. 独立全屏路由 `/orgs/$org/projects/$id/workflow?wf=<id>`（`wf` 缺省=新建）。三栏：左节点库 / 中画布 / 右属性。

## 架构
- 路由文件 `web/src/routes/_authed/orgs.$org.projects.$id.workflow.tsx`。
- 特性目录 `web/src/features/workflow-canvas/`：`WorkflowCanvas.tsx`（`ReactFlowProvider`+三栏编排）、`NodePalette.tsx`、`PropertiesPanel.tsx`、`WorkflowNode.tsx`（自定义节点）、`canvasModel.ts`（纯 model↔ReactFlow 适配器）、`canvasTheme.css`（`--xy-*` token 覆盖）。
- 数据：复用 `workflowApi.ts`（`useWorkflows` 取、`useCreateWorkflow`/`useUpdateWorkflow` 存）、`usePrompts`/`useBasicPrompts`/`useCreatePrompt`。无新 API。
- 编辑态：ReactFlow `useNodesState`/`useEdgesState`（受控）为实时编辑源，仅在保存时转回 `WorkflowNode[]`。dirty = 与载入快照 JSON 对比。
- 后端：不变。

## 数据绑定契约（双向）
**model → ReactFlow（载入/seed）**：每个 `WorkflowNode n`：
- `rfNode = { id: n.id, type: "studio", position: n.position ?? seeded(n.id), data: { node: n } }`（自定义节点把整个 studio node 放 `data`）。RF 节点 id ≡ studio `node.id`（1:1）。
- 边：对每个 `n` 的每个 `dep ∈ n.dependsOn` 发 `rfEdge = { id: \`${dep}->${n.id}\`, source: dep, target: n.id }`。**source=上游依赖，target=被依赖节点 ⟹「B.dependsOn 含 A」中 A=source、B=target**（上→下，同 GraphView）。

**ReactFlow → model（保存）**：每个 `rfNode`：
- `dependsOn = edges.filter(e => e.target === rfNode.id).map(e => e.source)`。
- `{ id, type, promptId, promptText?, dependsOn, position: round(rfNode.position) }`；空 `promptText` 落 `undefined`（对齐既有 payload）。

**连/断边**：`onConnect({source,target})` ⟹ `target.dependsOn += source`（先建候选 model 跑 `findGraphError`，非空则 toast 拒绝、abort）。`onEdgesDelete`/删除键 ⟹ 从 `target.dependsOn` 移除 `source`。

**节点 id 与改名**：属性面板保留 id 可编辑（dependsOn 基于 id）。改名时：更新该节点 id + 级联改所有其他节点 `dependsOn` + 重建 RF 节点并 re-key 相关边（RF 节点 id 不可原地改）。可见标签即 id。拒绝重复/空 id（同 schema 文案）。

**位置 seed**：载入时若**所有**节点都有 position 用之；否则用 `layerize`（复用，不加新 dep）分层后按 `x=层内序×X_GAP, y=层序×Y_GAP` 排（注意 `layerize` 吃 `GraphEdge{from,to}`，from-依赖-to，即 `{from:n.id,to:dep}`，与 RF 边相反，仅 seed 用临时数组）。首存即落库。

**环检测**：每次加边走候选 model + `findGraphError`（复用 `WorkflowDialog.schema.ts`，已镜像后端 `ValidateCustomGraph`）。保存以后端 400 为兜底（解析消息 toast，同 index 路由）。

## ReactFlow v12 要点
- `@xyflow/react@^12`；`import "@xyflow/react/dist/style.css"` 一次 + `canvasTheme.css` 在 `.workflow-canvas` 容器覆盖 `--xy-*`（全部指向 studio token，**无硬编码色**）：`--xy-background-color→var(--bg-base)`、`--xy-node-background-color→var(--bg-surface)`、`--xy-node-border→1px solid var(--line)`、`--xy-node-color→var(--text-1)`、`--xy-edge-stroke[-default]→var(--line)`、`--xy-edge-stroke-selected→var(--amber)`、`--xy-connectionline-stroke→var(--amber)`、`--xy-controls-button-background-color[-hover]→var(--bg-surface)/var(--bg-raised)`、`--xy-controls-button-color→var(--text-2)`、`--xy-minimap-background-color→var(--bg-surface)`、`--xy-attribution-background-color→transparent`。
- 自定义节点 `nodeTypes={{ studio }}`：圆角卡片，左边色条/点用 `NODE_COLOR[type]`（复用 GraphView 的 map：script→--script、storyboard→--board、asset→--asset）；显示 id+中文类型；`Handle` target=Top、source=Bottom；**编辑视图无运行状态样式**。
- 画布：`<ReactFlow>` + `<Background/><Controls/><MiniMap nodeColor=…/>` + `fitView`；外包 `<ReactFlowProvider>`（`screenToFlowPosition` 需要）。
- 拖入：palette 项 `draggable` 设 `dataTransfer`；画布 `onDragOver`(preventDefault)+`onDrop`→`screenToFlowPosition`→在落点加新节点（唯一 id `node-${n+1}` 查重，默认类型来自拖拽项，`promptId=defaultPromptIdFor(type)`）。

## 布局
全屏三栏 flex：左 `NodePalette`(~200px，可拖 script/storyboard/asset 芯片 + 标准管线按钮) / 中画布(flex-1) / 右 `PropertiesPanel`(~320px，选中节点编辑或空提示)。顶栏：工作流名输入 + 返回 + 保存(非 dirty 禁用) + dirty 指示。

## 替换 / 入口
- 画布**替换** `WorkflowNodesEditor` 作为 DAG 编辑器。项目 index 路由工作流行加「编辑工作流」→ `/workflow?wf=<id>`；「新建工作流」→ 无 `wf`。Phase 3 移除弹框内嵌节点编辑器（如仍需仅命名的快建，留个瘦命名弹框）。

## 非目标（v1）
不加新节点类型（仅 script/storyboard/asset）；画布不叠加运行状态（仅编辑视图）；无实时协作；无 undo/redo；无边标签/条件；除初始 seed 外无自动排版。

## 风险
1. 保存丢 position（转换器漏字段）→ 给 `WorkflowNode` 加 `position?` + 转换器单测断言 payload 含 position。
2. id 改名 ↔ RF 节点 id 不可变 → 改名=重建节点+re-key 边，专项测试。
3. 前后端环检测分叉 → 复用 `findGraphError`。
4. 并行 Vite 陈旧 → 只对用户那个 :5173 验证，勿起第二个。
5. `screenToFlowPosition` 需 `ReactFlowProvider` 祖先，否则拖入静默失败 → 手测覆盖。
