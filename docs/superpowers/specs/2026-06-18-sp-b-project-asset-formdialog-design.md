# SP-B 项目创建/编辑 + 资产库 框架迁移设计

> 子项目 B，属于「重新审核接口服务 → 重构 web 页面」总工程的第二步。
> 总工程拆分：SP-A 配置/管理页重构（✅ DONE，PR #59）→ **SP-B 项目创建/编辑 + 资产库（本文）** → SP-C API 一致性整治（可选）→ SP-D 运行/工作流页 UX（可选）。

**目标：** 把项目创建/编辑对话框、工作流对话框、资产库页壳迁移到 SP-A 建立的 `features/common/crud/` 框架（`FormDialog` / `CrudResourcePage`），消除手写 Dialog/外壳重复，三处呈现/行为/视觉/交互**完全不变**。

**纪律（同 SP-A）：** 纯结构重构 + 外壳一致化。保留每个现有测试的全部断言，仅在 markup 变化时调选择器，不弱化断言。每个 Task 末尾 commit。

**非目标：**
- 不改后端 HTTP API（那是 SP-C）。
- 不做视觉/布局重做、不改任何交互语义或业务行为（运行/工作流页 UX 是 SP-D）。
- 不动 `AssetGalleryModal`、`AssetThumb`、`AssetMedia` 等展示型组件的内部（它们非 CRUD，无框架可用面）。

**技术栈：** 复用 SP-A 框架（`FormDialog`、`CrudResourcePage`、`useFormContext`、`Controller`、`schema: ZodType<T,T>` + `zodResolver` cast 桥接）。React 19 + TS + rhf + zod + TanStack Query + Vitest/Testing Library。

---

## 架构：复用 SP-A 委托模式

框架原语只拥有不变量（对话框 open/state、rhf FormProvider、提交/错误、页头/加载/错误/空态外壳）；资源页通过 `children` + `useFormContext` 委托表单字段，通过 `Controller` 委托复杂字段（绘本配置、工作流节点图），通过 `headerExtra`/`loadingSkeleton`/`emptyState` 委托页面特有 chrome。

三处迁移共用同一套现有 `features/*/api.ts` hooks（已自带 invalidate），不改数据流。

---

## 现状（已审）

- **项目创建/编辑**（`features/projects/`）：`CreateProjectForm`（422 行）/`EditProjectForm`（494 行）均已 rhf+zod，~90% 重复——同一组字段（name/brief/contentType/targetPlatform/style/plannerProvider/plannerModel/imageProvider/imageModel）+ `kind`（standard/picturebook 切换）+ `pbConfig`（绘本配置，经 `PictureBookConfigForm`）。各自被 `CreateProjectDialog`/`EditProjectDialog` 用手写 `<Dialog>` + `open`/`submitError` state 包裹。**注意：`kind` 与 `pbConfig` 当前是 rhf 之外的本地 `useState`，提交时合并**——这是迁移的关键 wrinkle。
- **工作流对话框**（`WorkflowDialog.tsx`，230 行）：`WorkflowForm` 是手写 useState（name + nodes 图 + submitError + isSubmitting），校验：name 必填、≥1 节点、节点 ID 非空且唯一、图无环（`findGraphError`）。`WorkflowDialog` 手写 `<Dialog>` + open 包裹。
- **资产库**（`features/library/LibraryPage.tsx` 的 `LibraryView`，432 行；路由 `orgs.$org.assets.tsx`）：已接收 `isLoading`/`isError`/`onRetry`/分页/过滤/Drawer props，但**自己手写**页头 `<h1>资产库</h1>` + 骨架网格 + "资产加载失败"+retry + 空态——正是 `CrudResourcePage` 提供的外壳。库特有：keyset 分页（"加载更多"）、过滤栏、详情 Drawer、缩略图网格。

---

## 三处迁移设计

### 1. 项目创建/编辑 → FormDialog + 共享 ProjectFields/schema

