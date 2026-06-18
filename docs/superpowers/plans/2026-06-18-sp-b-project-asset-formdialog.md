# SP-B 项目创建/编辑 + 工作流 表单去重与 schema 抽取 Implementation Plan（务实版）

> **状态：✅ DONE（2026-06-18）** — 全 4 任务 + 收尾完成，经 PR #61 squash 合并入 main（`fffe984`）。
> 共享 `ProjectFields`/`projectFormSchema` 去重 Create(423→133)/Edit(495→136)；`WorkflowForm` rhf 化 + `workflowFormSchema`（superRefine 复刻 4 条图校验，findGraphError 复用）；`pictureBookConfig` 全程 JSON 字符串、零新校验；浏览器烟雾 4/4 零控制台错误、零视觉/行为回归；`tsc`/`eslint`(projects 0 错，净 −1)/`vitest`(423) 全绿；终审 APPROVED。
> Library/assets 暂缓（CrudResourcePage 硬编码「加载失败」≠ 资产页「资产加载失败」+ 单列 vs 双列筛选栏，迁移会破断言/视觉，需先扩原语）——见下方「Library 暂缓说明」。

> 子项目 B，「重新审核接口服务 → 重构 web 页面」总工程第二步。SP-A（配置/管理页通用 CRUD 框架，PR #59）已 DONE，建立了 `web/src/features/common/crud/` 八原语。本计划**务实地**复用 SP-A 的 rhf+zod 模式（不照搬 `FormDialog`/`CrudResourcePage` 公共壳），把项目创建/编辑两表单的重复字段抽成共享 `ProjectFields` + `projectFormSchema`，把 `WorkflowForm` 抽出 `workflowFormSchema` 并 rhf 化。呈现/行为/视觉/交互**完全不变**，公共 API 不变，消费方与测试**零改写**。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 每个实现者**零代码库上下文**——本计划给出每一步的完整代码、确切文件路径、确切命令与期望输出。

**Goal:** 把 `features/projects/` 的项目创建/编辑表单重复的 9 字段 + `kind` + `pbConfig` 抽成共享 `ProjectFields`/`projectFormSchema`（kind/pbConfig 折入 rhf，单一 onSubmit），让 `CreateProjectForm` 与 `EditProjectForm` 各自渲染同一 `<ProjectFields/>`；把 `WorkflowForm` 抽出 `workflowFormSchema`（`findGraphError` + 4 条图校验经 `.superRefine` 逐字复刻）并改 rhf+`Controller`。两处提交 payload / 校验文案 / 视觉逐字逐像素不变。

**Architecture（务实路线，明确不照搬公共壳）：** 复用 SP-A 的 **rhf+zod 数据层模式**（`schema: ZodType<T,T>` + `zodResolver` + 单 `FormProvider` + `Controller` 委托复杂字段），但**不采用** SP-A 的 `FormDialog`/`CrudResourcePage` 公共壳。原因（裁定，非偏差）：
- 每个 `*Dialog` 都有 `trigger`+内部 `open` 的公共 API，被 3 个消费方依赖；`FormDialog` 自带 `<Dialog>`，套进 `*Dialog` 会变成**双 Dialog 嵌套**。
- 每个 `*Form` 是测试直接渲染的**无 Dialog 壳纯表单导出**；若 `*Form` 自身变成 `FormDialog`，测试 render 后字段会被关进 Radix Portal。
- 因此**去重的落点是字段与 schema**（`ProjectFields` + `projectFormSchema`），不是 Dialog 壳。每个 `*Dialog` **保留自有 `trigger`+`<Dialog>` 壳与公共 API**；`CreateProjectForm`/`EditProjectForm` 各自渲染共享 `<ProjectFields/>` 并使用共享 `projectFormSchema`；测试继续直接渲染内层 `*Form` 导出。零视觉/行为变化。

**Tech Stack:** React 19 + TypeScript、TanStack Query、react-hook-form + zod（`schema: ZodType<T,T>` + `zodResolver(schema) as unknown as Resolver<T>` cast 桥接，见 `FormDialog.tsx:43` 同款写法）、shadcn (`@/components/ui/*`)、studio `Button` (`@/components/studio/Button`)、Vitest + Testing Library + userEvent、sonner toast。

**约定（所有任务通用）：**
- 工作目录 `web/`（`/home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web`）。分支 `refactor/sp-b-project-asset-formdialog` 已 checkout。
- 单测：`npx vitest run <file>`；类型：`npx tsc --noEmit`（本仓 `tsc -b` 亦可，验收统一用 `--noEmit`）；lint：`npx eslint <dir>`。`-count` 不适用（vitest 默认不缓存）。
- 迁移时**保留现有测试的全部断言**，仅在 markup 真变化时调选择器；**绝不弱化断言**。
- **绝不新增今天不存在的校验**（尤其绘本配置——现状零前端校验，见硬约束 #7）。
- 每个 Task 末尾 commit；commit message 说「为什么」。

---

## ⚠️ 实现者必读：本计划独有的硬约束（违反即回归）

1. **`*Form` 导出必须保留且测试直接渲染它**。`projects.test.tsx` 渲染 `CreateProjectForm`、`EditProjectDialog.test.tsx` 渲染 `EditProjectForm`、`WorkflowDialog.test.tsx` 渲染 `WorkflowForm`——都是**无 Dialog 壳的纯表单导出**。迁移后这些导出**仍须存在、签名不变、单独渲染时表单可见可提交**。因此 `*Form` 不能直接「变成 `FormDialog`」（那会引入 Dialog 壳，字段被关进 Radix Portal）。

2. **`*Dialog` 的 `trigger`+内部 open 公共 API 必须保留**。消费方 `ProjectListPage.tsx`、`routes/_authed/orgs.$org.projects.$id.index.tsx`（2 处 WorkflowDialog）、`routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx`（1 处 EditProjectDialog）都用 `<XxxDialog trigger={...} .../>`。**不改这些消费方**。`*Dialog` 继续自持 `open` state + `<Dialog>` + `DialogTrigger`，内部渲染对应 `*Form`。**不引入 `FormDialog`**（避免双 Dialog）。

3. **`pictureBookConfig` 是 JSON 字符串字段**（`CreateProjectInput.pictureBookConfig?: string`、Edit 提交对象的 `pictureBookConfig: string`）。折入 rhf 后，提交时**必须 `JSON.stringify(pbConfig)`**——绝不能把对象直接放进 payload（曾因传对象导致 400）。Create 标准项目时连 `kind`/`pictureBookConfig` 都不带（保持 `...(kind==="picturebook" ? {...} : {})` 的条件展开）；Edit 标准项目时 `pictureBookConfig: ""`、`kind` 始终带。**提交 payload 与现状逐字节一致。**

4. **Create 与 Edit 的 payload 形状不同**，不能强行用同一 schema 的同一 output 直接当 payload：
   - Create：字段名 `brief`、规划/图片模型用「空则不带」条件展开、绘本「非绘本不带」。
   - Edit：字段名 `description`（不是 brief）、多 `storageConfigId`、模型字段**始终带**（即使空串）、`kind` 始终带、`pictureBookConfig` 始终带（标准为 `""`）。
   `projectFormSchema` 统一 rhf 表单字段，但**每个 Dialog 自己的 `onSubmit` 负责把表单值映射成各自的后端 payload**（保留各自原有映射逻辑逐字不变）。

5. **工作流 4 条校验文案逐字复刻**（见 `WorkflowDialog.tsx:95-120`）：`"请输入工作流名称"` / `"工作流必须包含至少一个节点"` / `"所有节点 ID 不能为空"` / `` `存在重复的节点 ID: ${n.id}` `` / `findGraphError` 的返回串。`findGraphError` 导出保留（`findGraphError` 被 `WorkflowDialog.test.tsx` 独立 import 测试）。

6. **`zod` 已是 v4、rhf v7、`@hookform/resolvers` v5**。schema 用 `z.object({...})`，类型用 `z.infer<typeof schema>`。`zodResolver(schema) as unknown as Resolver<T>` 桥接（与 `FormDialog.tsx` 同款 cast）。

7. **不新增前端校验。** 现状 Create/Edit 的绘本配置**无任何前端必填校验**（用户可不选年龄段直接提交）。`projectFormSchema` **不得**加「请选择年龄段」之类的 `.superRefine`——`kind`(enum) + `pbConfig`(对象) 仅为「折入 rhf 做单一 onSubmit」而存在，**不携带任何现状没有的校验**。已有的 `name/brief/style` 等必填仅复刻现状（后端缺则 400、且测试已断言拦截）。

---

## File Structure（先锁定）

