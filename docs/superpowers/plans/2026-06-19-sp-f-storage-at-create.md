# SP-F 项目创建时可选存储后端 Implementation Plan

> **✅ DONE（2026-06-19，PR #68 squash-merge）** — 2 任务完成并经 spec+代码质量两段审查 + 修复闭环。全量 `vitest` 453 用例绿、`tsc` 干净、SP-F 触及文件 `eslint` 无 error；浏览器烟雾确认创建对话框出现「存储配置」下拉、其余字段无回归（图片模型按 M9 设计仍仅编辑暴露）。下方勾选为完成留痕。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把「per-project 存储后端 override」从仅编辑暴露到创建：创建项目对话框出现存储下拉（继承组织默认 + 各 enabled 配置），选中后随创建提交。纯前端改动，后端已就绪。

**Architecture:** 镜像 edit 既有写法。`CreateProjectInput` 加 `storageConfigId?: string`；`CreateProjectDialog` 接 `storageConfigs` prop → 透传给共享 `ProjectFields`（其存储选择器已具备）→ `toCreateInput` omit-empty 带上；`ProjectListPage` 两处 `<CreateProjectDialog>` 传 `storageConfigs`（已在手，源自 `useStorageConfigs(org)`）。后端 `createProjectHandler` + `project.CreateInput` 已接受并持久化 `storageConfigId`，零改。

**Tech Stack:** React 19 + TS；rhf + zod（ProjectFields）；TanStack Router + React Query；Vitest + @testing-library/react + user-event。工作目录 `web/`；测试 `npx vitest run <path>`、类型 `npx tsc --noEmit`、lint `npx eslint <path>`。所有命令用显式 `cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && …`（持久 cwd 可能漂移）。

---

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `web/src/lib/types.ts` | Modify | `CreateProjectInput` += `storageConfigId?: string` |
| `web/src/features/projects/CreateProjectDialog.tsx` | Modify | import `StorageConfig`；`CreateProjectFormProps` += `storageConfigs?`；`CreateProjectForm`/`CreateProjectDialog` 解构并线程化；`<ProjectFields storageConfigs={…}/>`；`toCreateInput` omit-empty 带 `storageConfigId` |
| `web/src/features/projects/ProjectListPage.tsx` | Modify | 两处 `<CreateProjectDialog>`（头部 + 空态）加 `storageConfigs={storageConfigs}` |
| `web/src/features/projects/projects.test.tsx` | Modify | 新增创建流存储测试（镜像 `EditProjectDialog.test.tsx`）；保留现有断言 |
| `internal/...`（后端） | 不动 | 已接受/持久化 `storageConfigId` |

---

## Task 1: 暴露存储选择器到创建流（TDD）

**Files:**
- Modify: `web/src/lib/types.ts`
- Modify: `web/src/features/projects/CreateProjectDialog.tsx`
- Modify: `web/src/features/projects/ProjectListPage.tsx`
- Modify: `web/src/features/projects/projects.test.tsx`

### Step 1: 先写失败测试（`projects.test.tsx`）

在 `describe("CreateProjectForm", …)` 块内（现有用例之后、`})` 之前）追加两条测试。镜像 `EditProjectDialog.test.tsx:117-190` 的存储断言（该模式对 ProjectFields 存储 combobox 在 jsdom 下可靠工作）。先在该文件顶部确认有 `StorageConfig` 类型 import；若无，给现有 `@/lib/types` 的 `import type { … }` 补 `StorageConfig`。

在 `describe("CreateProjectForm")` 块内合适位置加一个本地 fixture（若文件内尚无 storageConfigs fixture）：

