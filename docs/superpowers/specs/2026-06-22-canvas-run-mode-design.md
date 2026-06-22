# 画布运行视图集成 设计（Canvas Run Mode）

> 把 run/workbench 视图并入工作流画布,经 `编辑 | 运行` 模式切换。运行模式在画布上叠加某次 run 的执行状态 + 点节点看产物 + 运行控制/画廊/日志/运行选择器。

## ⚠️ 关键发现(已核验后端)
**run 状态节点 id ≠ 工作流节点 id,不能按 id 叠加。** `PlanCustom`(planner.go:128)用 `LocalID=n.ID`,但 `todos.CreateGraph`(store.go:44)给每个 todo 发**新 UUID**,`todos` 表无 `local_id` 列(storage.go),`buildGraph`(projectstate/state.go)设 `GraphNode.ID=todo.ID`(UUID)。→ 工作流节点 `script-1` 与 run 节点 UUID **无持久回链**。
**解法:结构映射 `(type, 该类型在拓扑序中的序号)`。** 两侧都是同一工作流定义建的同构 DAG、都用同一 `layerize`。纯函数 `overlayRunStatus(workflowNodes, edges, projectState) → Map<canvasNodeId, {status, assetId?, todoId?}>`:对画布节点按 layerize 拓扑序算每类型序号,对 `state.nodes`(后端已拓扑序,数组序即可)算每类型序号,按 `(type, 序号)` join。**计数不匹配的节点 → pending/中性、不可点,绝不崩。** 布局用工作流**自存的 node.position**,不用 run 图布局。
图片扇出是 `pips`(PipState[])不是节点 → 画布上点 storyboard 节点开分镜抽屉,图片走画廊 + 右栏 SelectedAssetPanel。

## 模式模型
路由 `/workflow?wf=<id>` 加顶栏 `编辑 | 运行` 切换。**模式由 `?run=` 派生**(有=运行,无=编辑);切到运行默认 `?run=workflow.latestPlanId`;URL 是唯一真源,无独立持久态。编辑模式=现有编辑器**完全不变**。

## 运行内容 → 复用组件
1. 节点状态叠加 + 点开看产物 → `WorkflowNode` 加 run 变体(复用 GraphView 的 NODE_COLOR + done 填充✓/running 琥珀转环/failed danger);点击 → `ScriptView`/`StoryboardView` 抽屉 + `SelectedAssetPanel`(复刻 handleSelectNode,用 join 出的 todoId)。
2. 运行控制 + 状态徽标 → 移植 `WorkbenchView` 顶栏(运行/取消/重新运行 via useRun/useCancel、badge readyForReview/showReviewBadge、SseIndicator、去审核 CTA)。
3. 素材画廊 + 运行汇总 → `AssetGalleryModal` + `RunSummary`(原样)。
4. 事件日志 + 运行选择器 → `EventLog`(可折叠)by `useProductionTimeline`;选择器 = `usePlans(id)` 切 `?run=`。

## 路由整合
加 `?run=`。`/runs/$runId`:能解析出 run 的 workflowId 就 `<Navigate>` 到 `/workflow?wf=&run=`,否则(legacy workflow_id=NULL)保留旧 `RunWorkbenchPage` 兜底。**Plan/`/plans` 当前不返回 workflowId**(后端有 plans.workflow_id 但列接口没透出)→ Phase 3 一行后端把 `workflowId` 加进 `/plans`(struct + SELECT + web 类型);不想动后端则仅从已知 wf.id 的项目页入口重定向,/runs 深链渲染旧页。项目页 run 入口/run 后跳转 → 画布运行模式。

## 只读硬化(运行模式)
禁:onNodesChange 编辑/onConnect*/onDrop 拖加/`deleteKeyCode=null`/剪贴板键盘/属性编辑/保存;键盘 useEffect 仅 `mode==="edit"` 挂。留:平移/缩放/minimap/fitView/节点选中(点开产物)。`nodesDraggable/Connectable=false`。

## 非目标
不做后端 local_id 持久化(结构映射够用);不把每张图做成画布节点;不换布局引擎;不动 EDGES-是-dependsOn-真源(仅编辑模式);无第二个 Vite。

## 风险
R1 planner fallback 替换自定义图致计数漂移 → 未匹配节点兜底。R2 /runs 重定向缺 plan.workflowId → Phase 3 一行后端或保留旧页兜底。R3 SSE live/DnD headless 难测 → 纯 mapper 单测 + :5173 手验。