| 文件 | 状态 | 职责 |
|---|---|---|
| `src/features/projects/ProjectFields.schema.ts` | 新建 | `projectFormSchema`（name/brief/description/contentType/targetPlatform/style/4 模型字段/storageConfigId/`kind` enum/`pbConfig` 对象，**无 picturebook 新校验**）+ `ProjectFormValues` 类型 + `defaultsFor(initial?)` |
| `src/features/projects/ProjectFields.tsx` | 新建 | `ProjectFields` 组件（仅组件导出，防 react-refresh）。`useFormContext` 渲染所有 rhf 字段 + kind 切换 + 经 `Controller` 接 `PictureBookConfigForm`。props 控制可见字段差异（id 前缀、brief 绑哪个字段、是否显存储下拉、`alwaysShowPlanner`） |
| `src/features/projects/ProjectFields.schema.test.ts` | 新建 | schema 校验单测（name/brief/style 必填、standard/picturebook 均通过、defaultsFor 回填/解析） |
| `src/features/projects/ProjectFields.test.tsx` | 新建 | 组件单测（kind 切换显隐 `PictureBookConfigForm`、Controller 委托、`alwaysShowPlanner` 显隐） |
| `src/features/projects/CreateProjectDialog.tsx` | 改写 | `CreateProjectForm` 改用 `ProjectFields`+schema（rhf `FormProvider` 自持，无 Dialog 壳，供测试直接渲染）；`CreateProjectDialog` 自持 open+trigger+`<Dialog>`，内部渲染 `CreateProjectForm`（**不引入 FormDialog**） |
| `src/features/projects/EditProjectDialog.tsx` | 改写 | 同上模式，复用 `ProjectFields`；保留 `description`/`storageConfigId`/始终带模型字段的映射 + 风格补项；`alwaysShowPlanner` 传 true |
| `src/features/projects/WorkflowDialog.schema.ts` | 新建 | `workflowFormSchema`（`name`+`nodes`，`.superRefine` 复刻 4 条校验，复用 `findGraphError`）+ `WorkflowFormValues` 类型 + `findGraphError` |
| `src/features/projects/WorkflowDialog.tsx` | 改写 | `findGraphError` 移到 schema 并 re-export 保留；`WorkflowForm` 用 rhf `FormProvider`，`nodes` 经 `Controller` 接 `WorkflowNodesEditor`；`WorkflowDialog` 自持 open+trigger+`<Dialog>`（**不引入 FormDialog**） |

**不动的文件**：`PictureBookConfigForm.tsx`、`pbConfig.ts`、`WorkflowNodesEditor.tsx`、`AssetGalleryModal`、`AssetThumb`、`AssetMedia`、`CoverDialog.tsx`、`ProjectListPage.tsx`（消费方，验收时仅确认仍编译）、所有路由文件、`features/*/api.ts`、**`src/features/library/LibraryPage.tsx`（资产库整体不动，见末尾「Library 暂缓说明」）**。

---

## Task 1: 抽 ProjectFields.schema.ts + ProjectFields.tsx

把 Create/Edit 重复的 9 字段 + `kind` + `pbConfig` 抽成共享 schema 与组件。**kind 与 pbConfig 折入 rhf**（消除现有 `useState`），提交时由各 Dialog 映射成各自 payload。**不新增任何现状没有的校验**（见硬约束 #7）。

**Files:**
- Create: `src/features/projects/ProjectFields.schema.ts`
- Create: `src/features/projects/ProjectFields.schema.test.ts`
- Create: `src/features/projects/ProjectFields.tsx`
- Create: `src/features/projects/ProjectFields.test.tsx`

- [x] **Step 1: 写 schema 的失败测试**

```ts
// src/features/projects/ProjectFields.schema.test.ts
import { describe, it, expect } from "vitest"
import { projectFormSchema, defaultsFor } from "./ProjectFields.schema"
import { emptyPictureBookConfig } from "./pbConfig"

describe("projectFormSchema", () => {
  const base = {
    name: "项目",
    brief: "一句创意",
    description: "",
    contentType: "短视频",
    targetPlatform: "抖音",
    style: "写实",
    plannerProvider: "",
    plannerModel: "",
    imageProvider: "",
    imageModel: "",
    storageConfigId: "",
    kind: "standard" as const,
    pbConfig: emptyPictureBookConfig,
  }

  it("name 必填", () => {
    const r = projectFormSchema.safeParse({ ...base, name: "" })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入项目名称")).toBe(true)
    }
  })

  it("brief 必填", () => {
    const r = projectFormSchema.safeParse({ ...base, brief: "" })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入创意需求")).toBe(true)
    }
  })

  it("style 必填", () => {
    const r = projectFormSchema.safeParse({ ...base, style: "" })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请选择风格")).toBe(true)
    }
  })

  it("standard 模式合法", () => {
    const r = projectFormSchema.safeParse(base)
    expect(r.success).toBe(true)
  })

  it("picturebook 模式不要求选年龄段——空年龄段仍通过（复刻现状零前端校验）", () => {
    const r = projectFormSchema.safeParse({
      ...base,
      kind: "picturebook",
      pbConfig: { ...emptyPictureBookConfig, ageBand: "" },
    })
    expect(r.success).toBe(true)
  })
})

describe("defaultsFor", () => {
  it("无 initial 时给空表单默认（standard + 空 pbConfig）", () => {
    const d = defaultsFor()
    expect(d.kind).toBe("standard")
    expect(d.name).toBe("")
    expect(d.brief).toBe("")
    expect(d.pbConfig).toEqual(emptyPictureBookConfig)
  })

  it("有 initial 时回填（含 picturebook + 解析 pictureBookConfig 字符串）", () => {
    const d = defaultsFor({
      name: "旧名",
      description: "旧需求",
      contentType: "广告片",
      targetPlatform: "B 站",
      style: "动画",
      kind: "picturebook",
      pictureBookConfig: JSON.stringify({
        ...emptyPictureBookConfig,
        ageBand: "0-3",
        pageCount: 8,
      }),
    })
    expect(d.name).toBe("旧名")
    expect(d.description).toBe("旧需求")
    expect(d.kind).toBe("picturebook")
    expect(d.pbConfig.ageBand).toBe("0-3")
    expect(d.pbConfig.pageCount).toBe(8)
  })

  it("pictureBookConfig 非法 JSON 回退空配置", () => {
    const d = defaultsFor({ pictureBookConfig: "{bad" })
    expect(d.pbConfig).toEqual(emptyPictureBookConfig)
  })
})
```

- [x] **Step 2: 跑测试确认失败**

Run: `npx vitest run src/features/projects/ProjectFields.schema.test.ts`
Expected: FAIL（`Cannot find module './ProjectFields.schema'`）。

- [x] **Step 3: 写 ProjectFields.schema.ts**

> 说明：`pbConfig` 用宽松对象 schema（沿用 `PictureBookConfig` 形状，不做逐字段强约束——现状本就无逐字段校验）。**不加任何 `.superRefine`**（硬约束 #7：现状绘本无前端必填校验，本重构不引入）。`kind`(enum) 与 `pbConfig`(对象) 仅为「折入 rhf 做单一 onSubmit」而存在。`description` 字段并入 schema（Create 不用、Edit 用），保持单一表单模型。`name/brief/style/contentType/targetPlatform` 必填复刻现状（后端缺则 400，且 Create/Edit 测试已断言拦截 name/style）。

```ts
// src/features/projects/ProjectFields.schema.ts
import { z } from "zod"
import type { PictureBookConfig, Project } from "@/lib/types"
import { emptyPictureBookConfig } from "./pbConfig"

// 绘本配置子 schema：形状对齐 PictureBookConfig；不做逐字段强约束（现状即无）。
const pbConfigSchema: z.ZodType<PictureBookConfig> = z.object({
  ageBand: z.enum(["", "0-3", "3-6", "6-8"]),
  bookType: z.string(),
  illustrationStyle: z.string(),
  narrationStyle: z.string(),
  themes: z.array(z.string()),
  pageCount: z.number(),
  voice: z.string(),
})

// 项目创建/编辑共享表单模型。把 kind（项目类型）与 pbConfig（绘本配置）
// 折入 rhf（原来是各 Dialog 的本地 useState），让单一 onSubmit 拿到全量。
// name/brief/style 必填（后端缺则 400）；description（Edit 用）与各模型字段为可空字符串。
// 注意：不对 pbConfig/kind 加任何 superRefine——现状绘本配置无前端必填校验，本重构不引入新校验。
export const projectFormSchema = z.object({
  name: z.string().min(1, "请输入项目名称"),
  brief: z.string().min(1, "请输入创意需求"),
  description: z.string(),
  contentType: z.string().min(1, "请选择内容类型"),
  targetPlatform: z.string().min(1, "请选择目标平台"),
  style: z.string().min(1, "请选择风格"),
  plannerProvider: z.string(),
  plannerModel: z.string(),
  imageProvider: z.string(),
  imageModel: z.string(),
  storageConfigId: z.string(),
  kind: z.enum(["standard", "picturebook"]),
  pbConfig: pbConfigSchema,
})

export type ProjectFormValues = z.infer<typeof projectFormSchema>

// 编辑时从 project.pictureBookConfig（原始 JSON）解析；空/解析失败回退空配置。
function parsePbConfig(raw?: string): PictureBookConfig {
  if (!raw) return emptyPictureBookConfig
  try {
    const parsed = JSON.parse(raw) as Partial<PictureBookConfig>
    return { ...emptyPictureBookConfig, ...parsed }
  } catch {
    return emptyPictureBookConfig
  }
}

// initial 项目 → 表单默认值。无 initial = 空表单（新建）。
// 注意：style 默认空串——CreateProjectForm 会在 initial.style 缺省时用 styles[0]?.name
// 覆盖（保留现状「默认选首个风格」UX），故此处不擅自填首风格。
export function defaultsFor(
  initial?: Partial<Project> & { brief?: string },
): ProjectFormValues {
  return {
    name: initial?.name ?? "",
    brief: initial?.brief ?? "",
    description: initial?.description ?? "",
    contentType: initial?.contentType ?? "短视频",
    targetPlatform: initial?.targetPlatform ?? "抖音",
    style: initial?.style ?? "",
    plannerProvider: initial?.plannerProvider ?? "",
    plannerModel: initial?.plannerModel ?? "",
    imageProvider: initial?.imageProvider ?? "",
    imageModel: initial?.imageModel ?? "",
    storageConfigId: initial?.storageConfigId ?? "",
    kind: initial?.kind === "picturebook" ? "picturebook" : "standard",
    pbConfig: parsePbConfig(initial?.pictureBookConfig),
  }
}
```