```tsx
  const STORAGE_CONFIGS: StorageConfig[] = [
    {
      id: "sc1", orgId: "o1", scope: "org", name: "主存储桶", mode: "s3",
      enabled: true, isDefault: true, endpoint: "https://s3.example.com",
      region: "us-east-1", bucket: "my-bucket", accessKeyId: "AKID",
      hasSecret: true, publicPrefix: "", useSsl: true,
    },
    {
      id: "sc2", orgId: "o1", scope: "org", name: "备用仓库", mode: "github",
      enabled: true, isDefault: false, endpoint: "", region: "", bucket: "my-repo",
      accessKeyId: "my-owner", hasSecret: false, publicPrefix: "", useSsl: false,
    },
  ]

  it("renders the storage dropdown and submits storageConfigId when a config is selected", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeProject({ id: "x" }))
    const user = userEvent.setup()
    render(<CreateProjectForm styles={STYLES} storageConfigs={STORAGE_CONFIGS} onSubmit={onSubmit} />)

    // 必填项填好再提交。
    await user.type(screen.getByLabelText("项目名称"), "X")
    await user.type(screen.getByLabelText("创意需求"), "一句")

    // 存储下拉默认"继承组织默认"；选「主存储桶」。
    const trigger = screen.getByRole("combobox", { name: /存储配置/ })
    await user.click(trigger)
    await user.click(await screen.findByRole("option", { name: /主存储桶/ }))

    await user.click(screen.getByRole("button", { name: "创建" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    expect(onSubmit.mock.calls[0][0]).toMatchObject({ storageConfigId: "sc1" })
  })

  it("omits storageConfigId when storage is left at inherit-default", async () => {
    const onSubmit = vi.fn().mockResolvedValue(makeProject({ id: "x" }))
    const user = userEvent.setup()
    render(<CreateProjectForm styles={STYLES} storageConfigs={STORAGE_CONFIGS} onSubmit={onSubmit} />)

    await user.type(screen.getByLabelText("项目名称"), "X")
    await user.type(screen.getByLabelText("创意需求"), "一句")
    await user.click(screen.getByRole("button", { name: "创建" }))

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
    // omit-empty：未选存储 → payload 不含 storageConfigId（= 后端用组织默认）。
    expect(onSubmit.mock.calls[0][0].storageConfigId).toBeUndefined()
  })
```

> 注：若 `makeProject`/`STYLES` helper 名在该文件不同，用文件内真实的。确认 `userEvent`/`waitFor`/`screen`/`render` 已 import（现有用例已用）。

### Step 2: 运行测试，确认 FAIL

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && npx vitest run src/features/projects/projects.test.tsx
```
预期：新两条 FAIL——`storageConfigs` 不是 `CreateProjectForm` 的 prop（TS/渲染层面）、存储 combobox 不渲染（create 未传 storageConfigs）、payload 无 `storageConfigId`。

### Step 3: `CreateProjectInput` 加字段（`web/src/lib/types.ts`）

把 `CreateProjectInput` 接口（在 `imageProvider?/imageModel?` 之后、`customWorkflowEnabled?` 之前）加一行：

```ts
  // M10: per-project 存储配置 override；空/省略 = 后端用组织默认存储配置。
  storageConfigId?: string
```

### Step 4: `CreateProjectDialog.tsx` 线程化 storageConfigs + toCreateInput

4a. 顶部 import 加 `StorageConfig`（现有 `import type { CreateProjectInput, ModelConfig, Project, Style } from "@/lib/types"` → 加 `StorageConfig`）：
```ts
import type {
  CreateProjectInput,
  ModelConfig,
  Project,
  StorageConfig,
  Style,
} from "@/lib/types"
```

4b. `toCreateInput` 返回对象内（在 imageProvider/imageModel 展开之后、kind 展开之前或之后皆可，置于 kind 之前）加 omit-empty 存储：
```ts
    ...(values.storageConfigId
      ? { storageConfigId: values.storageConfigId }
      : {}),
```

4c. `CreateProjectFormProps` 加 prop（在 `imageModels?` 之后）：
```ts
  /** M10: org 存储配置列表（供存储下拉）。空 = 不显示存储下拉（= 用组织默认）。 */
  storageConfigs?: StorageConfig[]
