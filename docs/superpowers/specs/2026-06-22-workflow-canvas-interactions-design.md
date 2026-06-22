# 工作流画布交互增强 设计（Canvas Interactions）

> 在已合入的 ReactFlow 工作流画布上加 4 组交互（参考 beequant/n8n 类编辑器）。全部限于 `web/src/features/workflow-canvas/*`，无后端改动，pnpm。

## 不变量（必须保持）
1. **EDGES 是 dependsOn 唯一真源**：新交互建/删依赖只改 `rfEdges`，绝不写 `data.node.dependsOn`；`toStudioNodes` 存盘时从边推。
2. **环检测复用 `findGraphError`**：每条建边路径（connect / 拖空白连 / 边插入分裂）先建候选 `toStudioNodes(nodes,[...edges,newEdge])` 跑它，非空 toast 拒绝。
3. **amber 三主题 token only**，无硬编码色；画布 chrome 经 `.workflow-canvas` 的 `--xy-*` 映射。
4. **边 id 约定** `` `${source}->${target}` ``；插入/粘贴 remap 须重生成 id。
5. **dirty/Save 不变**：`dirty = 当前 toStudioNodes 快照 ≠ loadedSnapshot`；undo/redo 还原 `{nodes,edges}` 后 dirty 自然重算。

## 4 组交互
1. **从 handle 拖到空白 → 节点选择器**：`onConnectStart` 记 source；`onConnectEnd` 用 v12 `connectionState.toNode==null` 判空白 → 光标处弹 `NodeTypePicker` → 选中后 `addNodeAt` 建节点 + 连 source→新（带 guard）。
2. **节点/边快捷操作**：节点选中显 `<NodeToolbar>`（删除/复制）；自定义 `StudioEdge`（`BaseEdge`+`EdgeLabelRenderer`）中点悬浮「删除」+「+」插入分裂边（A→B 拆成 A→N→B）。
3. **多选 + 剪贴板**：`selectionOnDrag`+`panOnDrag={[1,2]}` 框选/中右键平移；多选整体移动 + 批量删；Ctrl/Cmd+C/V/D 复制/粘贴/复制（fresh id + remap 内部边，丢到外部的边）。
4. **撤销/重做 + 自动整理 + 吸附**：`useUndoRedo`（`{nodes,edges}` 快照栈，`takeSnapshot()` 在每次变更前调）；自动整理按钮（复用 `seedPositions`）；`snapToGrid`；对齐辅助线（helper lines，独立任务）。

## 跨切面：undo/redo
`useUndoRedo` 持 `past/future: Snapshot[]`（`Snapshot={nodes,edges}` 浅拷贝即可，节点是整体替换非原地改），暴露 `takeSnapshot/undo/redo/canUndo/canRedo`，`maxHistory≈100`。**位置变更靠 `onNodeDragStart` 一次快照=一步**，绝不在 `onNodeDrag` tick 里快照；纯选中变更不快照。删除用 v12 `onBeforeDelete`（在应用前快照）。

## 测试策略
逻辑全压进 `canvasModel.ts`/`useUndoRedo.ts` 纯 helper（`nextNodeId`/`duplicateNode`/`insertNodeOnEdge`/`cloneSelection`/`getHelperLines`/`useUndoRedo`）做单测；DnD/键盘/框选/hover 在 :5173 手验（headless 不可靠）。每阶段回归既有流（拖加/连边 guard/键盘删/属性编辑/Save/标准管线）。