> ⚠️ 类型注意：`Project` 类型当前**没有** `brief` 字段（仅 `CreateProjectInput` 有 `brief`；`Project` 有 `description`）。`defaultsFor` 的 `initial?.brief` 在传 `Project` 时恒为 `undefined`→`""`，这是预期（Edit 用 `description` 不用 `brief`）。故参数类型放宽为 `Partial<Project> & { brief?: string }`（已在上方代码中体现）。

- [x] **Step 4: 跑 schema 测试确认通过**

Run: `npx vitest run src/features/projects/ProjectFields.schema.test.ts`
Expected: PASS（projectFormSchema 5 + defaultsFor 3 = 8 passed）。

- [x] **Step 5: 写 ProjectFields 组件的失败测试**

> `ProjectFields` 用 `useFormContext`，测试需用一个最小 rhf wrapper 包裹（`FormProvider`）。kind 切换是 rhf 字段（用按钮 `onClick → setValue("kind", ...)`），切到 picturebook 渲染 `PictureBookConfigForm`（断言其标志性文案「年龄段」出现）。`alwaysShowPlanner` 用例验证：无 `textModels` 时传 `alwaysShowPlanner` 仍渲染规划下拉（保留 Edit 现状）。

```tsx
// src/features/projects/ProjectFields.test.tsx
import { describe, it, expect } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { FormProvider, useForm } from "react-hook-form"
import { ProjectFields, type ProjectFieldsProps } from "./ProjectFields"
import { defaultsFor, type ProjectFormValues } from "./ProjectFields.schema"
import type { Style } from "@/lib/types"

const STYLES: Style[] = [
  { name: "写实", suffix: "" },
  { name: "动画", suffix: "" },
]

function Harness({
  initial,
  ...props
}: {
  initial?: Parameters<typeof defaultsFor>[0]
} & Partial<ProjectFieldsProps>) {
  const form = useForm<ProjectFormValues>({
    defaultValues: defaultsFor(initial),
  })
  return (
    <FormProvider {...form}>
      <ProjectFields styles={STYLES} fieldIdPrefix="t" {...props} />
    </FormProvider>
  )
}

describe("ProjectFields", () => {
  it("默认 standard 模式不渲染绘本配置", () => {
    render(<Harness />)
    expect(screen.getByLabelText("项目名称")).toBeInTheDocument()
    // 绘本配置的标志文案「年龄段」不出现。
    expect(screen.queryByText("年龄段")).not.toBeInTheDocument()
  })

  it("点「儿童绘本」切到 picturebook 模式并展开 PictureBookConfigForm", async () => {
    const user = userEvent.setup()
    render(<Harness />)
    await user.click(screen.getByRole("button", { name: "儿童绘本" }))
    // PictureBookConfigForm 经 Controller 渲染——其「年龄段」label 出现。
    expect(await screen.findByText("年龄段")).toBeInTheDocument()
  })

  it("initial 为 picturebook 时直接展开绘本配置并回填年龄段", () => {
    render(
      <Harness
        initial={{
          kind: "picturebook",
          pictureBookConfig: JSON.stringify({
            ageBand: "3-6",
            bookType: "narrative",
            illustrationStyle: "",
            narrationStyle: "plain",
            themes: [],
            pageCount: 16,
            voice: "",
          }),
        }}
      />,
    )
    expect(screen.getByText("年龄段")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "3-6" })).toBeInTheDocument()
  })

  it("alwaysShowPlanner 时即使无 textModels 也渲染规划下拉（保留 Edit 现状）", () => {
    render(<Harness alwaysShowPlanner project={{} as never} />)
    expect(screen.getByLabelText(/规划用模型/)).toBeInTheDocument()
  })

  it("不传 alwaysShowPlanner 且无 textModels 时不渲染规划下拉（保留 Create 现状）", () => {
    render(<Harness />)
    expect(screen.queryByLabelText(/规划用模型/)).not.toBeInTheDocument()
  })
})
```

- [x] **Step 6: 跑测试确认失败**

Run: `npx vitest run src/features/projects/ProjectFields.test.tsx`
Expected: FAIL（`Cannot find module './ProjectFields'`）。

- [x] **Step 7: 写 ProjectFields.tsx**

> 设计：`ProjectFields` 只导出 React 组件 + 其 props 类型（schema/常量在 `.schema.ts`，避免 react-refresh lint）。它用 `useFormContext<ProjectFormValues>()`，渲染 name/brief(or description)/kind 切换/picturebook 配置/contentType/targetPlatform/style/规划模型/图片模型/存储配置。
>
> Create 与 Edit 的字段差异用 props 表达，**不在组件里硬编码**：
> - `fieldIdPrefix`: id 前缀（避免两实例 id 冲突）。测试按中文 label 选（不按 id），故 id 仅需保证唯一。
> - `briefFieldName`: `"brief"`（Create）或 `"description"`（Edit）——「创意需求」textarea 绑哪个 rhf 字段。
> - `briefRequired`: Create 的 brief 必填（显示错误）；Edit 的 description 不显示必填错误（现状 Edit 无 description 校验）。
> - `alwaysShowPlanner`: Edit 传 true——即使无 text 模型也渲染规划下拉（复刻 Edit 现状：规划下拉无条件显示）；Create 不传（仅有 text 模型时显示，复刻 Create 现状）。
> - `textModels` / `imageModels` / `storageConfigs` / `project`(用于 Edit 的「当前」提示与风格补项)。
>
> ⚠️ **保留逐字现状**：内容类型/目标平台常量、风格补项逻辑（Edit）、模型下拉的 `__default__` 哨兵 + `provider::model` 编码、各「当前：…」提示文案、`PictureBookConfigForm` 经 `Controller name="pbConfig"` 绑定。kind 切换按钮样式逐字照搬。**无 `errors.pbConfig?.ageBand` 错误显示块**（硬约束 #7：无该校验）。