```

4d. `CreateProjectForm` 解构形参加 `storageConfigs`，并传给 `ProjectFields`：
```tsx
export function CreateProjectForm({
  styles,
  textModels,
  imageModels,
  storageConfigs,
  onSubmit,
  onSuccess,
}: CreateProjectFormProps) {
```
`<ProjectFields … />` 调用加 `storageConfigs={storageConfigs}`：
```tsx
        <ProjectFields
          styles={styles}
          fieldIdPrefix="create"
          briefFieldName="brief"
          briefRequired
          textModels={textModels}
          imageModels={imageModels}
          storageConfigs={storageConfigs}
        />
```

4e. `CreateProjectDialog` 解构形参加 `storageConfigs`，并传给内部 `CreateProjectForm`：
```tsx
export function CreateProjectDialog({
  trigger,
  styles,
  textModels,
  imageModels,
  storageConfigs,
  onSubmit,
  onSuccess,
}: CreateProjectDialogProps) {
```
内部 `<CreateProjectForm … />` 加 `storageConfigs={storageConfigs}`：
```tsx
        <CreateProjectForm
          styles={styles}
          textModels={textModels}
          imageModels={imageModels}
          storageConfigs={storageConfigs}
          onSubmit={onSubmit}
          onSuccess={(project) => {
            setOpen(false)
            onSuccess?.(project)
          }}
        />
```

### Step 5: `ProjectListPage.tsx` 两处传 prop

`storageConfigs` 已在 `ProjectListView` 解构在手（来自 props）。两处 `<CreateProjectDialog>` 各加 `storageConfigs={storageConfigs}`：

头部站点（现 `trigger={newButton} styles textModels onSubmit onSuccess`）改为含：
```tsx
          <CreateProjectDialog
            trigger={newButton}
            styles={styles}
            textModels={textModels}
            storageConfigs={storageConfigs}
            onSubmit={onCreate}
            onSuccess={onOpenProject}
          />
```
空态站点（现 `trigger={<Button…>} styles textModels onSubmit onSuccess`）改为含：
```tsx
            <CreateProjectDialog
              trigger={<Button variant="amber">新建项目</Button>}
              styles={styles}
              textModels={textModels}
              storageConfigs={storageConfigs}
              onSubmit={onCreate}
              onSuccess={onOpenProject}
            />
```

> 注：`ProjectListPage.tsx` 已 import `StorageConfig` 且 `storageConfigs` 已是 `ProjectListViewProps` 字段/解构（无需新增）。是否给 textModels 传 imageModels 不在本任务范围——只加 storageConfigs。

### Step 6: 运行测试，确认 PASS

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && npx vitest run src/features/projects/projects.test.tsx
```
预期：全绿（新两条 + 现有 CreateProjectForm/ProjectListView 断言）。

> 若 Radix 存储 combobox 在 create 上下文里 select 不稳定（edit 同款应可靠；若仍 flaky），仅"select→payload"那条退化为：保留渲染断言（combobox 存在 + 选项可见），把"payload 含 sc1"留给 E2E，并在测试注释说明——但**不要删除** omit-default 那条。先按上面写法试，能过就保留。

### Step 7: tsc + eslint

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && npx tsc --noEmit
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && npx eslint src/lib/types.ts src/features/projects/CreateProjectDialog.tsx src/features/projects/ProjectListPage.tsx src/features/projects/projects.test.tsx
```
预期：tsc 干净；eslint 触及文件 0 error。

### Step 8: commit

```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && git add src/lib/types.ts src/features/projects/CreateProjectDialog.tsx src/features/projects/ProjectListPage.tsx src/features/projects/projects.test.tsx
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && git commit -m "feat(projects): expose per-project storage override at create

镜像编辑流：CreateProjectInput += storageConfigId；CreateProjectDialog 接 storageConfigs
透传给共享 ProjectFields(存储下拉:继承组织默认 + enabled 配置);toCreateInput omit-empty 带上;
ProjectListPage 两处入口传 storageConfigs。后端已接受,零改。"
```

---

## Task 2: 收尾 — 全量验证 + 浏览器烟雾 + 结束分支

**Files:** 无（仅验证）

### Step 1: 全量 tsc
```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && npx tsc --noEmit
```
预期：干净（0 error）。

### Step 2: 全量 eslint
```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && npx eslint .
```
预期：仅剩 2 个既有、与 SP-F 无关的 error（`AssetGalleryModal.tsx`/`useProductionTimeline.ts`，更严 react-hooks 规则，先于本批存在，超范围不动）。除此 0 新 error。

### Step 3: 全量 vitest
```bash
cd /home/hellotalk/code/go/src/github.com/costa92/llm-agent-ecosystem/llm-agent-studio/web && npx vitest run
```
预期：全部测试通过（含新增创建流存储两条 + projects.test 其余 + edit 存储测试不受影响）。

### Step 4: 浏览器烟雾（创建对话框存储下拉）

复用截图 harness 模式（playwright-core from `/home/hellotalk/code/web/sentinel-web/node_modules`，系统 chrome `/usr/bin/google-chrome`，登录 `demo@studio.com`/`demo12345`，org `169278fcd0dec7d485c741215a578fab`，需本地 studiod:8083 + Vite:5173 在跑）。脚本：进 `/orgs/<org>/projects` → 点「新建项目」打开对话框 → 截图 `/tmp/sp-f-create.png`。

```bash
cat > /tmp/sp-f-shot.cjs <<'EOF'
const { chromium } = require("/home/hellotalk/code/web/sentinel-web/node_modules/playwright-core")
const BASE = "http://localhost:5173"
const ORG = "169278fcd0dec7d485c741215a578fab"
;(async () => {
  const b = await chromium.launch({ executablePath: "/usr/bin/google-chrome", headless: true, args: ["--no-sandbox", "--disable-dev-shm-usage"] })
  const p = await (await b.newContext({ viewport: { width: 1200, height: 900 } })).newPage()
  await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" })
  await p.fill("#email", "demo@studio.com"); await p.fill("#password", "demo12345")
  await p.click('button:has-text("登录")'); await p.waitForTimeout(2200)
  await p.goto(`${BASE}/orgs/${ORG}/projects`, { waitUntil: "domcontentloaded" }); await p.waitForTimeout(2500)
  await p.click('button:has-text("新建项目")'); await p.waitForTimeout(1200)
  await p.screenshot({ path: "/tmp/sp-f-create.png", fullPage: false })
  console.log("shot /tmp/sp-f-create.png")
  await b.close()
})().catch((e) => { console.error("ERR", e.message); process.exit(1) })
EOF
node /tmp/sp-f-shot.cjs
```

### Step 5: 人工核对截图（Read `/tmp/sp-f-create.png`）
确认创建对话框出现「存储配置」下拉（默认「继承组织默认」），其余字段（名称/创意需求/风格/模型）布局未回归。

### Step 6: 结束开发分支
调用 superpowers:finishing-a-development-branch，按结构化选项 Push + PR + squash 合并（同 SP-A/B/C/D/E 节奏）。

---

## 风险 / 边界
- **omit-empty：** create 未选存储 → payload 省略 `storageConfigId`（后端用组织默认）；与 edit 的 always-send `""` 行为等价或更保守。
- **两处入口都要传 prop：** 漏一处则该入口无存储下拉——计划已覆盖头部 + 空态。
- **Radix combobox 单测稳定性：** 优先用 edit 同款 select 断言；若 flaky，select-payload 那条退化为渲染断言 + E2E，omit-default 断言必留。
- **零回归：** ProjectFields / edit 流 / 后端均不动；create 仅多一个可选存储下拉，无 storageConfigs 时不渲染（行为同今天）。
- **YAGNI：** 只做存储下拉暴露；不顺手统一 201、不加 send-empty、不动其它 SP-C 遗留项。
