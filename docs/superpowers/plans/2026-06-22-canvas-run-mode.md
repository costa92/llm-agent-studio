# 画布运行视图集成 实现计划

> subagent-driven,逐 Phase、Phase 间复审。Spec 见 specs/2026-06-22-canvas-run-mode-design.md。
> **关键:run 状态按 `(type,拓扑序内同类型序号)` 结构映射,不能按 id**(todo 是新 UUID,无回链——已核验)。纯 mapper 先写先测。
> pnpm;验证只打用户那一个 :5173(勿起第二 Vite)。Phase 1-2 无后端改动;Phase 3 一行后端(/plans 加 workflowId)。
> 浏览器:demo@studio.com/demo12345;项目「测试-端到端案例」有 run——id 经 UI/API 现查,勿硬编码。

## Phase 1 — 模式切换 + 只读运行状态叠加 + 运行选择器 + 运行顶栏
- **1.1 纯 mapper** `web/src/features/workflow-canvas/runOverlay.ts`:`overlayRunStatus(nodes, edges, state): Map<id,{status,assetId?,todoId?}>`——画布节点用 layerize(同 seedPositions 的 `GraphEdge{from,to}`)算每类型序号;state.nodes 按数组序算每类型序号;`(type,序号)` join;未匹配省略(调用方当 pending)。测 `runOverlay.test.ts`:标准管线 2 节点映射、多 asset 扇、计数不匹配部分映射不抛、空 state 空 map。
- **1.2 WorkflowNode run 变体**:`data.run?:RunNodeStatus` 在时(运行模式)按 GraphView 视觉(done 填充✓/running 琥珀虚线转环/failed border-danger bg-danger/15/pending 中性)渲染,隐藏编辑 NodeToolbar;复用 GraphView.tsx:91-115 类形;amber only。测加 data-status/类断言。
- **1.3 路由模式**:workflow 路由 search 加 `run:z.string().optional()`;`mode=run?"run":"edit"`;运行模式 `runId=run??workflow.latestPlanId`;传 `mode/runId/org/project` 给 WorkflowCanvas;编辑路径不动。
- **1.4 WorkflowCanvas 模式壳 + 运行数据**:props `mode/runId`;顶栏加 `编辑|运行` 段切换(经注入 navigate 回调改 `?run=`,画布保持 route-agnostic 仿 onBack);运行模式调 useProjectState/usePlans/useRun/useCancel/useProductionTimeline,overlayRunStatus 注入 `toReactFlow(savedNodes)` 的 `data.run`(运行模式 rfNodes 用自存 position,不用 useNodesState 可编辑态),`<ReactFlow>` 只读(nodesDraggable/Connectable=false、deleteKeyCode=null、不挂编辑 handler、键盘 useEffect gate mode==="edit");运行顶栏移植 WorkbenchView 控制(badge/SseIndicator/运行·取消·重新运行/去审核);运行选择器 usePlans 切 `?run=`(标 #N+createdAt+status;跨 wf 过滤需 Phase3 后端字段,P1 列项目全部 plans);编辑模式一切照旧(handlers/panels 仅 mode==="edit" 渲染)。
- **1.5 验证**:单测 1.1/1.2;:5173 切运行→saved-position 节点显状态色,切回编辑→编辑器完好(拖/连/存仍可),选择器切 `?run=`,运行/取消/badge/SSE 在;三主题。

## Phase 2 — 点节点看产物 + 画廊 + 汇总 + 日志
- **2.1 运行模式选中/抽屉**:移植 RunWorkbenchPage 的 Selection + handleSelectNode(由 overlay map 解析 todoId/type/assetId);ReactFlow `onNodeClick`(运行模式)→ handleSelectNode;Sheet 抽屉 ScriptView/StoryboardView,useScript/useShots keyed by runId+todoId。
- **2.2 右栏 + 画廊 + 汇总**:运行右栏 = SelectedAssetPanel(useAsset + previewAssetId 逻辑);加 RunSummary 条 + AssetGalleryModal「查看全部素材(N)」;EventLog(可折叠)by useProductionTimeline,SSE state 帧 `qc.setQueryData(["project-state",id,runId])`。
- **2.3 验证**:单测 handleSelectNode 解析(script/storyboard→todoId、asset→assetId);:5173 点剧本→抽屉、分镜→抽屉、图节点→右栏预览、画廊开、汇总计数、日志实时(手验)。

## Phase 3 — 路由整合 + 只读硬化 + 全矩阵
- **3.1 /runs 重定向**:一行后端给 `/plans` 加 `workflowId`(Go struct+SELECT COALESCE(workflow_id,'')+web Plan 类型);runs 路由:plan.workflowId 非空→`<Navigate>` `/workflow?wf=&run=`,否则保留旧 RunWorkbenchPage 兜底。(不想动后端:仅项目页已知 wf.id 入口重定向,/runs 深链渲旧页——记录两案,推荐一行后端。)
- **3.2 项目页改链**:workflow 行「查看产物」→`/workflow?wf=&run=latestPlanId`;run 后跳转→`/workflow?wf=res.workflowId&run=res.planId`;run 历史「进入工作台」保留 /runs(靠 3.1 重定向)。
- **3.3 只读硬化**:审运行模式 ReactFlow props + 键盘 gate;测 `WorkflowCanvas.runmode.test.tsx`(运行模式无 NodePalette/PropertiesPanel/保存,connect/drop handler 未挂);编辑回归测试仍绿。
- **3.4 全矩阵**:三主题×{编辑↔运行;跑新生成看状态在画布上实时climb;开剧本/分镜/图产物;画廊;选择器切 run;/runs 深链重定向;legacy 无 wf 的 run 仍开旧页}。`pnpm -C web test`+`build`。SSE/DnD 手验,纯 mapper 单测。
