# SP-A：配置/管理页通用 CRUD 框架重构设计

> 子项目 A，属于「重新审核接口服务 → 重构 web 页面」总工程的第一步。
> 总工程拆分：**SP-A 配置/管理页重构（本文）** → SP-B 项目创建/编辑+资产库 → SP-C API 一致性整治（可选）→ SP-D 运行/工作流页 UX（可选）。

**目标：** 为 llm-agent-studio 前端的五个配置/管理页（PlatformAdmin / ModelConfig / StorageConfig / Prompt / Members）抽出一套**通用 CRUD 框架**，把当前内联在巨型文件里的「列表 → 新增/编辑模态 → 删除确认 → toast」重复逻辑收敛为可复用原语；五页迁移到框架后各自保持现有呈现（表格或卡片）与行为不变。

**非目标：** 不改后端 HTTP API（那是 SP-C）；不做视觉/布局重做（那是 SP-D）；不改任何业务行为或交互语义（纯结构重构 + 外壳一致化）。

---

## 1. 背景与现状（审计结论）

两轮只读审计（API 面 + 五页结构）结论：

- **后端 API 健康**：85 条路由，无死接口、无前端坏调用、`web/src/lib/types.ts` 是类型单一来源、鉴权 gate 一致。故本子项目**不动后端**。
- **前端痛点在配置/管理页**：五页把 列表+新增+编辑+删除+确认 全塞在单文件，行数 264–1053；其中 `PlatformAdminPage.tsx`(1053) 是多区块聚合（orgs/users/admins/邮件/全局存储）的真正巨石。
- 五页共享同一套不变骨架，但各自在「表单字段、呈现形态（表格 vs 卡片）、额外动作（set-default / reveal-key / list-models / copy / 改角色）、密钥处理」上分叉。

源文件（绝对路径前缀 `web/src/`）：

| 页面 | 文件 | 行数 | 测试 |
|---|---|---|---|
| PlatformAdmin | `features/platform/PlatformAdminPage.tsx` | 1053 | `features/platform/PlatformAdminPage.test.tsx`(351) |
| ModelConfig | `features/cost/ModelConfigPage.tsx` | 744 | `features/cost/ModelConfigPage.test.tsx`(396) |
| StorageConfig | `features/storage/StorageConfigPage.tsx` | 680 | `features/storage/StorageConfigPage.test.tsx`(235) |
| Prompt | `features/prompt/PromptListPage.tsx` | 452 | `features/prompt/PromptListPage.test.tsx`(154) |
| Members | `features/members/MembersPage.tsx` | 264 | `features/members/MembersPage.test.tsx`(131) |

## 2. 决策记录

- **走法 = 通用 CRUD 框架**（用户明确选择，优先于审计的「外科式抽取」建议）。审计与设计者均提示泛型框架有泄漏风险；为此本设计采用「**委托式**」框架而非「配置式巨型 props」框架来规避泄漏（见 §3 设计原则）。
- **列表呈现 = 框架双模式，保留现状**：框架同时支持 `table` 与 `cards` 两种 layout，各页沿用现有呈现，不做卡片↔表格互换。只统一外壳/对话框/空态/间距。
- **零行为 + 零视觉回归**：迁移前后业务行为、交互、像素呈现一致；现有页面测试断言保留。

## 3. 设计原则（防泄漏的核心）

框架只拥有**不变部分**，资源页**委托**自己的差异部分：

- 框架拥有：对话框状态机（创建/编辑/删除的开合与草稿）、外壳布局（标题/描述/新增按钮/Skeleton/错误重试/空态）、create/update/delete mutation 与 toast/invalidate 的接线。
- 资源委托：**表单字段 JSX**、**item 渲染**（列定义或卡片渲染）、**额外行为按钮**、**错误文案映射**——通过 children / render-prop / 配置传入，框架对其内部无感知。

判定标准：一个原语若需要为某资源新增「专属布尔开关 props」（如 `showRevealKey`、`isGithubMode`），即视为泄漏信号，应改为把该差异作为 children/render-prop 下沉到资源页。

## 4. 框架原语（新模块 `web/src/features/common/crud/`）

每个原语单独文件、单一职责、独立可测。

