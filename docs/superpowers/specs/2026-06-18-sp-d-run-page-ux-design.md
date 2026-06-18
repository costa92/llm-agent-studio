# SP-D 运行页 UX 打磨设计

> 子项目 D，「重新审核接口服务 → 重构 web 页面」总工程第四步（收尾）。
> 进度：SP-A 配置/管理页 CRUD 框架（PR #59）✅ · SP-B 项目/工作流对话框去重（PR #61）✅ · SP-C API 一致性（仅 1 实质 bug，PR #63 删除提示词 200）✅ · **SP-D 运行页 UX（本文）**。

**目标：** 打磨运行工作台（`WorkbenchView`）的 4 处体验——事件日志可读性、运行总状态/进度概览、DAG/阶段视图、选中资产面板。**纯表现层改动**：所有数据、行为、SSE 实时、路由接线、抽屉/相册/绘本阅读器均不变；只改呈现。

**与 SP-A/B/C 的纪律差异：** SP-A/B 是「零视觉变化」重构；**SP-D 是有意的视觉/交互改进**（非零变化）。但仍严守：不改后端/API/数据流，不破坏既有运行页行为（SSE 累积、阶段选择、抽屉、相册、绘本、待审核跳转）。

**非目标：**
- 不改后端 HTTP API / projectstate.Compute / SSE 帧（SP-C 已结）。
- 资产库页迁移**仍暂缓**（用户本轮选「运行/工作流页 UX」，Library 不在 SP-D 本 pass）。
- 不重做整页布局（仍是三栏工作台）；不引入新框架。

**技术栈：** React 19 + TS；改动集中在 `WorkbenchView`（`features/workflow/WorkbenchPage.tsx`）+ 子组件 `EventLog`/`TimelineStage`（`components/studio/`）+ 路由 `preview` 槽（`...runs.$runId.tsx`）。复用既有资产 API（accept/reject）。

---

## 现状（已审 + 截图）

运行页 = 路由 `...runs.$runId.tsx` 组装数据后渲染 `WorkbenchView`（三栏）。所有 4 区数据已就绪、由 props 传入：
- `log: LogLine[]`（`{seq, kind, text, emphasis?: StageId}`），现由 `<EventLog>` 平铺渲染——左栏「事件日志」是一条条原始事件（`S2 todo_ready (script)`、`S4 asset_prescreened 预筛`…），密集、技术化、可读性差。
- `state: ProjectState`（`{stages, pips, assets:{done,total,pending}, runStatus, status, error, nodes, edges, isCustom}`）——顶栏只有一个状态 `badge` + 运行中 `SlateBar`，**无总进度概览**。
- 中栏 `TimelineStage` 列表（或自定义 DAG 走 `GraphView`）+ S4 `PipGroup` 缩略图。
- 右栏 `preview`（路由传入 `AssetThumb` + `AssetPreviewActions`）或空态。

---

## 4 处设计（均纯表现，数据来自既有 props）

### 1. 事件日志可读性（`EventLog` + `WorkbenchView` 的 log 映射）
- **按阶段分组**：用 `LogLine.emphasis`（S1–S5）把平铺流分组，每组一个友好小标题（规划 / 剧本 / 分镜 / 素材 / 审核），组内按 seq。
- **友好文案**：对仍偏原始的 `kind`/`text`（如 `todo_ready (script)`）补一层中文映射表（`kind → 友好短语`），保留 `emphasis` 着色。
- **默认折叠为「详情」**：日志整体收进一个默认折叠的 `<details>`「事件详情」，折叠态只显「最新动态」一行（最后一条 + 计数）。展开看全量分组。降噪同时不丢信息。
- 时间戳：`LogLine` 无 `ts` 字段、SSE 帧不保证带时间——**不引入真实时间戳**（避免触后端）；如需顺序感，用 seq 既有顺序即可。（记于风险。）