```tsx
// src/features/projects/ProjectFields.tsx
import { Controller, useFormContext } from "react-hook-form"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { cn } from "@/lib/utils"
import type {
  ModelConfig,
  Project,
  StorageConfig,
  Style,
} from "@/lib/types"
import { MODE_LABELS } from "@/features/storage/StorageConfigPage"
import { PictureBookConfigForm } from "./PictureBookConfigForm"
import type { ProjectFormValues } from "./ProjectFields.schema"

// 内容类型 / 目标平台为前端枚举（后端只存字符串，无白名单约束）。
const CONTENT_TYPES = ["短视频", "广告片", "动画", "宣传片"] as const
const TARGET_PLATFORMS = ["抖音", "视频号", "B 站", "小红书", "通用"] as const

export interface ProjectFieldsProps {
  styles: Style[]
  // id 前缀，避免 Create/Edit 同时挂载时 id 冲突。Create 传 "create" / Edit 传 "edit"。
  fieldIdPrefix: string
  // 「创意需求」textarea 绑的 rhf 字段：Create=brief（必填），Edit=description（不校验）。
  briefFieldName?: "brief" | "description"
  briefRequired?: boolean
  // Edit=true：即使无 text 模型也渲染规划下拉（复刻 Edit 现状）。Create 不传（仅有模型时显示）。
  alwaysShowPlanner?: boolean
  textModels?: ModelConfig[]
  imageModels?: ModelConfig[]
  storageConfigs?: StorageConfig[]
  // Edit 用：风格补项 + 各「当前：…」提示。
  project?: Project
}

// 项目创建/编辑共享字段块（经 useFormContext 读写）。
// 字段差异由 props 表达——同一组件供 Create / Edit 复用，呈现/行为各自不变。
export function ProjectFields({
  styles,
  fieldIdPrefix,
  briefFieldName = "brief",
  briefRequired = true,
  alwaysShowPlanner = false,
  textModels,
  imageModels,
  storageConfigs,
  project,
}: ProjectFieldsProps) {
  const {
    register,
    control,
    watch,
    setValue,
    formState: { errors },
  } = useFormContext<ProjectFormValues>()

  const pre = (s: string) => `${fieldIdPrefix}-${s}`
  const kind = watch("kind")

  // Edit 风格补项：项目当前风格若不在 styles 列表，补一项避免回显丢失。
  const styleOptions = styles
  const hasCurrentStyle =
    !project?.style || styleOptions.some((s) => s.name === project.style)

  // 规划下拉显示条件：Edit（alwaysShowPlanner）无条件显示；Create 仅有 text 模型时显示。
  const showPlanner = alwaysShowPlanner || (textModels != null && textModels.length > 0)
  const plannerModels = textModels ?? []

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label htmlFor={pre("name")}>项目名称</Label>
        <Input id={pre("name")} aria-invalid={errors.name != null} {...register("name")} />
        {errors.name && <p className="text-[12px] text-danger">{errors.name.message}</p>}
      </div>

      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label htmlFor={pre("brief")}>创意需求</Label>
        <Textarea
          id={pre("brief")}
          rows={2}
          placeholder="用一句话描述你想要的作品"
          aria-invalid={briefRequired && errors.brief != null}
          {...register(briefFieldName)}
        />
        {briefRequired && errors.brief && (
          <p className="text-[12px] text-danger">{errors.brief.message}</p>
        )}
      </div>

      {/* 项目类型：标准 / 儿童绘本。选绘本展开 PictureBookConfigForm。 */}
      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label>项目类型</Label>
        <div className="flex gap-2">
          {(
            [
              { v: "standard", label: "标准" },
              { v: "picturebook", label: "儿童绘本" },
            ] as const
          ).map((opt) => (
            <button
              key={opt.v}
              type="button"
              onClick={() => setValue("kind", opt.v, { shouldValidate: false })}
              className={cn(
                "rounded-md border px-4 py-[7px] text-[13px] font-medium transition-colors",
                kind === opt.v
                  ? "border-amber bg-amber text-[#1a1408]"
                  : "border-line text-text-2 hover:border-text-3 hover:text-text-1",
              )}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>

      {kind === "picturebook" && (
        <div className="sm:col-span-2">
          <Controller
            control={control}
            name="pbConfig"
            render={({ field }) => (
              <PictureBookConfigForm value={field.value} onChange={field.onChange} />
            )}
          />
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={pre("contentType")}>内容类型</Label>
        <Controller
          control={control}
          name="contentType"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id={pre("contentType")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CONTENT_TYPES.map((ct) => (
                  <SelectItem key={ct} value={ct}>
                    {ct}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={pre("targetPlatform")}>目标平台</Label>
        <Controller
          control={control}
          name="targetPlatform"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id={pre("targetPlatform")}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TARGET_PLATFORMS.map((tp) => (
                  <SelectItem key={tp} value={tp}>
                    {tp}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor={pre("style")}>风格</Label>
        <Controller
          control={control}
          name="style"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id={pre("style")} aria-invalid={errors.style != null}>
                <SelectValue placeholder="选择风格" />
              </SelectTrigger>
              <SelectContent>
                {!hasCurrentStyle && project?.style && (
                  <SelectItem value={project.style}>{project.style}</SelectItem>
                )}
                {styleOptions.map((s) => (
                  <SelectItem key={s.name} value={s.name}>
                    {s.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
        {errors.style && <p className="text-[12px] text-danger">{errors.style.message}</p>}
      </div>

      {/* 规划模型下拉：Edit 无条件显示（alwaysShowPlanner），Create 仅有 text 模型时显示。 */}
      {showPlanner && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={pre("plannerModel")}>
            {project ? "规划用模型" : "规划用模型（可选）"}
          </Label>
          <ModelPairSelect
            triggerId={pre("plannerModel")}
            models={plannerModels}
            providerName="plannerProvider"
            modelName="plannerModel"
          />
          {project ? (
            <p className="text-[11.5px] text-text-3">
              当前：{project.plannerProvider && project.plannerModel
                ? `${project.plannerProvider} · ${project.plannerModel}`
                : "组织默认"}。保存后下次 run 起生效。
            </p>
          ) : (
            <p className="text-[11.5px] text-text-3">
              留空 = 走组织默认；选某个模型则本次及后续 run 都用该模型。
            </p>
          )}
        </div>
      )}

      {imageModels && imageModels.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={pre("imageModel")}>
            {project ? "图片生成模型" : "图片生成模型（可选）"}
          </Label>
          <ModelPairSelect
            triggerId={pre("imageModel")}
            models={imageModels}
            providerName="imageProvider"
            modelName="imageModel"
          />
          {project ? (
            <p className="text-[11.5px] text-text-3">
              当前：{project.imageProvider && project.imageModel
                ? `${project.imageProvider} · ${project.imageModel}`
                : "组织默认"}。保存后下次 run 起生效。
            </p>
          ) : (
            <p className="text-[11.5px] text-text-3">
              留空 = 走组织默认；选某个模型则本次及后续 run 都用该模型生成图片。
            </p>
          )}
        </div>
      )}

      {/* 存储配置下拉：仅 Edit 传 storageConfigs 时渲染。 */}
      {storageConfigs && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor={pre("storageConfigId")}>存储配置</Label>
          <Controller
            control={control}
            name="storageConfigId"
            render={({ field }) => (
              <Select
                value={field.value || "__default__"}
                onValueChange={(v) => field.onChange(v === "__default__" ? "" : v)}
              >
                <SelectTrigger id={pre("storageConfigId")} aria-invalid={false}>
                  <SelectValue placeholder="继承组织默认" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default__">继承组织默认</SelectItem>
                  {storageConfigs.filter((c) => c.enabled).map((c) => (
                    <SelectItem key={c.id} value={c.id}>
                      {c.name}（{MODE_LABELS[c.mode]}）
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          />
          <p className="text-[11.5px] text-text-3">
            当前：{(() => {
              if (!project?.storageConfigId) return "继承组织默认"
              const c = storageConfigs.find((c) => c.id === project.storageConfigId)
              return c ? `${c.name}（${MODE_LABELS[c.mode]}）` : project.storageConfigId
            })()}。保存后下一次资源生成或加载起生效。
          </p>
        </div>
      )}
    </div>
  )
}

// provider+model 成对下拉（规划/图片共用）。__default__ = 空（走 org 默认），
// 其余编码为 `${provider}::${model}`。保留现状双 Controller 嵌套语义。
function ModelPairSelect({
  triggerId,
  models,
  providerName,
  modelName,
}: {
  triggerId: string
  models: ModelConfig[]
  providerName: "plannerProvider" | "imageProvider"
  modelName: "plannerModel" | "imageModel"
}) {
  const { control } = useFormContext<ProjectFormValues>()
  return (
    <Controller
      control={control}
      name={providerName}
      render={({ field: provField }) => (
        <Controller
          control={control}
          name={modelName}
          render={({ field: modField }) => (
            <Select
              value={
                provField.value && modField.value
                  ? `${provField.value}::${modField.value}`
                  : "__default__"
              }
              onValueChange={(v) => {
                if (v === "__default__") {
                  provField.onChange("")
                  modField.onChange("")
                  return
                }
                const sep = v.indexOf("::")
                if (sep < 0) return
                provField.onChange(v.slice(0, sep))
                modField.onChange(v.slice(sep + 2))
              }}
            >
              <SelectTrigger id={triggerId} aria-invalid={false}>
                <SelectValue placeholder="使用组织默认" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">使用组织默认</SelectItem>
                {models.map((m) => {
                  const key = `${m.provider}::${m.model}`
                  return (
                    <SelectItem key={key} value={key}>
                      {m.provider} · {m.model}
                      {m.isDefault ? "（默认）" : ""}
                    </SelectItem>
                  )
                })}
              </SelectContent>
            </Select>
          )}
        />
      )}
    />
  )
}
```

- [x] **Step 8: 跑组件测试确认通过**

Run: `npx vitest run src/features/projects/ProjectFields.test.tsx`
Expected: PASS（5 passed）。若「年龄段」label 文案与 `PictureBookConfigForm` 实际不符，以 `PictureBookConfigForm.tsx` 的 `<Label>年龄段</Label>` 为准（已确认存在）。

- [x] **Step 9: 类型 + lint 自检**

Run: `npx tsc --noEmit`
Expected: 无错。
Run: `npx eslint src/features/projects/ProjectFields.tsx src/features/projects/ProjectFields.schema.ts`
Expected: 无 NEW 错误（schema 在 `.schema.ts` → 无 react-refresh 警告；`ProjectFields.tsx` 仅导出组件 + props 类型，类型导出不触发 react-refresh）。

- [x] **Step 10: Commit**

```bash
git add src/features/projects/ProjectFields.schema.ts src/features/projects/ProjectFields.schema.test.ts src/features/projects/ProjectFields.tsx src/features/projects/ProjectFields.test.tsx
git commit -m "feat(projects): 抽共享 ProjectFields + projectFormSchema(折 kind/pbConfig 入 rhf,无新校验)——为 Create/Edit 复用做准备"
```

---