### 4.1 `useCrudResource<T, CreateInput, UpdateInput>(cfg)` — headless 状态机
- **输入 cfg**：`{ list: UseQueryResult<T[]>, create, update, remove (三个 mutation), getId(item)=>string, messages?(错误码→文案映射) }`。
- **状态**：`dialog: { mode: "create" | "edit", target?: T } | null`；`deleteTarget: T | null`。
- **handlers**：`openCreate()`、`openEdit(item)`、`closeDialog()`、`requestDelete(item)`、`cancelDelete()`、`confirmDelete()`。
- **submit(values)**：按 `dialog.mode` 调 create 或 update(getId(target), values)；成功→closeDialog + toast.success；查询失效沿用各 mutation hook 既有的 `onSuccess` invalidate（框架不重复处理，迁移时逐页核对该 hook 是否已 invalidate）；失败→返回/抛出 ApiError 文案（经 `messages` 映射）。
- **返回**：`{ items, isLoading, error, dialog, deleteTarget, ...handlers, submit, submitError }`。
- **不做**：不渲染任何 UI；不持有 fetch（list/mutation 由调用方注入，复用既有 `features/*/api.ts` hooks）。

### 4.2 `<CrudResourcePage>` — 外壳
- **props**：`title`、`description?`、`createLabel?`、`onCreate?`（无创建表单的页面如 Members 可不传）、`isLoading`、`error`、`isEmpty`、`emptyHint?`、`children`（列表主体）。
- 渲染：页头（标题+描述+「新增」按钮）、加载 Skeleton、错误+重试、空态，然后 `children`。
- **不**内嵌 FormDialog/ConfirmDialog —— 由资源页显式挂载（保持组合显式、避免外壳吞掉对话框状态）。

### 4.3 `<DataView<T> layout="table" | "cards">` — 双模式列表
- `layout="table"`：`columns: Array<{ key, header, cell:(item)=>ReactNode, className? }>` + `rowActions`。
- `layout="cards"`：`renderCard:(item, actions)=>ReactNode`（资源完全掌控卡片外观）+ 可选 `groupBy:(item)=>string`（供 ModelConfig 按 kind 分组）。
- `rowActions: RowAction<T>[]` 在表格末列 / 卡片角落统一渲染。
- `items`、`getId`。空态由 CrudResourcePage 负责，DataView 假定非空。

### 4.4 `<FormDialog>` — 对话框壳 + rhf
- **props**：`open`、`mode:"create"|"edit"`、`title`、`schema: ZodType`、`defaultValues`、`onSubmit(values)`、`submitError?`、`submitting?`、`children`（字段 JSX）。
- 拥有：Dialog 开合、`FormProvider`（react-hook-form + zodResolver）、提交按钮+loading、表单级错误展示、取消。
- 资源拥有：`children` 内用 `useFormContext()` 渲染 `<Input>/<Select>/<RevealSecretInput>/条件字段`。
- create↔edit 通过 `mode` + `defaultValues` 区分（编辑预填）。

### 4.5 `<ConfirmDialog>` — 确认
- **props**：`open`、`title`、`description?`、`confirmLabel?`、`variant?: "danger"|"default"`、`confirming?`、`onConfirm`、`onCancel`。删除/移除/撤销/重置共用。

### 4.6 `<RevealSecretInput>` — 密钥字段
- Eye/EyeOff 明文切换；可选 `onReveal?():Promise<string>`（reveal-key 异步解密，ModelConfig 用）；`alreadySet?: boolean` 展示「已配置，留空保持不变」提示。
- 不传 `onReveal` 即退化为普通 password 输入（StorageConfig / Mail 用）。

### 4.7 `<SingletonConfigForm>` — 单记录 upsert
- 给 PlatformAdmin 的「全局邮件」「全局存储」：拉一条记录 → 表单 → upsert（无列表、无删除）。
- **props**：`query`（取单条）、`upsert` mutation、`schema`、`defaultsFrom(record)`、`children` 字段、`title`。
- 与 CrudResourcePage 并列的第二种形态，避免把「列表 CRUD」硬套到「单条配置」。

### 4.8 `RowAction<T>` 描述符
- `{ label, icon?, onClick:(item)=>void, variant?, hidden?:(item)=>boolean, disabled?:(item)=>boolean }`。
- 表达 edit / delete / set-default / reveal / copy / list-models / 改角色 / toggle-admin / reset-password 等所有行级动作，按页配置数组。

## 5. 五页迁移（各自保留呈现）