- 新建 `features/projects/ProjectFields.schema.ts`：导出 `projectFormSchema`（zod）+ `ProjectFormValues` 类型 + `defaultsFor(initial?)` 辅助。**把 `kind` 折入 schema 作 enum 字段、`pbConfig` 折入 schema 作对象字段**（消除 rhf 之外的本地 state，使 `FormDialog` 的单一 `onSubmit` 拿到全量）。`pbConfig` 在 picturebook 模式下用 `.superRefine` 套用现有绘本校验（如有）。
- 新建 `features/projects/ProjectFields.tsx`：`ProjectFields` 组件（仅 React 组件导出，防 react-refresh lint），经 `useFormContext` 渲染所有 rhf 字段 + kind 切换；当 `kind==="picturebook"` 时渲染 `PictureBookConfigForm`（经 `Controller` 绑定 `pbConfig` 字段，保留其现有 props/校验）。供 Create/Edit 共用。
- `CreateProjectDialog.tsx` / `EditProjectDialog.tsx` 改为薄层：`FormDialog<ProjectFormValues>`（mode create/edit、title、schema=projectFormSchema、defaultValues、onSubmit、`<ProjectFields/>` 作 children）。Create 成功后的"跳工作台/关闭"、Edit 的预填，沿用现有 onSuccess/初始值逻辑。
- toast 单源：**迁移不改变 toast 归属**（保持现状的成功/失败 toast 由谁发出），仅确保 FormDialog 化后不出现双发（SP-A 同类教训）。如现状无 toast 则不新增。

### 2. WorkflowForm → FormDialog

- 新建 `features/projects/WorkflowDialog.schema.ts`：`workflowFormSchema`（zod：`name` min 1、`nodes` 数组）+ `.superRefine` **复用 `findGraphError` + 唯一/非空 ID 校验**（与现有 4 条 setSubmitError 逐字等价）+ 类型导出。
- `WorkflowForm` 经 `FormDialog` 承载：`name` 用 `register`/`useFormContext`，`nodes` 复杂图字段用 `Controller` 绑定 `WorkflowNodesEditor`（保留其现有交互）。`FormDialog` 接管 open/submitError/isSubmitting。
- 保留 `findGraphError` 导出（被测试引用）；校验错误文案逐字不变。

### 3. LibraryView 页壳 → CrudResourcePage

- `LibraryView` 改用 `CrudResourcePage` 作页壳：`title="资产库"`、`isLoading`/`isError`/`onRetry`/`isEmpty` 透传；过滤栏放 `headerExtra`；缩略图网格 + "加载更多" + 详情 Drawer 作 `children`；网格骨架作 `loadingSkeleton`、空态作 `emptyState`（沿用 SP-A 为此加的可定制点）。视觉逐像素不变。
- `AssetGalleryModal` 等展示组件**不动**。

---

## 测试

保留并跑通全部现有断言：`CreateProjectDialog.test.tsx`、`EditProjectDialog.test.tsx`、`projects.test.tsx`、`PictureBookConfigForm.test.tsx`、`WorkflowDialog`（含 `findGraphError`/图校验）、`library.test.tsx`、`AssetGalleryModal.test.tsx`、`assetThumb`/`assetStatus`。仅在 markup 嵌套变化时调选择器，不弱化断言。各 section/组件可补针对性单测（如 `ProjectFields` 的 kind 切换、schema 的图校验 superRefine）。

收尾：`npx tsc --noEmit` 干净、`npx eslint`（新文件不得新增错误，schema 抽到 `*.schema.ts` 避免 react-refresh）、`npx vitest run` 全绿；浏览器烟雾对比三处迁移前截图零回归。

---

## 风险 / 边界

- **kind/pbConfig 折入 rhf**：当前是 rhf 之外本地 state。折入 schema 后须保证提交 payload（`CreateProjectInput`/`UpdateProjectInput`）逐字不变，尤其 `pictureBookConfig` 是 JSON **字符串**字段（非对象）——序列化时机/格式不能变（曾因传对象导致 400）。
- **工作流图校验 parity**：4 条校验分支（name 必填 / ≥1 节点 / ID 唯一非空 / 无环）须经 superRefine 等价复刻，错误文案逐字一致。
- **资产库 keyset 分页**：`CrudResourcePage` 接管页壳后，分页/Drawer/过滤的 URL search-param 行为不能变。
- **toast 单源**：迁移后提交成功/失败 toast 不得双发（SP-A 同类教训）。

## 任务拆分（交 writing-plans 细化）

1. 抽 `ProjectFields.schema.ts` + `ProjectFields.tsx`（TDD：schema 校验 + kind 切换/pbConfig 委托）。
2. `CreateProjectDialog` → FormDialog（薄层 + 共享字段），保留断言。
3. `EditProjectDialog` → FormDialog（复用 ProjectFields），保留断言。
4. `WorkflowDialog.schema.ts`（superRefine 复用 findGraphError）+ `WorkflowForm` → FormDialog（Controller 接节点编辑器）。
5. `LibraryView` 页壳 → CrudResourcePage（headerExtra/children/skeleton/empty）。
6. 收尾验收：tsc/eslint/vitest 全绿 + 浏览器烟雾零回归 + finishing-a-development-branch。