## Task 2: CreateProjectForm 改用共享 ProjectFields（Dialog 壳与公共 API 不变）

`CreateProjectForm`（测试直接渲染的纯表单）改用 rhf `FormProvider` + `<ProjectFields/>`，提交映射逻辑**逐字保留**（空模型不带、绘本带 `JSON.stringify`、标准不带 kind/pbConfig）。`CreateProjectDialog` 保留自有 `open`+`trigger`+`<Dialog>` 壳与公共 API，内部渲染 `CreateProjectForm`（**不引入 `FormDialog`**，硬约束 #2）。

**Files:**
- Modify: `src/features/projects/CreateProjectDialog.tsx`
- 测试 `src/features/projects/projects.test.tsx` 的 `CreateProjectForm` 用例**保留全部断言**（仅在 markup 变化时调选择器）。

- [x] **Step 1: 先跑现有测试建立基线**

Run: `npx vitest run src/features/projects/projects.test.tsx`
Expected: PASS（现状全绿；记录 `CreateProjectForm` 用例数 + `ProjectListView` 用例数）。

- [x] **Step 2: 重写 CreateProjectDialog.tsx**

> 关键：`CreateProjectForm` 不再自管 `kind`/`pbConfig` 的 `useState`，改由 rhf（`defaultsFor`）。提交时把 rhf 值映射成 `CreateProjectInput`——映射逻辑与现状 `CreateProjectDialog.tsx:106-132` **逐字一致**（空模型条件展开、绘本 `JSON.stringify(pbConfig)`、标准不带 kind/pbConfig）。`style` 默认值：现状是 `styles[0]?.name`——在 `defaultValues` 里覆盖 `defaultsFor()` 的空串。
>
> `CreateProjectForm` 内部用 `useForm` + `FormProvider` 包 `<ProjectFields/>`，自带 `<form>` + 提交按钮 + submitError（**不**用 `FormDialog`，因为测试直接渲染 `CreateProjectForm` 时字段需在 DOM 顶层可见，不能藏进 Dialog Portal）。`CreateProjectDialog` 保留自有 `<Dialog open trigger>` 壳，内部渲染 `CreateProjectForm`——字段去重落在 `ProjectFields`，公共 API（trigger）与现状一致，**无双 Dialog**。
>
> 映射逻辑抽成模块级 `toCreateInput(values)` 供 `CreateProjectForm` 调用。

```tsx
// src/features/projects/CreateProjectDialog.tsx
import { useState } from "react"
import { useForm, FormProvider, type Resolver } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { Loader2 } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Button } from "@/components/studio/Button"
import type {
  CreateProjectInput,
  ModelConfig,
  Project,
  Style,
} from "@/lib/types"
import { ProjectFields } from "./ProjectFields"
import {
  projectFormSchema,
  defaultsFor,
  type ProjectFormValues,
} from "./ProjectFields.schema"

// 表单值 → CreateProjectInput。空模型不带（= 后端无 override）；
// 绘本带 kind + JSON.stringify(pbConfig)（绝不传对象——曾因此 400）；标准不带 kind/pbConfig。
function toCreateInput(values: ProjectFormValues): CreateProjectInput {
  return {
    name: values.name,
    brief: values.brief,
    contentType: values.contentType,
    targetPlatform: values.targetPlatform,
    style: values.style,
    ...(values.plannerProvider && values.plannerModel
      ? { plannerProvider: values.plannerProvider, plannerModel: values.plannerModel }
      : {}),
    ...(values.imageProvider && values.imageModel
      ? { imageProvider: values.imageProvider, imageModel: values.imageModel }
      : {}),
    ...(values.kind === "picturebook"
      ? { kind: "picturebook" as const, pictureBookConfig: JSON.stringify(values.pbConfig) }
      : {}),
  }
}

export interface CreateProjectFormProps {
  styles: Style[]
  textModels?: ModelConfig[]
  imageModels?: ModelConfig[]
  onSubmit: (input: CreateProjectInput) => Promise<Project>
  onSuccess?: (project: Project) => void
}

// 无 Dialog 壳的纯表单（测试直接渲染）。提交调 onSubmit，成功调 onSuccess。
export function CreateProjectForm({
  styles,
  textModels,
  imageModels,
  onSubmit,
  onSuccess,
}: CreateProjectFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const resolver = zodResolver(projectFormSchema) as unknown as Resolver<ProjectFormValues>
  const form = useForm<ProjectFormValues>({
    resolver,
    // 默认选中首个风格（保留现状 UX：免去必填空态）。
    defaultValues: { ...defaultsFor(), style: styles[0]?.name ?? "" },
  })

  const submit = form.handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const project = await onSubmit(toCreateInput(values))
      onSuccess?.(project)
    } catch {
      setSubmitError("创建失败，请重试")
    }
  })

  return (
    <FormProvider {...form}>
      <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <ProjectFields
          styles={styles}
          fieldIdPrefix="create"
          briefFieldName="brief"
          briefRequired
          textModels={textModels}
          imageModels={imageModels}
        />
        {submitError && (
          <p role="alert" className="text-[12px] text-danger">
            {submitError}
          </p>
        )}
        <DialogFooter>
          <Button type="submit" variant="amber" disabled={form.formState.isSubmitting}>
            {form.formState.isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            创建
          </Button>
        </DialogFooter>
      </form>
    </FormProvider>
  )
}

export interface CreateProjectDialogProps extends CreateProjectFormProps {
  trigger: React.ReactNode
}

// Dialog 壳：trigger 打开，创建成功后自动关闭并透传 onSuccess。
// 保留自有 <Dialog open trigger>（公共 API 不变），内部渲染 CreateProjectForm——不引入 FormDialog（避免双 Dialog）。
export function CreateProjectDialog({
  trigger,
  styles,
  textModels,
  imageModels,
  onSubmit,
  onSuccess,
}: CreateProjectDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[90vh] w-[95vw] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>新建项目</DialogTitle>
          <DialogDescription>用一句创意需求开始你的第一支作品。</DialogDescription>
        </DialogHeader>
        <CreateProjectForm
          styles={styles}
          textModels={textModels}
          imageModels={imageModels}
          onSubmit={onSubmit}
          onSuccess={(project) => {
            setOpen(false)
            onSuccess?.(project)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
```

> 注：本文件**不导入 `FormDialog`**。「去重」体现在字段统一走 `ProjectFields`+`projectFormSchema`；Dialog 壳与 trigger 公共 API 逐字保留（务实路线，非偏差——见 Architecture 节裁定）。

- [x] **Step 3: 跑测试 + 按需调选择器**

Run: `npx vitest run src/features/projects/projects.test.tsx`
Expected: PASS。`CreateProjectForm` 用例的断言（`getByLabelText("项目名称")`/`"创意需求"`、提交带 `name/brief/style/contentType/targetPlatform`、规划下拉显隐、默认不带 override）应**无需改选择器**（label 文案与 `register("brief")` 字段不变；`style` 默认仍是 `styles[0].name`）。

- [x] **Step 4: 类型 + lint**

Run: `npx tsc --noEmit`（无错）
Run: `npx eslint src/features/projects/CreateProjectDialog.tsx`（无 NEW 错误）

- [x] **Step 5: Commit**

```bash
git add src/features/projects/CreateProjectDialog.tsx
git commit -m "refactor(projects): CreateProjectForm 改用共享 ProjectFields+schema(kind/pbConfig 入 rhf),提交映射与 trigger API 逐字不变"
```

---

## Task 3: EditProjectForm → 复用 ProjectFields（含 alwaysShowPlanner）

`EditProjectForm` 改用 rhf `FormProvider` + `<ProjectFields/>`（带 Edit 差异 props：`briefFieldName="description"`、`alwaysShowPlanner`、`storageConfigs`、`project`）。提交映射**逐字保留**（`description`、`storageConfigId`、模型字段始终带、`kind` 始终带、绘本 `JSON.stringify` 否则 `""`）。`EditProjectDialog` 保留自有 trigger+open+`<Dialog>` 壳（**不引入 FormDialog**）。

**Files:**
- Modify: `src/features/projects/EditProjectDialog.tsx`
- 测试 `src/features/projects/EditProjectDialog.test.tsx` **保留全部断言**。

- [x] **Step 1: 先跑现有测试建立基线**

Run: `npx vitest run src/features/projects/EditProjectDialog.test.tsx`
Expected: PASS（5 用例：提交 name/description + 模型字段、清空 name 拦截、存储下拉项、选配置提交 sc1、改回默认提交 ""）。

- [x] **Step 2: 重写 EditProjectDialog.tsx**

> Edit 的 `onSubmit` 入参对象 shape 与现状逐字一致（见 `EditProjectDialog.tsx:81-94` 的 `onSubmit` 类型）。映射：`description` 取 rhf `description`、模型/`storageConfigId` 始终带、`kind` 始终带、`pictureBookConfig` = picturebook ? `JSON.stringify(pbConfig)` : `""`。default 经 `defaultsFor(project)`（Edit 的 brief 不用——`description` 经 `defaultsFor` 的 `initial.description` 回填）。
>
> ⚠️ **Edit 规划下拉显隐（决策 3，已定）**：现状 Edit 的「规划用模型」**无条件显示**（即使无 text 模型也渲染空下拉）。`ProjectFields` 通过 `alwaysShowPlanner` prop 表达此差异——Edit 传 `alwaysShowPlanner`（Create 不传）。规划下拉条件 `alwaysShowPlanner || (textModels?.length>0)`、models 用 `textModels ?? []`（已在 Task 1 的 `ProjectFields.tsx` 落地）。保留 Edit 现状无条件显示规划下拉。