| 页面 | 迁移形态 | 关键点/验证 |
|---|---|---|
| **Members** | CrudResourcePage + DataView·table；rowActions=改角色(内联 Select)/移除(ConfirmDialog)；加成员=页头内联 email+role（非 FormDialog）| 最简，**首迁**，端到端跑通框架 |
| **Prompt** | DataView·cards；FormDialog(name/content/style/kind)；rowActions=编辑/设默认/复制/删除 | 卡片模式 + 简单表单 |
| **StorageConfig** | DataView·table；FormDialog 内 `StorageModeFields`（mode 条件字段，superRefine 校验保留）+ RevealSecretInput；rowActions=编辑/设默认/删除 | **委托字段逃生口** + 密钥；删除 409→「有引用，请改用停用」文案保留 |
| **ModelConfig** | DataView·cards(groupBy=kind)；FormDialog 内 list-models 自定义字段 + RevealSecretInput(onReveal=reveal-key)；rowActions=编辑/删除 | reveal-key 异步 + 自定义字段 + 分组卡片 |
| **PlatformAdmin** | **组合多原语**：AdminsSection(CrudResourcePage·table，加/撤销)；UsersSection(CrudResourcePage·table，仅 rowActions：详情/重置密码/删除/toggle-admin，无创建表单)；MailSection + GlobalStorageSection(各一个 SingletonConfigForm)；OrgsSection(只读 DataView·table) | 最大、**最后做**；证明框架可组合出多区块页 |

## 6. 数据流

资源页 = 注入既有 api.ts hooks → `useCrudResource(cfg)` → 渲染 `<CrudResourcePage>`（外壳）内含 `<DataView>`（列表）+ 显式挂载 `<FormDialog>`（受 `dialog` 驱动）+ `<ConfirmDialog>`（受 `deleteTarget` 驱动）。表单字段经 `FormProvider` 由资源页 children 提供；错误经 `messages` 映射成 toast。

## 7. 错误处理

- 沿用现有 `ApiError`（`apiClient.ts`）。各资源保留**自己的**错误码→文案映射（409/404/412 语义各页不同，如「最后一个管理员」「有引用请停用」「用户不存在」），通过 `useCrudResource` 的 `messages` 注入，不在框架里硬编码业务文案。
- 表单级错误（zod 校验、提交失败）经 `FormDialog.submitError` 展示在字段下方/底部。

## 8. 测试策略（TDD）

- **框架原语单测**（新增，先写）：
  - `useCrudResource`：openCreate/openEdit/closeDialog/requestDelete/confirmDelete 状态迁移；submit 在 create/edit 两态分别调对应 mutation；失败映射文案。
  - `FormDialog`：create/edit 预填绑定、提交回调、submitError 展示、取消不提交。
  - `ConfirmDialog`：confirm 调回调、cancel 不调。
  - `RevealSecretInput`：明文切换、onReveal 异步解密、alreadySet 提示;无 onReveal 退化普通密码。
  - `DataView`：table 渲染 columns + rowActions；cards 渲染 renderCard + groupBy 分组。
- **现有 5 页测试保持绿**：迁移后保留全部断言；仅当 markup 变化时调整选择器，不弱化断言。
- **回归**：`npx tsc -b` 干净 + `npx vitest run` 全绿 + 每页浏览器截图与迁移前对比（零视觉回归）。

## 9. 迁移顺序（增量、每步独立可交付 + commit）

1. 建框架原语（带单测），不碰任何页面。
2. 迁 Members（最简）—— 跑通框架端到端。
3. 迁 Prompt（卡片 + 简单表单）。
4. 迁 StorageConfig（条件字段 + 密钥）。
5. 迁 ModelConfig（分组卡片 + reveal + list-models）。
6. 迁 PlatformAdmin（组合 sections + SingletonConfigForm）。

每步：相关测试绿 + 截图烟雾 + 一个原子 commit。

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 泛型抽象泄漏（props 爆炸） | 委托式设计：字段/渲染/动作下沉到资源页，框架只管不变量；以「是否要加资源专属布尔 props」为泄漏判定红线 |
| 视觉回归 | 双模式保留现状；每页迁移后截图与迁移前逐页对比 |
| 测试震荡 | 保留现有断言，仅调选择器；新原语补单测 |
| PlatformAdmin 体量大 | 放最后；先由前四页验证框架；其多区块用「CrudResourcePage×N + SingletonConfigForm×2 + 只读 DataView」组合，不强求单一抽象 |

## 11. 验收标准

- 五页全部迁移到框架，各自呈现/行为/视觉不变。
- 新增 `features/common/crud/` 原语模块，含单测。
- 五页文件显著瘦身（巨型文件拆为「薄资源页 + 框架原语 + 委托字段组件」）。
- `tsc -b` 干净；`vitest run` 全绿；每页截图零回归。
- 无后端改动；无业务行为变更。
