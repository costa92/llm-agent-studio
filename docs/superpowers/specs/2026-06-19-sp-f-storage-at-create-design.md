# SP-F 项目创建时可选存储后端（storage-at-create）设计

> 子项目 F，「重新审核接口服务 → 重构 web 页面」总工程的 SP-C 审计遗留收尾项（F2，用户选定落地为真功能）。
> 进度：SP-A CRUD 框架（#59）✅ · SP-B 对话框去重（#61）✅ · SP-C API 一致性（#63）✅ · SP-D 运行页 UX（#64）✅ · SP-E 资产库迁移（#66）✅ · **SP-F storage-at-create（本文）**。

**目标：** 把「per-project 存储后端 override」从「仅编辑」暴露到「**创建**」。今天编辑项目已支持选 `storageConfigId`（空 = 继承组织默认），但创建对话框不暴露存储选择、`CreateProjectInput` 也无该字段——创建只能用组织默认。本功能镜像 edit 既有写法把存储选择器暴露到 create。

**关键事实：后端已就绪。** `createProjectHandler`（`internal/httpapi/handlers.go`，request struct 含 `storageConfigId` json tag）与 `project.CreateInput`（`internal/project/store.go`，含 `StorageConfigID`）已接受并持久化 `storageConfigId`。**纯前端改动，后端零改。**

**非目标：**
- 不改后端（已就绪）。
- 不改编辑流（edit 的存储 override 不动；create 镜像它）。
- 不改 `ProjectFields` 共享组件（其存储选择器已具备：「继承组织默认」选项 + 仅列 `enabled` 配置 + 当前值提示）——create 只是开始给它传 `storageConfigs` prop。
- 不动 SP-C 其它遗留项（F3/F4/F5/F6/F8 经复核 moot/cosmetic/YAGNI，刻意不碰）。

**技术栈：** React 19 + TS；TanStack Router + React Query（`useStorageConfigs`）；rhf + zod（`ProjectFields`）；Vitest + @testing-library/react。

---

## 现状（已审）

- **编辑流（镜像对象）：** `ProjectsPage`（`orgs.$org.projects.index.tsx`）`useStorageConfigs(org)` → `ProjectListView` → `EditProjectDialog` `storageConfigs` prop → `<ProjectFields storageConfigs={...} />`；提交时 `storageConfigId: values.storageConfigId` 进 update payload。
- **`ProjectFields`（共享）：** 已有 `storageConfigId` 表单字段 + `storageConfigs` prop；`storageConfigs` 真值时渲染存储选择器（「继承组织默认」+ `filter(c => c.enabled)` 选项 + 当前值提示）。`ProjectFormValues` 含 `storageConfigId`，`defaultsFor` 置 `storageConfigId: initial?.storageConfigId ?? ""`。
- **创建流（缺口）：** `CreateProjectDialog` 不接 `storageConfigs`、不传给 `ProjectFields`、`toCreateInput` 不含 `storageConfigId`；`CreateProjectInput` 类型无该字段。`ProjectListPage` 两处 `<CreateProjectDialog>`（头部「新建项目」+ 空态「还没有项目」）均未传 `storageConfigs`。

---

## 设计（4 文件，全镜像 edit 现有写法）

| 文件 | 改动 |
|---|---|
| `web/src/lib/types.ts` | `CreateProjectInput` 接口新增 `storageConfigId?: string`（对齐后端 request struct `storageConfigId` + `Project` 实体既有同名字段） |
| `web/src/features/projects/CreateProjectDialog.tsx` | ① import `StorageConfig` 类型；② `CreateProjectFormProps` 新增 `storageConfigs?: StorageConfig[]`（`CreateProjectDialogProps` 经 extends 自动获得）；③ `<ProjectFields storageConfigs={storageConfigs} />` 透传；④ `toCreateInput` 含 `storageConfigId`（见下「决策」） |
| `web/src/features/projects/ProjectListPage.tsx` | 两处 `<CreateProjectDialog>` 均加 `storageConfigs={storageConfigs}`（`storageConfigs` 已是 `ProjectListViewProps` 在手字段，源自 `useStorageConfigs(org)`） |
| `web/src/features/projects/projects.test.tsx` | 新增创建流存储测试（镜像 `EditProjectDialog.test.tsx` 存储断言）；保留现有断言全绿 |