```tsx
// src/features/projects/EditProjectDialog.tsx
import { useState } from "react"
import { useForm, FormProvider, type Resolver } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { Loader2 } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Button } from "@/components/studio/Button"
import type {
  ModelConfig,
  Project,
  StorageConfig,
  Style,
} from "@/lib/types"
import { ProjectFields } from "./ProjectFields"
import {
  projectFormSchema,
  defaultsFor,
  type ProjectFormValues,
} from "./ProjectFields.schema"

export interface EditProjectFormProps {
  project: Project
  textModels?: ModelConfig[]
  imageModels?: ModelConfig[]
  styles?: Style[]
  storageConfigs?: StorageConfig[]
  onSubmit: (input: {
    name: string
    description: string
    contentType: string
    targetPlatform: string
    style: string
    plannerProvider: string
    plannerModel: string
    imageProvider: string
    imageModel: string
    storageConfigId: string
    kind: "standard" | "picturebook"
    pictureBookConfig: string
  }) => Promise<Project>
  onSuccess?: (project: Project) => void
}

export function EditProjectForm({
  project,
  textModels,
  imageModels,
  styles,
  storageConfigs,
  onSubmit,
  onSuccess,
}: EditProjectFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const resolver = zodResolver(projectFormSchema) as unknown as Resolver<ProjectFormValues>
  const form = useForm<ProjectFormValues>({
    resolver,
    defaultValues: defaultsFor(project),
  })

  const submit = form.handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const updated = await onSubmit({
        name: values.name,
        description: values.description,
        contentType: values.contentType,
        targetPlatform: values.targetPlatform,
        style: values.style,
        plannerProvider: values.plannerProvider,
        plannerModel: values.plannerModel,
        imageProvider: values.imageProvider,
        imageModel: values.imageModel,
        storageConfigId: values.storageConfigId,
        kind: values.kind,
        // 标准项目不带绘本配置（发空串）；绘本项目序列化当前配置（绝不传对象）。
        pictureBookConfig:
          values.kind === "picturebook" ? JSON.stringify(values.pbConfig) : "",
      })
      onSuccess?.(updated)
    } catch {
      setSubmitError("更新失败，请重试")
    }
  })

  return (
    <FormProvider {...form}>
      <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <ProjectFields
          styles={styles ?? []}
          fieldIdPrefix="edit"
          briefFieldName="description"
          briefRequired={false}
          alwaysShowPlanner
          textModels={textModels}
          imageModels={imageModels}
          storageConfigs={storageConfigs}
          project={project}
        />
        {submitError && (
          <p role="alert" className="text-[12px] text-danger">
            {submitError}
          </p>
        )}
        <DialogFooter className="mt-2">
          <Button type="submit" variant="amber" disabled={form.formState.isSubmitting}>
            {form.formState.isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            保存
          </Button>
        </DialogFooter>
      </form>
    </FormProvider>
  )
}

export interface EditProjectDialogProps extends EditProjectFormProps {
  trigger: React.ReactNode
}

export function EditProjectDialog({ trigger, onSuccess, ...formProps }: EditProjectDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[90vh] w-[95vw] flex-col overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑项目信息</DialogTitle>
          <DialogDescription>
            基本信息即时生效；模型/存储改动影响后续所有 run，当前正在跑的 run 不受影响。
          </DialogDescription>
        </DialogHeader>
        <EditProjectForm
          {...formProps}
          onSuccess={(p) => {
            setOpen(false)
            onSuccess?.(p)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
```

- [x] **Step 3: 跑测试 + 调选择器**

Run: `npx vitest run src/features/projects/EditProjectDialog.test.tsx`
Expected: PASS。断言用 `getByLabelText("项目名称")`/`"创意需求"`（description 绑定）、`getByRole("combobox",{name:/存储配置/})`、提交对象 `name/description/contentType/style/plannerProvider/storageConfigId`——label 文案与字段映射均保留，**应无需调选择器**。`创意需求` textarea 现绑 `description`（`briefFieldName="description"`），`user.clear+type` 后提交 `arg.description` 正确。

- [x] **Step 4: 跑 ProjectFields 单测 + 类型 + lint**

Run: `npx vitest run src/features/projects/ProjectFields.test.tsx`（含 `alwaysShowPlanner` 用例，PASS）
Run: `npx tsc --noEmit`（无错）
Run: `npx eslint src/features/projects/EditProjectDialog.tsx`（无 NEW 错误）

- [x] **Step 5: Commit**

```bash
git add src/features/projects/EditProjectDialog.tsx
git commit -m "refactor(projects): EditProjectForm 复用共享 ProjectFields(description/storage/始终带模型/alwaysShowPlanner),提交映射与规划下拉显隐逐字不变"
```

---

## Task 4: WorkflowDialog.schema.ts + WorkflowForm rhf 化（Dialog 壳不变）

抽 `workflowFormSchema`（`.superRefine` 复刻 4 条校验，复用 `findGraphError`）。`WorkflowForm` 改用 rhf `FormProvider`，`nodes` 经 `Controller` 接 `WorkflowNodesEditor`。`findGraphError` 移到 schema 文件并从 `WorkflowDialog.tsx` re-export（测试独立 import，硬约束 #5）。`WorkflowDialog` 壳保留自有 trigger+open+`<Dialog>`（**不引入 FormDialog**——同样的双 Dialog 问题，2 个消费方依赖 trigger）。

**Files:**
- Create: `src/features/projects/WorkflowDialog.schema.ts`
- Create: `src/features/projects/WorkflowDialog.schema.test.ts`
- Modify: `src/features/projects/WorkflowDialog.tsx`
- 测试 `src/features/projects/WorkflowDialog.test.tsx` **保留全部断言**（含 `findGraphError` 独立 describe）。

- [x] **Step 1: 先跑现有测试建立基线**

Run: `npx vitest run src/features/projects/WorkflowDialog.test.tsx`
Expected: PASS（WorkflowForm 8 用例 + findGraphError 6 用例）。

- [x] **Step 2: 写 schema 的失败测试**

```ts
// src/features/projects/WorkflowDialog.schema.test.ts
import { describe, it, expect } from "vitest"
import { workflowFormSchema, findGraphError } from "./WorkflowDialog.schema"

const node = (id: string, dependsOn: string[] = []) => ({
  id,
  type: "script",
  promptId: "",
  dependsOn,
})

describe("workflowFormSchema superRefine", () => {
  it("name 空 → 请输入工作流名称", () => {
    const r = workflowFormSchema.safeParse({ name: "  ", nodes: [node("a")] })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入工作流名称")).toBe(true)
    }
  })

  it("0 节点 → 工作流必须包含至少一个节点", () => {
    const r = workflowFormSchema.safeParse({ name: "wf", nodes: [] })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(
        r.error.issues.some((i) => i.message === "工作流必须包含至少一个节点"),
      ).toBe(true)
    }
  })

  it("空 ID 节点 → 所有节点 ID 不能为空", () => {
    const r = workflowFormSchema.safeParse({ name: "wf", nodes: [node("")] })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(
        r.error.issues.some((i) => i.message === "所有节点 ID 不能为空"),
      ).toBe(true)
    }
  })

  it("重复 ID → 存在重复的节点 ID: a", () => {
    const r = workflowFormSchema.safeParse({
      name: "wf",
      nodes: [node("a"), node("a")],
    })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(
        r.error.issues.some((i) => i.message === "存在重复的节点 ID: a"),
      ).toBe(true)
    }
  })

  it("循环依赖 → findGraphError 文案", () => {
    const r = workflowFormSchema.safeParse({
      name: "wf",
      nodes: [node("A", ["B"]), node("B", ["A"])],
    })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => /循环依赖/.test(i.message))).toBe(true)
    }
  })

  it("合法线性图通过", () => {
    const r = workflowFormSchema.safeParse({
      name: "wf",
      nodes: [node("a"), node("b", ["a"])],
    })
    expect(r.success).toBe(true)
  })
})

// findGraphError 行为单测从 WorkflowDialog.test.tsx 继续覆盖（独立 import）；
// 此处只确认 schema 文件正确导出同一函数。
describe("findGraphError (from schema)", () => {
  it("self-loop 返回循环依赖文案", () => {
    expect(findGraphError([{ id: "A", dependsOn: ["A"] }])).toMatch(/循环依赖/)
  })
})
```

- [x] **Step 3: 跑测试确认失败**

Run: `npx vitest run src/features/projects/WorkflowDialog.schema.test.ts`
Expected: FAIL（`Cannot find module './WorkflowDialog.schema'`）。