### 2. 运行总状态/进度概览（`WorkbenchView` 新增一条概览条）
- 顶栏下方新增 `RunSummary` 条（新建小组件或内联）：读既有 `state`——`runStatus`（运行中/已完成/失败/空闲）文案 + 已完成阶段 `X/N`（`stages.filter(done).length / stages.length`）+ 素材 `assets.done/assets.total` + 一条细进度条（完成比例）。
- 失败/取消态用 `state.error`/`status` 着色（复用 `statusVariant`）。运行中可与现有 `SlateBar` 协调（避免重复）。无新数据。

### 3. DAG/阶段视图打磨（`TimelineStage` / 中栏）
- 阶段连接线按 done/active/pending 着色更分明；`done` 阶段连接线实心、`pending` 虚线/灰。
- S4 `PipGroup` 缩略图增大点击热区、hover 态。
- S5「人工审核」`pending` 阶段内联一个显眼「去审核 →」CTA（今天只在顶栏），与 `onOpenReview` 同接线。
- `GraphView`（自定义工作流）做同等的 done/active/pending 着色对齐（若成本可控；否则记为后续）。

### 4. 选中资产面板增强（右栏 / 路由 `preview` 槽）
- 预览更大；下方列资产元数据（类型/版本/状态，读选中 asset）。
- 当选中 asset 为 `pending_acceptance` 时，内联「采纳 / 拒绝」快捷操作——**复用既有 `useAccept`/`useReject`**（`features/review/api.ts`，已封装 `POST /api/assets/{id}/accept|reject`，roleAdmin，非 pending→409）。无需新 API。非该态维持现有 `AssetPreviewActions`（在新标签打开/复制链接）。
- 空态文案更友好（已有，可微调）。

---

## 测试

- **保留**既有断言：`workflow.test.tsx`、`useProductionTimeline.test.tsx`、`GraphView.test.tsx`、`PictureBookReader.test.tsx`、`AssetGalleryModal.test.tsx`——SP-D 改表现，这些行为断言须仍绿；仅在 DOM 结构变动时调选择器，不弱化。
- **新增**针对新表现逻辑的单测：事件日志分组/折叠 + kind→友好文案映射；`RunSummary` 进度计算（X/N、done/total、各 runStatus 文案/着色）；选中面板的 pending→accept/reject 渲染与调用。
- **浏览器烟雾 + 截图**：每区改完截图对比，确认无行为回归（SSE live、阶段点击开抽屉、相册、绘本、去审核跳转、运行/取消/重跑）。

## 风险 / 边界
- **SSE 实时累积**：事件日志分组/折叠须正确处理「运行中持续追加行」（分组按 emphasis 实时归并；折叠态「最新动态」随最后一行更新）。
- **无时间戳**：不引入真实时间（避免后端改动）；只用 seq 顺序。
- **accept/reject 权限**：右栏快捷采纳/拒绝复用 `useAccept`/`useReject`（已带 admin 门禁 + 失效逻辑）；仅在 `canRun`/admin 可见，与审核台一致；409（非 pending）/403 文案对齐。
- **自定义 DAG（isCustom）**：`GraphView` 路径与 `TimelineStage` 路径都要兼顾（概览条对 custom 也要算 stages，或对 custom 退化为节点计数）。
- **不破坏待审核流**：`runStatus==="done"` 的「待审核 · N」徽标 + 「去审核」CTA 语义不变。

## 任务拆分（交 writing-plans 细化）
1. 事件日志：`kind→友好文案` 映射 + `EventLog` 分组/折叠（含「最新动态」折叠态）。
2. `RunSummary` 概览条（runStatus + X/N 阶段 + done/total 素材 + 进度条），接入 `WorkbenchView`。
3. `TimelineStage`/连接线 + S4 pip + S5 内联「去审核」CTA 着色与交互打磨（GraphView 着色对齐视成本）。
4. 选中资产面板：更大预览 + 元数据 + pending 态 accept/reject 快捷操作（路由 `preview` 槽 + 必要的小组件）。
5. 收尾：tsc/eslint/vitest 全绿 + 浏览器烟雾逐区截图零行为回归 + finishing-a-development-branch。
