# 工作流画布交互增强 实现计划

> subagent-driven，逐 Phase 执行、Phase 间复审。Spec 见 `docs/superpowers/specs/2026-06-22-workflow-canvas-interactions-design.md`。
> **顺序决策：undo/redo 放最前（Phase A）**——后续每个交互诞生即接 `takeSnapshot`，避免事后往 ~13 个 handler 回填漏调。无后端改动；pnpm；只动 `web/src/features/workflow-canvas/*`。
> 不变量：EDGES 是 dependsOn 真源 / 环检测复用 `findGraphError` / amber token / 边 id `${source}->${target}` / dirty 从 toStudioNodes 推。

## Phase A — 历史基座 + 吸附/整理
- **A1 `useUndoRedo.ts`**（new）：`useReactFlow` 取 get/set Nodes/Edges；`past/future` ref + canUndo/canRedo；`takeSnapshot`（推当前、清 future、capMaxHistory）/`undo`/`redo`。测 `useUndoRedo.test.tsx`：snapshot→mutate→undo 还原、redo 重放、snapshot 后清 future、cap 丢旧。
- **A2 接线 + 快捷键 + 按钮**（WorkflowCanvas）：`takeSnapshot()` 作为 onConnect/onDrop/deleteSelected/renameSelected/patchSelected/onStandardPipeline 第一行；`onNodeDragStart={()=>takeSnapshot()}`；删除走 `onBeforeDelete={async()=>{takeSnapshot();return true}}`。document keydown：Ctrl/Cmd+Z=undo、+Shift+Z 或 Ctrl+Y=redo（在 input/textarea 内跳过）。顶栏加 撤销/重做 图标按钮 disabled 绑 canUndo/canRedo。测：rename 后 canUndo 真；深拖路径手验。
- **A3 自动整理**：NodePalette 加 `onAutoTidy` 按钮；handler `takeSnapshot`→`seedPositions(toStudioNodes(...))`→`setRfNodes` 套用 position→`setTimeout(fitView,0)`。
- **A4 吸附**：`const GRID=16`；`<ReactFlow snapToGrid snapGrid={[16,16]}>`；`<Background gap={16}>`。
- **A 验证(:5173)**：加/移/连/删后 Ctrl+Z 逐步回退（位置一步）、Ctrl+Shift+Z 重做、回到载入态 Save 变灰；自动整理后 fitView、Ctrl+Z 复原；拖动按 16px 吸附。

## Phase B — 连线建图
- **B1 `NodeTypePicker.tsx`**（new）：受控浮层（fixed left/top 屏幕坐标）列 3 类型 chip（复用 NODE_COLOR/TYPE_LABEL），`bg-bg-raised border-line`，点外关闭。测 `NodeTypePicker.test.tsx`：点剧本→onPick("script")、点遮罩→onClose。
- **B2 拖到空白建+连**（WorkflowCanvas）：`onConnectStart` 记 `{nodeId,handleType}`；`onConnectEnd(e,connectionState)` 若 `connectionState.toNode==null` 且 source handle → `screenToFlowPosition` 算落点、弹 picker；选中：`takeSnapshot`→`addNodeAt`→读新 id（B2a `nextNodeId` 纯 helper）→guard→`addEdge({id:`${source}->${newId}`,source,target:newId})`。测 `nextNodeId` 纯单测；建+连手验。
- **B3 NodeToolbar 删/复制**：`WorkflowNode` 内 `<NodeToolbar isVisible={selected} position={Top}>` 删除(text-danger)/复制；handler 经新建 `CanvasActionsContext`（CanvasInner 提供，避免污染纯 `toReactFlow`）；`duplicateNode(rfNodes,id,prompts)` 纯 helper（clone data.node、nextNodeId、+40/+40、dependsOn 清空、原节点不动）。测 `duplicateNode` 单测；toolbar 可见手验。
- **B4 `StudioEdge.tsx`**（new）：`BaseEdge`+`EdgeLabelRenderer`（`pointerEvents:all` + `nodrag nopan`），中点常显淡「+」、hover 加「删除」；`edgeTypes={{studio:StudioEdge}}`+`defaultEdgeOptions={{type:"studio"}}`，并让 `toReactFlow`/onConnect/insert 建的边带 `type:"studio"`；`insertNodeOnEdge(...)` 纯 helper（A→B 拆 A→N、N→B，N 在中点）。测 `insertNodeOnEdge` 单测；hover 手验。
- **B 验证**：拖空白→picker→建+连；选中节点→toolbar 删/复制（复制无连线、新 id）；边 hover→删除/「+」插入分裂；均单步 undo。

## Phase C — 多选/剪贴板/对齐线
- **C1 框选配置**：`selectionOnDrag`、`panOnDrag={[1,2]}`、`selectionMode={Partial}`；多移/批删（onBeforeDelete 已快照）；onSelectionChange 多选时 selectedId=null（属性面板空）。手验框选+整体移动+批删。
- **C2 剪贴板 helper+快捷键**：`cloneSelection(rfNodes,rfEdges,selectedIds,offset,prompts)` 纯 helper（先建全量 oldId→newId、clone 节点+offset、只留两端都在选中集的边并 remap、丢外部边）。clipboard ref；keydown（input 内跳过）Ctrl/Cmd+C 存选区、V `takeSnapshot`+粘贴(选中粘贴集)、D 原地复制。测 `cloneSelection` 单测（内部边保留 remap、外部边丢、offset、toStudioNodes 依赖随新 id）；键盘手验。
- **C3 对齐辅助线（独立、最后）**：`getHelperLines(dragged,nodes,threshold)` 纯几何 helper + `HelperLines.tsx` 覆盖层（`useStore` 读 transform 定位，stroke `var(--amber)`）；`onNodeDrag` 算线+吸附、`onNodeDragStop` 清；**不在 onNodeDrag 里 takeSnapshot**。测 `getHelperLines` 纯单测；覆盖层手验。

## 跨阶段
纯 helper 承担测试重量（`pnpm vitest run src/features/workflow-canvas`）；DnD/键盘/框选/hover 仅 :5173 手验。每阶段回归既有流 + 末尾 `pnpm test`/`pnpm build` 全绿 + 三主题手验。验证只打用户那一个 :5173（勿起第二 Vite）。