- [x] **Step 4: 写 WorkflowDialog.schema.ts**

> `findGraphError` 从 `WorkflowDialog.tsx:31-59` **逐字搬来**（不改一字——以现有文件实际实现为准，下方为参考形态，落地前对照原文件逐字核对）。superRefine 按现状 `submit` 的 4 个分支顺序与文案逐字复刻：name trim 空 → 0 节点 → 遍历节点（空 id → 重复 id）→ findGraphError。每个分支 `addIssue` 后 `return`，与现状「首个问题即返回」语义一致。

```ts
// src/features/projects/WorkflowDialog.schema.ts
import { z } from "zod"
import type { WorkflowNode } from "@/lib/types"

// 返回第一处问题的中文描述，无问题返回 null。前端与后端 ValidateCustomGraph 同义，
// 让用户在保存前就看到「循环依赖」，而不是运行时才 400。
// 从 WorkflowDialog.tsx 原样迁来（不改一字；落地前对照原文件逐字核对实现与文案）。
export function findGraphError(
  nodes: { id: string; dependsOn: string[] }[],
): string | null {
  const ids = new Set(nodes.map((n) => n.id))
  for (const n of nodes) {
    for (const dep of n.dependsOn) {
      if (!ids.has(dep)) return `节点「${n.id}」依赖了不存在的节点「${dep}」`
    }
  }
  // DFS 三色环检测
  const deps = new Map(nodes.map((n) => [n.id, n.dependsOn]))
  const color = new Map<string, number>() // 0 white,1 gray,2 black
  let cycleMsg: string | null = null
  const visit = (id: string): boolean => {
    color.set(id, 1)
    for (const dep of deps.get(id) ?? []) {
      const c = color.get(dep) ?? 0
      if (c === 1) {
        cycleMsg = `工作流存在循环依赖:「${id}」→「${dep}」`
        return true
      }
      if (c === 0 && visit(dep)) return true
    }
    color.set(id, 2)
    return false
  }
  for (const n of nodes) {
    if ((color.get(n.id) ?? 0) === 0 && visit(n.id)) return cycleMsg
  }
  return null
}

// 工作流表单 schema。name + nodes（DAG）。校验经 superRefine 复刻原 WorkflowForm
// submit 的 4 条分支（顺序、文案逐字一致），首个问题即停（用 ctx + 提前 return）。
export const workflowFormSchema = z
  .object({
    name: z.string(),
    nodes: z.array(
      z.object({
        id: z.string(),
        type: z.string(),
        promptId: z.string(),
        promptText: z.string().optional(),
        dependsOn: z.array(z.string()),
      }),
    ),
  })
  .superRefine((v, ctx) => {
    if (!v.name.trim()) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ["name"], message: "请输入工作流名称" })
      return
    }
    if (v.nodes.length === 0) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["nodes"],
        message: "工作流必须包含至少一个节点",
      })
      return
    }
    const ids = new Set<string>()
    for (const n of v.nodes) {
      if (!n.id) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["nodes"],
          message: "所有节点 ID 不能为空",
        })
        return
      }
      if (ids.has(n.id)) {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["nodes"],
          message: `存在重复的节点 ID: ${n.id}`,
        })
        return
      }
      ids.add(n.id)
    }
    const graphErr = findGraphError(v.nodes)
    if (graphErr) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, path: ["nodes"], message: graphErr })
    }
  })

export type WorkflowFormValues = z.infer<typeof workflowFormSchema>

// 等价于 WorkflowNode[]（运行时一致；类型上 promptText 可选对齐）。
export type WorkflowFormNode = WorkflowNode
```

- [x] **Step 5: 跑 schema 测试确认通过**

Run: `npx vitest run src/features/projects/WorkflowDialog.schema.test.ts`
Expected: PASS（superRefine 6 + findGraphError 1 = 7 passed）。

- [x] **Step 6: 重写 WorkflowDialog.tsx 的 WorkflowForm**

> `WorkflowForm` 改用 rhf：`name` 用 `register`，`nodes` 经 `Controller` 接 `WorkflowNodesEditor`。`findGraphError` 从 schema re-export（保留同名导出，测试 `import { WorkflowForm, findGraphError } from "./WorkflowDialog"` 不变）。
>
> ⚠️ 现状 `WorkflowForm` 的 submitError 是单条 `<p role="alert">`，测试断言 `findByText("请输入工作流名称")`、`findByText("存在重复的节点 ID: a")`、`findByRole("alert")` 内含「循环依赖」。rhf + zodResolver 把 superRefine 的 issue 映到 `errors.name`/`errors.nodes`。为保留「单条 alert 显示首个错误文案」的现状呈现与测试断言，**WorkflowForm 显示 `errors.name?.message ?? errors.nodes?.message` 于一个 `<p role="alert">`**（superRefine 首个问题即停，故同时至多一条）。`onSubmit` 在 rhf 校验通过后调用（`form.handleSubmit`），传 `{ name: name.trim(), nodes }`——**name 提交时 trim**（保留现状 `name.trim()`）。
>
> `WorkflowNodesEditor` 是受控（`nodes`/`onChange`）——经 `Controller name="nodes"` 的 `field.value`/`field.onChange` 桥接。默认值 `initial?.nodes ?? DEFAULT_NODES`。
>
> `DEFAULT_NODES` 常量保留。`WorkflowDialog`（Dialog 壳）保留 trigger + open + `<Dialog>` + `key` 重建（硬约束 #2，测试不覆盖 WorkflowDialog 壳，只测 WorkflowForm）。

```tsx
// WorkflowDialog.tsx —— 顶部 import 增加：
import { useForm, FormProvider, Controller, type Resolver } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import {
  workflowFormSchema,
  findGraphError,
  type WorkflowFormValues,
} from "./WorkflowDialog.schema"
// 移除原文件内联的 findGraphError 定义（已移到 schema），改为 re-export：
export { findGraphError }
// 保留 DEFAULT_NODES、WorkflowFormProps、WorkflowDialogProps、WorkflowDialog 壳不变。
```

```tsx
// WorkflowForm（替换原 useState 版本）：
export function WorkflowForm({
  initial,
  prompts,
  basics,
  org,
  onSubmit,
  onSuccess,
}: WorkflowFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const resolver = zodResolver(workflowFormSchema) as unknown as Resolver<WorkflowFormValues>
  const form = useForm<WorkflowFormValues>({
    resolver,
    defaultValues: {
      name: initial?.name ?? "",
      nodes: initial?.nodes ?? DEFAULT_NODES,
    },
  })
  const {
    register,
    control,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = form

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const saved = await onSubmit({ name: values.name.trim(), nodes: values.nodes })
      onSuccess?.(saved)
    } catch {
      setSubmitError("保存失败，请重试")
    }
  })

  // superRefine 首个问题即停 → name 与 nodes 至多一条错误。单条 alert 显示首个文案
  // （保留现状「一个 role=alert 显示首个错误」呈现与测试断言）。
  const validationError = errors.name?.message ?? errors.nodes?.message ?? null
  const shownError = validationError ?? submitError

  return (
    <FormProvider {...form}>
      <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col gap-4" noValidate>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="workflow-name">工作流名称</Label>
          <Input id="workflow-name" placeholder="e.g. 默认管线" {...register("name")} />
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto pr-1">
          <Controller
            control={control}
            name="nodes"
            render={({ field }) => (
              <WorkflowNodesEditor
                nodes={field.value}
                onChange={field.onChange}
                prompts={prompts}
                basics={basics}
                org={org}
              />
            )}
          />
        </div>

        {shownError && (
          <p role="alert" className="text-[12px] text-danger">
            {shownError}
          </p>
        )}

        <DialogFooter>
          <Button type="submit" variant="amber" disabled={isSubmitting}>
            {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            保存
          </Button>
        </DialogFooter>
      </form>
    </FormProvider>
  )
}
```

> ⚠️ `Input` 现状非受控（`register`）即可。`workflow-name` label 用 `getByLabelText("工作流名称")` 选（测试 `user.type(getByLabelText("工作流名称"), ...)`）。`<Label htmlFor="workflow-name">` + `<Input id="workflow-name">` 保留关联，断言不变。

- [x] **Step 7: 跑 WorkflowDialog 测试 + 调选择器**

Run: `npx vitest run src/features/projects/WorkflowDialog.test.tsx`
Expected: PASS。关键断言核对：
- 默认 2 节点 / 提交 name+nodes：`Controller` 桥接 `WorkflowNodesEditor`，`field.value` 初始 = DEFAULT_NODES（2 节点），提交 `arg.nodes` 有 2 项 ✓。
- 添加节点 / 标准管线 / 行内新建 / 自定义文本：均通过 `WorkflowNodesEditor` 的 `onChange`（= `field.onChange`）更新 rhf nodes ✓。
- name 空拦截：superRefine → `errors.name="请输入工作流名称"` → alert 显示该文案 ✓（`findByText("请输入工作流名称")`）。
- 重复 ID：`errors.nodes="存在重复的节点 ID: a"` ✓。
- 循环依赖：`errors.nodes` 含「循环依赖」，`findByRole("alert")` 文本匹配 `/循环依赖/` ✓。
- `findGraphError` 独立 describe：从 `./WorkflowDialog` re-export，签名/行为不变 ✓。

