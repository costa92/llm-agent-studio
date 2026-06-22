# 工作流可视画布编辑器 实现计划

> **执行：** subagent-driven，逐 Phase 执行、Phase 间复审。Spec 见 `docs/superpowers/specs/2026-06-22-workflow-canvas-editor-design.md`。
> **后端零改动**：position 是节点 JSON 里附加字段，`internal/workflows/store.go` 原样透传、planner 忽略未知字段（已核验）。若某任务似乎要改后端 → 停，说明转换器漏了字段。
> **验证只打用户那一个 :5173**，勿起第二个 Vite（并行 Vite 陈旧陷阱）。HMR 看着陈旧就重启现有 :5173。

## Phase 1 — 脚手架（只读画布）
交付：可导航、已主题化、只读渲染某工作流 DAG。

- **T1.1 加依赖**：`cd web && npm install @xyflow/react@^12`；`npm ls @xyflow/react` 解析；`npm run build` 仍绿。
- **T1.2 纯适配器** `web/src/features/workflow-canvas/canvasModel.ts`：`toReactFlow(nodes)→{nodes,edges}`（position ?? seeded）、`seedPositions(nodes)`（建 `GraphEdge{from:n.id,to:dep}`→`layerize`→按层/层内排）、边 id `\`${source}->${target}\``。测 `canvasModel.test.ts`：3 节点 script→storyboard→asset ⇒ RF 边 source=dep/target=node；无 position 的节点得 seed 分层坐标；有 position 的保留。
- **T1.3 自定义节点 + 主题 css**：`WorkflowNode.tsx`（`NODE_COLOR` 类型色、id+类型标签、target/source handle、无状态样式）、`canvasTheme.css`（`.workflow-canvas` 作用域 `--xy-*` 覆盖）。测 `WorkflowNode.test.tsx`：渲染标签+类型、应用类型色 var。
- **T1.4 画布壳（只读）** `WorkflowCanvas.tsx`：`ReactFlowProvider`→三栏；中 `<ReactFlow nodes edges nodeTypes={{studio}} fitView>`+`Background/Controls/MiniMap`；palette/properties 占位。引入两个 css。无 handler。
- **T1.5 路由 + 取数** `orgs.$org.projects.$id.workflow.tsx`：取 `org`/`id` param + `wf` search；`useWorkflows(id)` 按 `wf` 找；喂 `wf.nodes` 给画布；loading/error（Skeleton/重试，对齐 index）。`tsr generate`。测 `workflow-route.test.tsx`（仿 `workbench-route.test.tsx` harness）：挂 `/orgs/acme/projects/p1/workflow?wf=w1` + `installFetchRoutes` 返 fixture；断言节点(按 id 标签)渲染、存在一条边、容器应用 `--xy-background-color` token。
- **T1.6 入口** 改 `orgs.$org.projects.$id.index.tsx`：每行加「编辑工作流」→ `navigate({to:".../workflow", params, search:{wf:wf.id}})`（暂留旧弹框，Phase 3 移）。
- **Phase 1 验证**：test 绿、build 绿；:5173 打开路由 — DAG 渲染、边正确、平移/缩放/minimap/控件可用、三主题正确（只读）。

## Phase 2 — 编辑 + 持久化
交付：可完整编辑并保存（含位置）。

- **T2.1 类型扩展** `web/src/lib/types.ts`：`WorkflowNode` 加 `position?:{x:number;y:number}`（后端不变）。`tsc -b` 绿。
- **T2.2 受控态 + dirty + 保存**：`useNodesState`/`useEdgesState` 由载入 model 种子；`toStudioNodes(rfNodes,rfEdges)`（dependsOn 由入边、position 由 rfNode、空 promptText 落 undefined）；保存调 `useUpdateWorkflow`/`useCreateWorkflow` `{name,nodes}`；dirty=JSON diff；保存 400 解析 toast（复用 index 文案剥离）。测：`toStudioNodes` 双向 dependsOn + **payload 含 position**。
- **T2.3 拖入加节点**：`NodePalette.tsx` 可拖芯片；画布 `onDragOver`/`onDrop`+`screenToFlowPosition`；新节点唯一 id + `promptId=defaultPromptIdFor(type)`。测：「在某位置加节点」reducer 单测（避开 DnD flaky）。
- **T2.4 连/断边 + 环守卫**：`onConnect` 建候选 model 跑 `findGraphError`（import 自 `WorkflowDialog.schema.ts`）；null⇒加边+`target.dependsOn+=source`；非空⇒`toast.error`+abort。`onEdgesDelete` 移除 dep。测：A→B 置 `B.dependsOn=[A]`；已存 A→B 再连 B→A 被拒（不加边）；断开移除 dep。
- **T2.5 属性面板** `PropertiesPanel.tsx`：移植 `WorkflowNodesEditor` 的逐节点控件（id 输入 **改名级联** dependsOn + RF 节点/边 re-key；类型 Select 重置 promptId；提示词选择器 `__default__`/`__custom__`/`__create__` 含 `useCreatePrompt`；promptText 文本框；删除节点 + dependsOn 级联 + 移边）。无选中=空态。`onSelectionChange`→选中 id。测：改名级联、删除级联、提示词 sentinel。
- **T2.6 自由拖拽存位置**：确认 `onNodesChange` 保位置入 RF 态并流入 `toStudioNodes`。测：移动后 payload 带新坐标。
- **Phase 2 验证**：test/build 绿；:5173 — 拖入、连边(+环被 toast 拒)、改提示词含行内新建、改名(依赖跟随)、删节点(依赖清理)、拖动、保存→重载→图+位置持久化。

## Phase 3 — 打磨 + 对齐
交付：生产级对齐，移除旧表单路径。

- **T3.1 标准管线**：palette 按钮种 script-1→storyboard-1（复用两节点形状 + `defaultPromptIdFor`），seed 排版，标 dirty。测：产出 2 节点 + storyboard 依赖 script。
- **T3.2 状态对齐**：空(新建空画布+提示)/loading(Skeleton)/error(重试)，对齐 CRUD 基线。
- **T3.3 键盘**：Delete/Backspace 删选中节点(dependsOn 级联)或边(移 dep)，走 ReactFlow `deleteKeyCode`+`onNodesDelete`/`onEdgesDelete` 复用级联。测：删除键删节点并清依赖。
- **T3.4 移除旧表单**：index 路由「新建/编辑」改跳 `/workflow`；移除内嵌节点编辑器。`WorkflowDialog`/`WorkflowNodesEditor` 去留：若仍要仅命名快建，留瘦命名弹框；否则删 `WorkflowNodesEditor.tsx`+其测、精简 `WorkflowDialog`。改/迁相关测试。`tsc -b` 无悬空 import。
- **T3.5 全量 + 浏览器矩阵**：全量 vitest + `npm run build` 绿；**单个** :5173 上三主题验证拖/连(+环拒 toast)/行内建提示词/改名级联/删除级联/拖动/保存/重载持久化/minimap·缩放·控件/无硬编码色。

每 Phase 交付可独立复审软件：P1=可导航只读 DAG；P2=可编辑可持久化；P3=对齐并移除旧表单。