**决策（已定）—— `toCreateInput` 对 `storageConfigId` 空值省略：**
```ts
...(values.storageConfigId ? { storageConfigId: values.storageConfigId } : {})
```
理由：匹配 `toCreateInput` 对其它可选 override 字段（plannerProvider/plannerModel/imageProvider/imageModel）的既有 omit-empty 风格（第 0 条裁决：匹配现有风格优先）。空 = 不带 override = 后端用组织默认（与今天「创建用默认」行为一致）。edit 是 always-send `""`，但 create 的 `toCreateInput` 局部惯例是 omit-empty，故遵 create 本地风格。

**数据流：** `useStorageConfigs(org)`（ProjectsPage 已调，编辑流复用）→ ProjectListView → 两处 CreateProjectDialog → ProjectFields 存储选择器 → 选中值入 `ProjectFormValues.storageConfigId` → `toCreateInput` → `CreateProjectInput.storageConfigId` → POST → 后端已持久化。

**零视觉/行为回归：**
- `ProjectFields` 不改；create 仅多传一个 prop → 存储选择器出现在创建对话框（与编辑对话框同款）。
- `storageConfigs` 为空/undefined 时 `ProjectFields` 不渲染存储选择器 → 与今天创建对话框完全一致（防御回退）。
- 编辑流、后端均不动。

## 错误处理

无新增错误路径：存储选择是可选字段；非法/被删配置由既有 `ProjectFields`「仅列 enabled」与后端校验兜底（与 edit 同）。

## 测试

- **新增**（`projects.test.tsx`，镜像 `EditProjectDialog.test.tsx:117-190` 的三条存储断言）：
  - 传 `storageConfigs` 时创建对话框渲染存储下拉（「继承组织默认」+ 各 enabled 配置）。
  - 选具体配置 → 提交 payload 含 `storageConfigId: "<id>"`。
  - 选「继承组织默认」（空）→ 提交 payload **不含** `storageConfigId`（omit-empty）。
- **保留** `projects.test.tsx` 现有 CreateProjectForm 断言（name/brief/contentType/style/textModels）全绿；仅在 DOM 真变时调选择器，绝不弱化。
- `tsc --noEmit` 干净；`eslint` 触及文件 0 error（既有 2 个无关 error 在 `AssetGalleryModal.tsx`/`useProductionTimeline.ts`，超范围不动）。
- 浏览器烟雾：创建项目对话框出现存储下拉、可选具体后端、选后创建成功（可选——核心由单测覆盖）。

## 风险 / 边界

- **omit-empty vs send-empty：** 采 omit-empty（遵 create 本地风格）；后端对「无 storageConfigId」与「storageConfigId='' 」都应回落组织默认——edit 发 `""` 已工作，create 省略等价或更保守。
- **两处 CreateProjectDialog 都要传 prop：** 头部 + 空态；漏一处则该入口无存储选择器。计划须覆盖两处。
- **YAGNI：** 不顺手加 send-empty/统一 201 等无关项；只做存储选择器暴露。

## 任务拆分（交 writing-plans 细化）
1. 前端类型 + 创建对话框 + 路由透传 + 测试：`CreateProjectInput` 加字段 → `CreateProjectDialog` 接 prop/透传/`toCreateInput` → `ProjectListPage` 两处传 prop → 新增创建流存储测试（TDD）。
2. 收尾：tsc/eslint/vitest 全绿 + 浏览器烟雾（创建对话框存储下拉）+ finishing-a-development-branch。