若任何断言因 alert 时机失败（rhf 校验是异步），测试已用 `findBy*` 应能等到——无需弱化。

- [x] **Step 8: 类型 + lint**

Run: `npx tsc --noEmit`（无错）
Run: `npx eslint src/features/projects/WorkflowDialog.tsx src/features/projects/WorkflowDialog.schema.ts`
Expected: 无 NEW 错误。`WorkflowDialog.tsx` 现同时 export 组件与 `findGraphError`（re-export）——若 react-refresh 对「组件 + 1 个非组件函数导出」告警，确认 SP-A 同类既有模式（如 `StorageConfigPage` export `MODE_LABELS`）是否同样告警——若为既有模式则接受（非 NEW）。**不**为消除告警而让测试改从 schema import `findGraphError`（硬约束 #5 要求保留 `WorkflowDialog` 的导出）。

- [x] **Step 9: Commit**

```bash
git add src/features/projects/WorkflowDialog.schema.ts src/features/projects/WorkflowDialog.schema.test.ts src/features/projects/WorkflowDialog.tsx
git commit -m "refactor(projects): WorkflowForm 迁到 rhf+zod(superRefine 复刻 4 条图校验,findGraphError 复用),Controller 接节点编辑器,文案逐字不变"
```

---

## Library 暂缓说明（本计划不含 Library 任务）

> 原 SP-B spec 含「LibraryView 页壳 → `CrudResourcePage`」一项，经评估**确认暂缓**，本计划不包含、`src/features/library/LibraryPage.tsx` 与资产页**完全不动**。原因：
>
> 1. **错误文案不可定制**：`CrudResourcePage` 的错误态文案写死「加载失败」，无 `errorHint` prop；而 `library.test.tsx` 断言「资产加载失败」。强行套用会让 `getByText("资产加载失败")` 失败（断言被破坏）。
> 2. **布局形态不兼容**：`CrudResourcePage` 外壳是单列 `mx-auto max-w-[1200px] p-6` 居中；而 `LibraryView` 是「左 `FilterRail`（`aside w-56`）+ 右栏全宽网格」两栏布局。套用会让网格不再占满右栏 → 视觉回归（与「逐像素不变」纪律抵触）。
>
> 迁移 Library 需先给 `CrudResourcePage` 加 `errorHint?: string` prop + 两栏布局支持（属 SP-A 框架增强，超出本计划范围）。**故 Library 迁移延后到后续单独工程**；本计划只做项目 Create/Edit 表单去重 + WorkflowForm schema 抽取。

---

## 收尾验收（全部任务后）

- [x] **全量类型检查**：`npx tsc --noEmit` → 无错。
- [x] **lint 无新增（仅本计划触碰目录）**：`npx eslint src/features/projects` → 无 NEW 错误；确认 schema 全在 `*.schema.ts`（无 react-refresh 警告）。（**不**对 `src/features/library` 跑——本计划未触碰。）
- [x] **全量回归**：`npx vitest run` → 全绿（核对 projects/workflow 用例数与基线一致，新增 schema/字段单测计入；library 用例数与基线一致，因未触碰）。
- [x] **消费方仍编译**：确认 `ProjectListPage.tsx`、`routes/_authed/orgs.$org.projects.$id.index.tsx`、`routes/_authed/orgs.$org.projects.$id.runs.$runId.tsx` 未改且 `tsc` 通过（trigger 公共 API 保留）。
- [x] **浏览器烟雾对比迁移前**（参考 MEMORY「Studio dev runtime」：studiod :8083 + Vite :5173；`demo@studio.com / DevReveal#123`）——**仅项目 Create/Edit + Workflow 对话框**：
  - 新建项目（标准 + 切绘本选/不选年龄段均可提交 + 提交）→ 确认 payload `pictureBookConfig` 为字符串、标准项目不带 kind。
  - 编辑项目（改 name/创意需求、切绘本、选存储配置、**规划下拉在无 text 模型时仍显示**）。
  - 工作流（新建/编辑、添加节点、标准管线、制造重复 ID / 循环依赖看错误文案逐字一致、保存成功关闭）。
  - **资产库无需烟雾**（本计划未触碰）。
- [x] **toast 单源核对**：项目编辑成功的 `toast.success("项目信息已更新")` 仍由 `ProjectListPage.tsx` 的 `onSuccess` 发出（未在 Dialog/Form 内重复发）；Create/Workflow 现状无 toast（不新增）。
- [x] `superpowers:finishing-a-development-branch` 完成 `refactor/sp-b-project-asset-formdialog`（push + PR；studio 手动 lockstep，参考 MEMORY「Studio/authz test + release」）。

---

## Self-Review 记录

- **Spec 覆盖（务实版，scope 已收敛）**：
  - §1 项目创建/编辑去重 → Task 1（ProjectFields.schema + ProjectFields）+ Task 2（Create）+ Task 3（Edit）。kind/pbConfig 折入 rhf ✓；pbConfig JSON 字符串序列化保留 ✓（硬约束 #3，Task 2/3 的 `JSON.stringify`）；提交 payload 逐字节不变 ✓（Create/Edit 各自 onSubmit 映射逐字保留）。**Dialog 壳与 trigger 公共 API 保留、不引入 FormDialog**（务实裁定，非偏差——见 Architecture）；测试继续直接渲染内层 `*Form`。
  - §2 WorkflowForm → Task 4。superRefine 复刻 4 条校验 + findGraphError 复用并保留导出 + Controller 接节点编辑器 ✓。文案逐字（schema 测试断言精确字符串）✓。WorkflowDialog 壳保留（不引入 FormDialog，避免双 Dialog）✓。
  - §3 LibraryView → **暂缓，本计划不含**。理由记录于「Library 暂缓说明」节：`CrudResourcePage` 错误文案写死「加载失败」（测试断言「资产加载失败」）+ 单列 `max-w-[1200px]` vs LibraryView 两栏过滤栏布局 → 会破断言 + 视觉回归；迁移需先给原语加 `errorHint` prop + 两栏支持（SP-A 框架增强，超范围），故延后到后续单独工程。
  - §测试 → 每 Task 先跑基线、保留断言、新增 schema/组件单测。
  - §风险（kind/pbConfig 入 rhf、图校验 parity、toast 单源）→ 分别在硬约束 #3/#4/#7、Task 4、收尾 toast 核对覆盖。
- **零新校验确认（硬约束 #7）**：`projectFormSchema` **不含** picturebook 的 `.superRefine`；现状 Create/Edit 绘本配置无前端必填校验，本重构**不引入**「请选择年龄段」等任何新校验。`ProjectFields.tsx` 无 `errors.pbConfig?.ageBand` 显示块。schema 测试用例显式断言「空年龄段仍通过」以锁定此行为。已有 `name/brief/style/contentType/targetPlatform` 必填仅复刻现状（后端缺则 400，测试已断言 name/style 拦截）。
- **占位符扫描**：无 "TBD"/"similar to"/"write tests for the above"。每个 schema/组件/Form/Dialog 步骤均给完整代码；迁移步骤给完整替换块 + 逐字保留点。`findGraphError` 给参考实现并要求落地前对照原文件逐字核对（其实现在原仓为既有代码、原样搬运）。
- **类型一致性**：
  - `ProjectFormValues`（schema 导出）字段 `name/brief/description/contentType/targetPlatform/style/plannerProvider/plannerModel/imageProvider/imageModel/storageConfigId/kind/pbConfig` —— Task 1 schema、Task 1 ProjectFields（`watch/register/Controller name=...`）、Task 2 `toCreateInput`、Task 3 Edit 映射，命名跨任务一致。
  - `defaultsFor(initial?: Partial<Project> & { brief?: string })` —— Create 用 `defaultsFor()`、Edit 用 `defaultsFor(project)`，签名一致。
  - `ProjectFields` props（`styles/fieldIdPrefix/briefFieldName/briefRequired/alwaysShowPlanner/textModels/imageModels/storageConfigs/project`）—— Task 1 定义、Task 2/3 调用一致；`alwaysShowPlanner` 在 Task 1 即落地（Edit 在 Task 3 传 true）。
  - `WorkflowFormValues`（`name/nodes`）、`findGraphError` 签名 —— Task 4 schema 定义、WorkflowForm 使用、`WorkflowDialog.tsx` re-export、两处测试 import 一致。
- **务实路线（明确裁定，非偏差）**：
  - Create/Edit/Workflow 三处**均不引入 `FormDialog`/`CrudResourcePage` 公共壳**——因这些原语自带 `<Dialog>`/单列页壳，会与现有 trigger 公共 API（3 消费方依赖）冲突成双 Dialog，且 `*Form` 被测试直接渲染。去重落点是字段+schema（`ProjectFields`/`projectFormSchema`/`workflowFormSchema`），公共 API 与测试零改写、视觉/行为零变化。这是本计划的既定 scope，非对 spec 的偏离。
