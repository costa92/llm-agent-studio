# SP-A 配置/管理页通用 CRUD 框架 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 抽出 `web/src/features/common/crud/` 通用 CRUD 框架原语，并把 Members/Prompt/StorageConfig/ModelConfig/PlatformAdmin 五页迁移到框架，各页呈现/行为/视觉不变。

**Architecture:** 委托式框架——框架原语只拥有不变量（对话框状态机、外壳、双模式列表、Dialog 接线、toast/错误），资源页通过 children/render-prop/配置委托自己的表单字段、item 渲染、行级动作。复用既有 `features/*/api.ts` hooks（已自带 invalidate）。

**Tech Stack:** React 19 + TypeScript、TanStack Query、react-hook-form + zod(zodResolver)、shadcn (`@/components/ui/*`)、studio Button (`@/components/studio/Button`)、Vitest + Testing Library + userEvent、sonner toast。

**约定（所有任务通用）：**
- 工作目录 `web/`；测试 `npx vitest run <file>`；类型 `npx tsc -b`。
- 测试风格：mock `./api` 钩子 / 用 `installFetchRoutes`（`src/test/helpers.ts`）；render 组件 + `<Toaster>`；`userEvent` 交互。
- 迁移页时**保留现有页面测试的全部断言**，仅在 markup 变化时调选择器；不弱化断言。
- 每个 Task 末尾 commit；commit message 说「为什么」。

---

## File Structure（先锁定）

新建模块 `web/src/features/common/crud/`：

| 文件 | 职责 |
|---|---|
| `types.ts` | `RowAction<T>`、`Column<T>` 类型 |
| `ConfirmDialog.tsx` | 二次确认对话框 |
| `RevealSecretInput.tsx` | 密钥字段（Eye 切换 + 可选 reveal） |
| `DataView.tsx` | 双模式列表（table/cards + rowActions + groupBy） |
| `FormDialog.tsx` | 表单对话框壳（rhf FormProvider + 提交/错误） |
| `useCrudResource.ts` | headless 状态机（dialog/deleteTarget + submit/confirmDelete + toast） |
| `CrudResourcePage.tsx` | 外壳（页头/加载/错误/空态）+ `SingletonConfigForm` |
| `index.ts` | 桶导出 |

迁移修改：`features/members/MembersPage.tsx`、`features/prompt/PromptListPage.tsx`、`features/storage/StorageConfigPage.tsx`、`features/cost/ModelConfigPage.tsx`、`features/platform/PlatformAdminPage.tsx`（及各自 `*.test.tsx`）。

---

## Task 1: 类型 + ConfirmDialog

**Files:**
- Create: `web/src/features/common/crud/types.ts`
- Create: `web/src/features/common/crud/ConfirmDialog.tsx`
- Test: `web/src/features/common/crud/ConfirmDialog.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// ConfirmDialog.test.tsx
import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { ConfirmDialog } from "./ConfirmDialog"

describe("ConfirmDialog", () => {
  it("确认调 onConfirm、取消调 onCancel 不调 onConfirm", async () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    render(
      <ConfirmDialog open title="确认移除成员？" description="将移除 alice"
        confirmLabel="确认移除" onConfirm={onConfirm} onCancel={onCancel} />,
    )
    expect(screen.getByText("确认移除成员？")).toBeInTheDocument()
    expect(screen.getByText("将移除 alice")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "取消" }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole("button", { name: "确认移除" }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it("open=false 不渲染内容", () => {
    render(<ConfirmDialog open={false} title="X" onConfirm={() => {}} onCancel={() => {}} />)
    expect(screen.queryByText("X")).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/features/common/crud/ConfirmDialog.test.tsx`
Expected: FAIL（模块不存在 / Cannot find module './ConfirmDialog'）

- [ ] **Step 3: Write types.ts**

```tsx
// types.ts
import type { ReactNode } from "react"

// 行级动作描述符：表达 edit/delete/set-default/reveal/copy 等所有行内/卡片角操作。
export interface RowAction<T> {
  label: string
  icon?: ReactNode
  onClick: (item: T) => void
  variant?: "ghost" | "amber" | "destructive"
  hidden?: (item: T) => boolean
  disabled?: (item: T) => boolean
}

// 表格列定义。
export interface Column<T> {
  key: string
  header: ReactNode
  cell: (item: T) => ReactNode
  className?: string
}
```

- [ ] **Step 4: Write ConfirmDialog.tsx**

```tsx
// ConfirmDialog.tsx
import type { ReactNode } from "react"
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Button as UiButton } from "@/components/ui/button"

export interface ConfirmDialogProps {
  open: boolean
  title: string
  description?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  variant?: "danger" | "default"
  confirming?: boolean
  onConfirm: () => void
  onCancel: () => void
}

// 通用二次确认。删除/移除/撤销/重置共用；danger 用 destructive 按钮。
export function ConfirmDialog({
  open, title, description, confirmLabel = "确认", cancelLabel = "取消",
  variant = "danger", confirming = false, onConfirm, onCancel,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onCancel() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description != null && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        <DialogFooter>
          <UiButton variant="outline" onClick={onCancel}>{cancelLabel}</UiButton>
          <UiButton
            variant={variant === "danger" ? "destructive" : "default"}
            disabled={confirming}
            onClick={onConfirm}
          >
            {confirmLabel}
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `npx vitest run src/features/common/crud/ConfirmDialog.test.tsx`
Expected: PASS（2 passed）

- [ ] **Step 6: Commit**

```bash
git add src/features/common/crud/types.ts src/features/common/crud/ConfirmDialog.tsx src/features/common/crud/ConfirmDialog.test.tsx
git commit -m "feat(crud): ConfirmDialog + RowAction/Column 类型——通用确认与行动作描述符"
```

---

## Task 2: RevealSecretInput

**Files:**
- Create: `web/src/features/common/crud/RevealSecretInput.tsx`
- Test: `web/src/features/common/crud/RevealSecretInput.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// RevealSecretInput.test.tsx
import { describe, it, expect, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { RevealSecretInput } from "./RevealSecretInput"

describe("RevealSecretInput", () => {
  it("默认 password 类型，点眼睛切到 text", async () => {
    render(<RevealSecretInput value="s3cr3t" onChange={() => {}} />)
    const input = screen.getByLabelText("密钥输入")
    expect(input).toHaveAttribute("type", "password")
    await userEvent.click(screen.getByRole("button", { name: "显示/隐藏密钥" }))
    expect(input).toHaveAttribute("type", "text")
  })

  it("alreadySet 显示「留空保持不变」提示", () => {
    render(<RevealSecretInput value="" onChange={() => {}} alreadySet />)
    expect(screen.getByText(/留空保持不变/)).toBeInTheDocument()
  })

  it("onReveal 异步解密填入值", async () => {
    const onChange = vi.fn()
    const onReveal = vi.fn().mockResolvedValue("decrypted-key")
    render(<RevealSecretInput value="" onChange={onChange} alreadySet onReveal={onReveal} />)
    await userEvent.click(screen.getByRole("button", { name: "显示已存密钥" }))
    await waitFor(() => expect(onReveal).toHaveBeenCalled())
    await waitFor(() => expect(onChange).toHaveBeenCalledWith("decrypted-key"))
  })

  it("无 onReveal 时不渲染「显示已存密钥」按钮", () => {
    render(<RevealSecretInput value="" onChange={() => {}} alreadySet />)
    expect(screen.queryByRole("button", { name: "显示已存密钥" })).not.toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/features/common/crud/RevealSecretInput.test.tsx`
Expected: FAIL（Cannot find module './RevealSecretInput'）

- [ ] **Step 3: Write implementation**

```tsx
// RevealSecretInput.tsx
import { useState } from "react"
import { Eye, EyeOff } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Button as UiButton } from "@/components/ui/button"

export interface RevealSecretInputProps {
  id?: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  alreadySet?: boolean
  onReveal?: () => Promise<string>
  disabled?: boolean
}

// 密钥输入：Eye 明文切换；alreadySet 提示「留空保持不变」；
// 传 onReveal 时多一个「显示已存密钥」按钮（异步解密回填）。不传则退化为普通 password。
export function RevealSecretInput({
  id, value, onChange, placeholder, alreadySet = false, onReveal, disabled = false,
}: RevealSecretInputProps) {
  const [show, setShow] = useState(false)
  const [revealing, setRevealing] = useState(false)
  const [revealError, setRevealError] = useState<string | null>(null)

  async function handleReveal() {
    if (!onReveal) return
    setRevealing(true)
    setRevealError(null)
    try {
      const v = await onReveal()
      onChange(v)
      setShow(true)
    } catch {
      setRevealError("无法读取已存密钥")
    } finally {
      setRevealing(false)
    }
  }

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-2">
        <Input
          id={id}
          aria-label="密钥输入"
          type={show ? "text" : "password"}
          value={value}
          placeholder={placeholder}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
        <UiButton type="button" variant="ghost" size="sm"
          aria-label="显示/隐藏密钥" onClick={() => setShow((s) => !s)}>
          {show ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
        </UiButton>
        {onReveal && (
          <UiButton type="button" variant="outline" size="sm"
            aria-label="显示已存密钥" disabled={revealing} onClick={handleReveal}>
            {revealing ? "读取中…" : "显示已存"}
          </UiButton>
        )}
      </div>
      {alreadySet && (
        <p className="text-[11px] text-text-3">已配置，留空保持不变。</p>
      )}
      {revealError && <p className="text-[11px] text-red-400">{revealError}</p>}
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/features/common/crud/RevealSecretInput.test.tsx`
Expected: PASS（4 passed）。若 `lucide-react` 的 Eye/EyeOff 导入报错，确认项目已用该库（ModelConfigPage 已用 Eye/EyeOff，照其导入方式）。

- [ ] **Step 5: Commit**

```bash
git add src/features/common/crud/RevealSecretInput.tsx src/features/common/crud/RevealSecretInput.test.tsx
git commit -m "feat(crud): RevealSecretInput——密钥明文切换 + 可选 reveal-key 异步回填"
```

---

## Task 3: DataView（双模式列表）

**Files:**
- Create: `web/src/features/common/crud/DataView.tsx`
- Test: `web/src/features/common/crud/DataView.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// DataView.test.tsx
import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { DataView } from "./DataView"

interface Row { id: string; name: string; kind: string }
const rows: Row[] = [
  { id: "a", name: "Alpha", kind: "text" },
  { id: "b", name: "Beta", kind: "image" },
]

describe("DataView", () => {
  it("table 模式渲染列 + 行动作", async () => {
    const onEdit = vi.fn()
    render(
      <DataView<Row> layout="table" items={rows} getId={(r) => r.id}
        columns={[{ key: "name", header: "名称", cell: (r) => r.name }]}
        rowActions={[{ label: "编辑", onClick: onEdit }]} />,
    )
    expect(screen.getByText("名称")).toBeInTheDocument()
    expect(screen.getByText("Alpha")).toBeInTheDocument()
    await userEvent.click(screen.getAllByRole("button", { name: "编辑" })[0])
    expect(onEdit).toHaveBeenCalledWith(rows[0])
  })

  it("cards 模式用 renderCard，actions 传入卡片", () => {
    render(
      <DataView<Row> layout="cards" items={rows} getId={(r) => r.id}
        rowActions={[{ label: "删除", onClick: () => {} }]}
        renderCard={(r, actions) => (
          <div data-testid="card">{r.name}{actions}</div>
        )} />,
    )
    expect(screen.getAllByTestId("card")).toHaveLength(2)
    expect(screen.getAllByRole("button", { name: "删除" })).toHaveLength(2)
  })

  it("cards 模式 groupBy 按组渲染分组标题", () => {
    render(
      <DataView<Row> layout="cards" items={rows} getId={(r) => r.id}
        groupBy={(r) => r.kind}
        renderCard={(r) => <div>{r.name}</div>} />,
    )
    expect(screen.getByText("text")).toBeInTheDocument()
    expect(screen.getByText("image")).toBeInTheDocument()
  })

  it("hidden 的行动作不渲染", () => {
    render(
      <DataView<Row> layout="table" items={rows} getId={(r) => r.id}
        columns={[{ key: "name", header: "名称", cell: (r) => r.name }]}
        rowActions={[{ label: "设默认", onClick: () => {}, hidden: (r) => r.id === "a" }]} />,
    )
    // 只有 b 行有「设默认」
    expect(screen.getAllByRole("button", { name: "设默认" })).toHaveLength(1)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/features/common/crud/DataView.test.tsx`
Expected: FAIL（Cannot find module './DataView'）

- [ ] **Step 3: Write implementation**

```tsx
// DataView.tsx
import type { ReactNode } from "react"
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table"
import { Button as UiButton } from "@/components/ui/button"
import type { Column, RowAction } from "./types"

interface DataViewProps<T> {
  items: T[]
  getId: (item: T) => string
  layout: "table" | "cards"
  rowActions?: RowAction<T>[]
  columns?: Column<T>[]
  renderCard?: (item: T, actions: ReactNode) => ReactNode
  groupBy?: (item: T) => string
  minWidthClass?: string
}

function ActionButtons<T>({ item, actions }: { item: T; actions: RowAction<T>[] }) {
  return (
    <>
      {actions
        .filter((a) => !a.hidden?.(item))
        .map((a) => (
          <UiButton key={a.label} variant={a.variant === "amber" ? "default" : a.variant ?? "ghost"}
            size="sm" aria-label={a.label} disabled={a.disabled?.(item)}
            onClick={() => a.onClick(item)}>
            {a.icon}{a.label}
          </UiButton>
        ))}
    </>
  )
}

// 双模式列表：table 用 columns + 末列 rowActions；cards 用 renderCard(item, actions) + 可选 groupBy。
// 空态由上层 CrudResourcePage 负责，这里假定 items 非空。
export function DataView<T>({
  items, getId, layout, rowActions = [], columns = [], renderCard, groupBy, minWidthClass,
}: DataViewProps<T>) {
  if (layout === "table") {
    return (
      <Table className={minWidthClass}>
        <TableHeader>
          <TableRow>
            {columns.map((c) => <TableHead key={c.key} className={c.className}>{c.header}</TableHead>)}
            {rowActions.length > 0 && <TableHead className="text-right">操作</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => (
            <TableRow key={getId(item)}>
              {columns.map((c) => <TableCell key={c.key} className={c.className}>{c.cell(item)}</TableCell>)}
              {rowActions.length > 0 && (
                <TableCell className="text-right">
                  <ActionButtons item={item} actions={rowActions} />
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )
  }

  // cards
  const renderItems = (list: T[]) =>
    list.map((item) => (
      <div key={getId(item)}>
        {renderCard?.(item, <ActionButtons item={item} actions={rowActions} />)}
      </div>
    ))

  if (groupBy) {
    const groups = new Map<string, T[]>()
    for (const item of items) {
      const k = groupBy(item)
      const arr = groups.get(k) ?? []
      arr.push(item)
      groups.set(k, arr)
    }
    return (
      <div className="flex flex-col gap-6">
        {[...groups.entries()].map(([key, list]) => (
          <section key={key} className="flex flex-col gap-3">
            <h3 className="text-[13px] font-semibold text-text-2">{key}</h3>
            <div className="flex flex-col gap-3">{renderItems(list)}</div>
          </section>
        ))}
      </div>
    )
  }
  return <div className="flex flex-col gap-3">{renderItems(items)}</div>
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/features/common/crud/DataView.test.tsx`
Expected: PASS（4 passed）

- [ ] **Step 5: Commit**

```bash
git add src/features/common/crud/DataView.tsx src/features/common/crud/DataView.test.tsx
git commit -m "feat(crud): DataView——table/cards 双模式列表 + rowActions + groupBy"
```

---

## Task 4: FormDialog

**Files:**
- Create: `web/src/features/common/crud/FormDialog.tsx`
- Test: `web/src/features/common/crud/FormDialog.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// FormDialog.test.tsx
import { describe, it, expect, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useFormContext } from "react-hook-form"
import { z } from "zod"
import { FormDialog } from "./FormDialog"

const schema = z.object({ name: z.string().min(1, "必填") })

function NameField() {
  const { register, formState } = useFormContext<{ name: string }>()
  return (
    <div>
      <input aria-label="名称" {...register("name")} />
      {formState.errors.name && <span>{formState.errors.name.message}</span>}
    </div>
  )
}

describe("FormDialog", () => {
  it("编辑模式预填 defaultValues，提交回调拿到值", async () => {
    const onSubmit = vi.fn()
    render(
      <FormDialog open mode="edit" title="编辑提示词" schema={schema}
        defaultValues={{ name: "旧名" }} onSubmit={onSubmit} onOpenChange={() => {}}>
        <NameField />
      </FormDialog>,
    )
    const input = screen.getByLabelText("名称") as HTMLInputElement
    expect(input.value).toBe("旧名")
    await userEvent.clear(input)
    await userEvent.type(input, "新名")
    await userEvent.click(screen.getByRole("button", { name: "保存" }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ name: "新名" }))
  })

  it("校验失败不提交，显示字段错误", async () => {
    const onSubmit = vi.fn()
    render(
      <FormDialog open mode="create" title="新建" schema={schema}
        defaultValues={{ name: "" }} onSubmit={onSubmit} onOpenChange={() => {}}>
        <NameField />
      </FormDialog>,
    )
    await userEvent.click(screen.getByRole("button", { name: "创建" }))
    expect(await screen.findByText("必填")).toBeInTheDocument()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("submitError 展示在底部", () => {
    render(
      <FormDialog open mode="create" title="新建" schema={schema}
        defaultValues={{ name: "" }} submitError="名称已存在"
        onSubmit={() => {}} onOpenChange={() => {}}>
        <NameField />
      </FormDialog>,
    )
    expect(screen.getByText("名称已存在")).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/features/common/crud/FormDialog.test.tsx`
Expected: FAIL（Cannot find module './FormDialog'）

- [ ] **Step 3: Write implementation**

```tsx
// FormDialog.tsx
import { useEffect, type ReactNode } from "react"
import { useForm, FormProvider, type DefaultValues, type FieldValues } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import type { ZodType } from "zod"
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"

interface FormDialogProps<T extends FieldValues> {
  open: boolean
  mode: "create" | "edit"
  title: string
  schema: ZodType<T>
  defaultValues: DefaultValues<T>
  submitLabel?: string
  submitting?: boolean
  submitError?: string | null
  onSubmit: (values: T) => void
  onOpenChange: (open: boolean) => void
  children: ReactNode
}

// 表单对话框壳：拥有 Dialog 开合 + rhf FormProvider(zodResolver) + 提交/取消/错误。
// 字段由资源页作为 children 传入，用 useFormContext() 读写。
// open/defaultValues 变化时 reset，保证创建↔编辑切换预填正确。
export function FormDialog<T extends FieldValues>({
  open, mode, title, schema, defaultValues, submitLabel,
  submitting = false, submitError, onSubmit, onOpenChange, children,
}: FormDialogProps<T>) {
  const form = useForm<T>({ resolver: zodResolver(schema), defaultValues })
  useEffect(() => {
    if (open) form.reset(defaultValues)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, mode])

  const label = submitLabel ?? (mode === "create" ? "创建" : "保存")
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <FormProvider {...form}>
          <form
            className="flex flex-col gap-3"
            onSubmit={form.handleSubmit((v) => onSubmit(v))}
          >
            {children}
            {submitError != null && submitError !== "" && (
              <p className="text-[12px] text-red-400">{submitError}</p>
            )}
            <DialogFooter>
              <UiButton type="button" variant="outline" onClick={() => onOpenChange(false)}>
                取消
              </UiButton>
              <Button type="submit" variant="amber" disabled={submitting}>
                {label}
              </Button>
            </DialogFooter>
          </form>
        </FormProvider>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/features/common/crud/FormDialog.test.tsx`
Expected: PASS（3 passed）。`@hookform/resolvers/zod` 已在用（StorageConfigPage 用 zodResolver），照其导入路径。

- [ ] **Step 5: Commit**

```bash
git add src/features/common/crud/FormDialog.tsx src/features/common/crud/FormDialog.test.tsx
git commit -m "feat(crud): FormDialog——rhf+zod 表单对话框壳,字段委托给资源页 children"
```

---

## Task 5: useCrudResource（状态机）

**Files:**
- Create: `web/src/features/common/crud/useCrudResource.ts`
- Test: `web/src/features/common/crud/useCrudResource.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// useCrudResource.test.tsx
import { describe, it, expect, vi, beforeEach } from "vitest"
import { renderHook, act, waitFor } from "@testing-library/react"
import { Toaster } from "sonner"
import { render, screen } from "@testing-library/react"
import { useCrudResource } from "./useCrudResource"

interface Row { id: string; name: string }
function setup(overrides: Partial<Parameters<typeof useCrudResource<Row>>[0]> = {}) {
  const create = vi.fn().mockResolvedValue(undefined)
  const update = vi.fn().mockResolvedValue(undefined)
  const remove = vi.fn().mockResolvedValue(undefined)
  const hook = renderHook(() =>
    useCrudResource<Row>({
      getId: (r) => r.id, create, update, remove,
      labels: { created: "已创建", updated: "已更新", deleted: "已删除" },
      ...overrides,
    }),
  )
  return { hook, create, update, remove }
}

describe("useCrudResource", () => {
  it("openCreate / openEdit / closeDialog 切换 dialog 状态", () => {
    const { hook } = setup()
    act(() => hook.result.current.openCreate())
    expect(hook.result.current.dialog).toEqual({ mode: "create", target: null })
    act(() => hook.result.current.openEdit({ id: "a", name: "A" }))
    expect(hook.result.current.dialog).toEqual({ mode: "edit", target: { id: "a", name: "A" } })
    act(() => hook.result.current.closeDialog())
    expect(hook.result.current.dialog).toBeNull()
  })

  it("submit 在 create 态调 create、edit 态调 update(id, values)，成功后关闭", async () => {
    const { hook, create, update } = setup()
    act(() => hook.result.current.openCreate())
    await act(async () => { hook.result.current.submit({ name: "新" }) })
    await waitFor(() => expect(create).toHaveBeenCalledWith({ name: "新" }))
    await waitFor(() => expect(hook.result.current.dialog).toBeNull())

    act(() => hook.result.current.openEdit({ id: "a", name: "A" }))
    await act(async () => { hook.result.current.submit({ name: "改" }) })
    await waitFor(() => expect(update).toHaveBeenCalledWith("a", { name: "改" }))
  })

  it("requestDelete/confirmDelete 调 remove(id) 并清空 deleteTarget", async () => {
    const { hook, remove } = setup()
    act(() => hook.result.current.requestDelete({ id: "a", name: "A" }))
    expect(hook.result.current.deleteTarget).toEqual({ id: "a", name: "A" })
    await act(async () => { hook.result.current.confirmDelete() })
    await waitFor(() => expect(remove).toHaveBeenCalledWith("a"))
    await waitFor(() => expect(hook.result.current.deleteTarget).toBeNull())
  })

  it("submit 失败时 submitError 经 errorMessage 映射、不关闭", async () => {
    const create = vi.fn().mockRejectedValue(new Error("boom"))
    const { hook } = setup({ create, errorMessage: () => "名称已存在" })
    act(() => hook.result.current.openCreate())
    await act(async () => { hook.result.current.submit({ name: "x" }) })
    await waitFor(() => expect(hook.result.current.submitError).toBe("名称已存在"))
    expect(hook.result.current.dialog).not.toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/features/common/crud/useCrudResource.test.tsx`
Expected: FAIL（Cannot find module './useCrudResource'）

- [ ] **Step 3: Write implementation**

```tsx
// useCrudResource.ts
import { useState } from "react"
import { toast } from "sonner"

export interface CrudConfig<T> {
  getId: (item: T) => string
  create: (input: unknown) => Promise<unknown>
  update: (id: string, input: unknown) => Promise<unknown>
  remove: (id: string) => Promise<unknown>
  labels?: { created?: string; updated?: string; deleted?: string }
  // 把错误映射成用户文案；action 区分 create/update/delete。返回的字符串用于 toast / submitError。
  errorMessage?: (action: "create" | "update" | "delete", err: unknown) => string
}

interface DialogState<T> { mode: "create" | "edit"; target: T | null }

export interface CrudResource<T> {
  dialog: DialogState<T> | null
  deleteTarget: T | null
  submitError: string | null
  submitting: boolean
  deleting: boolean
  openCreate: () => void
  openEdit: (item: T) => void
  closeDialog: () => void
  requestDelete: (item: T) => void
  cancelDelete: () => void
  confirmDelete: () => void
  submit: (values: unknown) => void
}

const defaultErr = () => "操作失败，请重试"

// headless CRUD 状态机：拥有 dialog/deleteTarget 状态 + 调用注入的 create/update/remove
// （通常是各 api.ts hook 的 mutateAsync，已自带 invalidate），并接 toast/错误文案。
export function useCrudResource<T>(cfg: CrudConfig<T>): CrudResource<T> {
  const [dialog, setDialog] = useState<DialogState<T> | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<T | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const msg = cfg.errorMessage ?? defaultErr

  function openCreate() { setSubmitError(null); setDialog({ mode: "create", target: null }) }
  function openEdit(item: T) { setSubmitError(null); setDialog({ mode: "edit", target: item }) }
  function closeDialog() { setDialog(null); setSubmitError(null) }
  function requestDelete(item: T) { setDeleteTarget(item) }
  function cancelDelete() { setDeleteTarget(null) }

  function submit(values: unknown) {
    if (!dialog) return
    const isEdit = dialog.mode === "edit"
    setSubmitting(true)
    setSubmitError(null)
    const p = isEdit && dialog.target
      ? cfg.update(cfg.getId(dialog.target), values)
      : cfg.create(values)
    p.then(() => {
      toast.success(isEdit ? cfg.labels?.updated ?? "已更新" : cfg.labels?.created ?? "已创建")
      setDialog(null)
    }).catch((err) => {
      setSubmitError(msg(isEdit ? "update" : "create", err))
    }).finally(() => setSubmitting(false))
  }

  function confirmDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    cfg.remove(cfg.getId(deleteTarget))
      .then(() => {
        toast.success(cfg.labels?.deleted ?? "已删除")
        setDeleteTarget(null)
      })
      .catch((err) => toast.error(msg("delete", err)))
      .finally(() => setDeleting(false))
  }

  return {
    dialog, deleteTarget, submitError, submitting, deleting,
    openCreate, openEdit, closeDialog, requestDelete, cancelDelete, confirmDelete, submit,
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest run src/features/common/crud/useCrudResource.test.tsx`
Expected: PASS（4 passed）

- [ ] **Step 5: Commit**

```bash
git add src/features/common/crud/useCrudResource.ts src/features/common/crud/useCrudResource.test.tsx
git commit -m "feat(crud): useCrudResource——dialog/delete 状态机 + submit/confirmDelete 接 toast/错误映射"
```

---

## Task 6: CrudResourcePage + SingletonConfigForm + 桶导出

**Files:**
- Create: `web/src/features/common/crud/CrudResourcePage.tsx`
- Create: `web/src/features/common/crud/index.ts`
- Test: `web/src/features/common/crud/CrudResourcePage.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// CrudResourcePage.test.tsx
import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { CrudResourcePage, SingletonConfigForm } from "./CrudResourcePage"
import { z } from "zod"
import { useFormContext } from "react-hook-form"

describe("CrudResourcePage", () => {
  it("加载态渲染 Skeleton，不渲染 children", () => {
    render(
      <CrudResourcePage title="提示词" isLoading isError={false} isEmpty={false}>
        <div data-testid="body">x</div>
      </CrudResourcePage>,
    )
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("错误态显示重试，点击调 onRetry", async () => {
    const onRetry = vi.fn()
    render(
      <CrudResourcePage title="提示词" isLoading={false} isError onRetry={onRetry} isEmpty={false}>
        <div />
      </CrudResourcePage>,
    )
    await userEvent.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalled()
  })

  it("空态显示 emptyHint", () => {
    render(
      <CrudResourcePage title="提示词" isLoading={false} isError={false} isEmpty emptyHint="暂无提示词">
        <div data-testid="body" />
      </CrudResourcePage>,
    )
    expect(screen.getByText("暂无提示词")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("正常态：标题 + 新增按钮(onCreate) + children", async () => {
    const onCreate = vi.fn()
    render(
      <CrudResourcePage title="提示词" createLabel="新增提示词" onCreate={onCreate}
        isLoading={false} isError={false} isEmpty={false}>
        <div data-testid="body">列表</div>
      </CrudResourcePage>,
    )
    expect(screen.getByText("提示词")).toBeInTheDocument()
    expect(screen.getByTestId("body")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "新增提示词" }))
    expect(onCreate).toHaveBeenCalled()
  })
})

describe("SingletonConfigForm", () => {
  const schema = z.object({ host: z.string() })
  function Field() {
    const { register } = useFormContext<{ host: string }>()
    return <input aria-label="主机" {...register("host")} />
  }
  it("预填 values 并提交", async () => {
    const onSubmit = vi.fn()
    render(
      <SingletonConfigForm title="邮件" schema={schema} values={{ host: "smtp.x" }}
        isLoading={false} onSubmit={onSubmit}>
        <Field />
      </SingletonConfigForm>,
    )
    expect((screen.getByLabelText("主机") as HTMLInputElement).value).toBe("smtp.x")
    await userEvent.click(screen.getByRole("button", { name: "保存" }))
    expect(onSubmit).toHaveBeenCalledWith({ host: "smtp.x" })
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest run src/features/common/crud/CrudResourcePage.test.tsx`
Expected: FAIL（Cannot find module './CrudResourcePage'）

- [ ] **Step 3: Write implementation**

```tsx
// CrudResourcePage.tsx
import { useEffect, type ReactNode } from "react"
import { useForm, FormProvider, type DefaultValues, type FieldValues } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import type { ZodType } from "zod"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"

interface CrudResourcePageProps {
  title: string
  description?: ReactNode
  createLabel?: string
  onCreate?: () => void
  isLoading: boolean
  isError: boolean
  onRetry?: () => void
  isEmpty: boolean
  emptyHint?: string
  headerExtra?: ReactNode
  children: ReactNode
}

// 配置/管理页外壳：页头(标题+描述+新增) + 加载/错误/空态；正常态渲染 children(列表 + 对话框由资源页挂载)。
export function CrudResourcePage({
  title, description, createLabel, onCreate, isLoading, isError, onRetry,
  isEmpty, emptyHint = "暂无数据。", headerExtra, children,
}: CrudResourcePageProps) {
  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1.5">
          <h1 className="font-heading text-[22px] font-bold text-text-1">{title}</h1>
          {description != null && <p className="text-[12px] text-text-3">{description}</p>}
        </div>
        {onCreate && (
          <Button variant="amber" onClick={onCreate}>{createLabel ?? "新增"}</Button>
        )}
      </header>
      {headerExtra}
      {isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">加载失败</p>
          {onRetry && <Button variant="ghost" onClick={onRetry}>重试</Button>}
        </div>
      ) : isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-10 rounded-lg" />)}
        </div>
      ) : isEmpty ? (
        <p className="py-8 text-center text-[13px] text-text-3">{emptyHint}</p>
      ) : (
        children
      )}
    </div>
  )
}

interface SingletonConfigFormProps<T extends FieldValues> {
  title: string
  description?: ReactNode
  schema: ZodType<T>
  values: T | undefined
  isLoading: boolean
  submitLabel?: string
  submitting?: boolean
  onSubmit: (values: T) => void
  children: ReactNode
}

// 单记录 upsert 表单（PlatformAdmin 的全局邮件/全局存储用）：拉一条 → 表单 → 保存。
export function SingletonConfigForm<T extends FieldValues>({
  title, description, schema, values, isLoading, submitLabel = "保存",
  submitting = false, onSubmit, children,
}: SingletonConfigFormProps<T>) {
  const form = useForm<T>({ resolver: zodResolver(schema), defaultValues: values as DefaultValues<T> })
  useEffect(() => { if (values) form.reset(values) /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [values])
  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">{title}</h2>
        {description != null && <p className="text-[12px] text-text-3">{description}</p>}
      </header>
      {isLoading ? (
        <Skeleton className="h-24 rounded-lg" />
      ) : (
        <FormProvider {...form}>
          <form className="flex flex-col gap-3" onSubmit={form.handleSubmit((v) => onSubmit(v))}>
            {children}
            <div className="flex justify-end">
              <Button type="submit" variant="amber" disabled={submitting}>{submitLabel}</Button>
            </div>
          </form>
        </FormProvider>
      )}
    </section>
  )
}
```

```tsx
// index.ts
export { ConfirmDialog } from "./ConfirmDialog"
export type { ConfirmDialogProps } from "./ConfirmDialog"
export { RevealSecretInput } from "./RevealSecretInput"
export { DataView } from "./DataView"
export { FormDialog } from "./FormDialog"
export { useCrudResource } from "./useCrudResource"
export type { CrudResource, CrudConfig } from "./useCrudResource"
export { CrudResourcePage, SingletonConfigForm } from "./CrudResourcePage"
export type { RowAction, Column } from "./types"
```

- [ ] **Step 4: Run test + typecheck**

Run: `npx vitest run src/features/common/crud/CrudResourcePage.test.tsx`
Expected: PASS（5 passed）
Run: `npx vitest run src/features/common/crud && npx tsc -b`
Expected: 全部 framework 测试 PASS、tsc 无错。

- [ ] **Step 5: Commit**

```bash
git add src/features/common/crud/CrudResourcePage.tsx src/features/common/crud/index.ts src/features/common/crud/CrudResourcePage.test.tsx
git commit -m "feat(crud): CrudResourcePage 外壳 + SingletonConfigForm + 桶导出"
```

---

## Task 7: 迁移 Members → 框架

**Files:**
- Modify: `web/src/features/members/MembersPage.tsx`（全量重写为框架组合，行为不变）
- Test: `web/src/features/members/MembersPage.test.tsx`（保留全部断言，调选择器）

**迁移要点（保持行为/呈现不变）：**
- Members 无创建 FormDialog（添加是页头内联 email+role+添加按钮）→ 用 `CrudResourcePage` 的 `headerExtra` 放现有「添加成员」section。
- 列表用 `DataView layout="table"`，columns = [邮箱, 角色(内联 select)]，rowActions = [移除]。
- 角色内联 select 的 `handleSetRole` 逻辑、错误 toast 文案**原样保留**（移进 column cell）。
- 移除确认用 `ConfirmDialog`，由 `useCrudResource` 的 `deleteTarget`/`requestDelete`/`confirmDelete` 驱动；`remove` 注入 `useRemoveMember().mutateAsync`；`errorMessage("delete", err)` 复用现有 409/404 文案。
- 保留所有 `aria-label`（如 `角色 ${m.email}`、`移除成员 ${m.email}`）以免测试选择器失效。

- [ ] **Step 1: 改测试预期（先让其 fail）**

在 `MembersPage.test.tsx`：把 `vi.mock("./api", ...)` 的 `useRemoveMember` 改为返回带 `mutateAsync` 的对象（`{ mutate: removeMutate, mutateAsync: vi.fn().mockResolvedValue({ok:true}), isPending: false }`），其余断言不变（仍断言渲染成员、内联改角色调 setRole、移除二次确认）。

Run: `npx vitest run src/features/members/MembersPage.test.tsx`
Expected: 先 FAIL 或仍 PASS（取决于断言）——本步只是对齐 mock 形态；真正驱动重写的是 Step 2 的结构。

- [ ] **Step 2: 重写 MembersPage.tsx 为框架组合**

保留文件顶部 `ROLE_OPTIONS`、`selectClass`、`handleAdd`/`handleSetRole` 不变。把渲染体改为：

```tsx
// 关键结构（其余既有 import / ROLE_OPTIONS / selectClass / handleAdd / handleSetRole 原样保留）
import { CrudResourcePage, DataView, ConfirmDialog, useCrudResource } from "@/features/common/crud"
import type { Column } from "@/features/common/crud"

// ...在组件内：
const crud = useCrudResource<OrgMember>({
  getId: (m) => m.userId,
  create: async () => {}, update: async () => {},
  remove: (id) => remove.mutateAsync(id),
  labels: { deleted: "已移除成员" },
  errorMessage: (_a, err) =>
    err instanceof ApiError && err.status === 409
      ? "不能移除或降级最后一个组织管理员"
      : err instanceof ApiError && err.status === 404
        ? "该用户不是本组织成员"
        : "移除失败，请重试",
})

const columns: Column<OrgMember>[] = [
  { key: "email", header: "邮箱", cell: (m) => <span className="text-text-1">{m.email}</span> },
  { key: "role", header: "角色", cell: (m) => (
    <select aria-label={`角色 ${m.email}`} value={m.role} disabled={setRole.isPending}
      onChange={(e) => handleSetRole(m, e.target.value as OrgRole)} className={selectClass}>
      {ROLE_OPTIONS.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
    </select>
  ) },
]

return (
  <>
    <CrudResourcePage
      title="成员管理"
      description="管理本组织成员与角色。按邮箱添加；行内可改角色；不能移除或降级最后一名组织管理员。"
      isLoading={members.isLoading} isError={members.isError} onRetry={() => void members.refetch()}
      isEmpty={!!members.data && members.data.length === 0} emptyHint="暂无成员。"
      headerExtra={/* 现有「添加成员」section JSX 原样搬入 */ null}
    >
      <DataView<OrgMember> layout="table" minWidthClass="min-w-[560px]"
        items={members.data ?? []} getId={(m) => m.userId} columns={columns}
        rowActions={[{ label: "移除", onClick: (m) => crud.requestDelete(m) }]} />
    </CrudResourcePage>
    <ConfirmDialog
      open={crud.deleteTarget != null}
      title="确认移除成员？"
      description={crud.deleteTarget ? `将移除 ${crud.deleteTarget.email} 在本组织的成员身份。此操作可重新添加。` : ""}
      confirmLabel="确认移除" confirming={crud.deleting}
      onConfirm={crud.confirmDelete} onCancel={crud.cancelDelete} />
  </>
)
```

把原「添加成员」`<section>`（含 email/role 输入 + 添加按钮，行 124–167）原样作为 `headerExtra` 传入（移除其外层重复的 members 列表分支——列表改由 DataView 渲染）。`rowActions` 的「移除」需保留可访问名：给该 action 的按钮 aria-label 为 `移除`（DataView 已用 `label` 作 aria-label；若测试按 `移除成员 ${email}` 选择，则改测试选择器为 `移除` 或给 rowActions 增加按 item 定制 label 的能力——优先改测试选择器，不扩框架）。

- [ ] **Step 3: 跑 Members 测试 + 调选择器**

Run: `npx vitest run src/features/members/MembersPage.test.tsx`
Expected: PASS。若移除按钮断言因 aria-label 变化失败，把测试里 `getByRole("button", { name: /移除成员/ })` 改为 `getAllByRole("button", { name: "移除" })[0]`，断言不弱化（仍验证点击触发确认 + confirm 调 mutateAsync）。

- [ ] **Step 4: 类型 + 浏览器烟雾**

Run: `npx tsc -b`（无错）
浏览器：登录 → `/orgs/{org}/members`，确认列表/添加/改角色/移除确认与迁移前一致，截图 `/tmp/sp-a-members.png`。

- [ ] **Step 5: Commit**

```bash
git add src/features/members/MembersPage.tsx src/features/members/MembersPage.test.tsx
git commit -m "refactor(members): 迁移到通用 CRUD 框架(CrudResourcePage+DataView+ConfirmDialog),行为不变"
```

---

## Task 8: 迁移 Prompt → 框架（卡片 + FormDialog）

**Files:**
- Modify: `web/src/features/prompt/PromptListPage.tsx`
- Test: `web/src/features/prompt/PromptListPage.test.tsx`（保留断言）

**先读现状：** `cat -n src/features/prompt/PromptListPage.tsx` 与 `src/features/prompt/api.ts`，记录：列表 hook、create/update/delete/set-default/styles hook、表单字段（name/content/style/kind）、卡片渲染、复制逻辑、错误文案。

**迁移要点：**
- `DataView layout="cards"`，`renderCard` 用现有卡片 JSX（含预览框、复制按钮）。
- 创建/编辑用 `FormDialog`（schema=name/content/style/kind），字段 JSX 作为 children（用 `useFormContext`）；style 建议复用 `usePromptStyles` catalog。
- rowActions（卡片角）：编辑(openEdit)、设默认(set-default mutateAsync)、复制(现有 copy 逻辑)、删除(requestDelete)。
- `useCrudResource` 注入 create/update/delete 的 mutateAsync；`errorMessage` 复用现有文案。
- set-default、copy 不属于 create/update/delete，作为独立 rowAction onClick 直接调对应 hook（不进 useCrudResource）。

- [ ] **Step 1: 对齐测试 mock**：把 `usePrompt*` mutation mock 加 `mutateAsync`（resolve）。Run 测试确认基线。

- [ ] **Step 2: 重写 PromptListPage.tsx**：按上要点组合；表单字段组件可内联或抽 `PromptFields`（用 `useFormContext`）。保留所有 aria-label / testid（卡片、编辑、删除、设默认、复制、表单字段）。

- [ ] **Step 3: 跑测试 + 调选择器**：`npx vitest run src/features/prompt/PromptListPage.test.tsx` → PASS（保留断言：渲染卡片、style 后缀、创建/编辑绑定、set-default、删除确认、copy 设 copiedId）。

- [ ] **Step 4: 类型 + 烟雾**：`npx tsc -b`；浏览器 `/orgs/{org}/prompt` 截图 `/tmp/sp-a-prompt.png` 对比。

- [ ] **Step 5: Commit**

```bash
git add src/features/prompt/PromptListPage.tsx src/features/prompt/PromptListPage.test.tsx
git commit -m "refactor(prompt): 迁移到 CRUD 框架(cards+FormDialog),set-default/copy 作行动作,行为不变"
```

---

## Task 9: 迁移 StorageConfig → 框架（表格 + 条件字段 + 密钥）

**Files:**
- Modify: `web/src/features/storage/StorageConfigPage.tsx`
- Create: `web/src/features/storage/StorageModeFields.tsx`（抽出 mode 条件字段，用 `useFormContext`）
- Test: `web/src/features/storage/StorageConfigPage.test.tsx`（保留断言）；`web/src/features/storage/api.test.ts` 不动

**先读现状：** `cat -n src/features/storage/StorageConfigPage.tsx`，记录 `StorageConfigForm` 的 mode 条件字段（localfs/s3/oss/cos/github）、superRefine 校验、secret 处理、`StorageConfigsTable`、set-default、删除 409 文案。

**迁移要点：**
- 表格用 `DataView layout="table"`（沿用现有列：名称/类型/关键字段/启用/默认/has-secret/操作）。
- 创建/编辑用 `FormDialog`，字段 = `<StorageModeFields />`（含 mode select 驱动的条件字段 + `RevealSecretInput`(secret，alreadySet=hasSecret，无 onReveal)）。**superRefine 校验逻辑原样保留**进 schema。
- rowActions：编辑、设默认(set-default mutateAsync)、删除(requestDelete)。
- 删除 409 → `errorMessage("delete")` 返回「该配置仍被引用，请改为停用」。

- [ ] **Step 1: 抽 StorageModeFields.tsx**：把现有 `StorageConfigForm` 内 mode 条件字段块（`showObjectFields`/`showRegion`/`isGithub` 分支）搬成独立组件，用 `useFormContext` 读写；secret 字段换成 `<RevealSecretInput>`。先写其单测（mode=github 显 owner/repo 不显 bucket；mode=s3 显 endpoint/bucket）。

- [ ] **Step 2: 重写 StorageConfigPage.tsx**：用 CrudResourcePage + DataView(table) + FormDialog(children=StorageModeFields)；schema 含 superRefine 原逻辑。对齐测试 mock 的 mutateAsync。

- [ ] **Step 3: 跑测试**：`npx vitest run src/features/storage/StorageConfigPage.test.tsx` → PASS（保留：mode 字段可见性、secret 提示、create/update、set-default、删除 409 文案）。

- [ ] **Step 4: 类型 + 烟雾**：`npx tsc -b`；浏览器 `/orgs/{org}/storage-config` 新增/编辑各 mode 切换 + 截图 `/tmp/sp-a-storage.png`。

- [ ] **Step 5: Commit**

```bash
git add src/features/storage/StorageConfigPage.tsx src/features/storage/StorageModeFields.tsx src/features/storage/StorageModeFields.test.tsx src/features/storage/StorageConfigPage.test.tsx
git commit -m "refactor(storage): 迁移到 CRUD 框架,抽 StorageModeFields(条件字段)+RevealSecretInput,校验/行为不变"
```

---

## Task 10: 迁移 ModelConfig → 框架（分组卡片 + reveal-key + list-models）

**Files:**
- Modify: `web/src/features/cost/ModelConfigPage.tsx`
- Create: `web/src/features/cost/ModelConfigFields.tsx`（表单字段，用 `useFormContext`，含 list-models + RevealSecretInput(onReveal=reveal-key)）
- Test: `web/src/features/cost/ModelConfigPage.test.tsx`（保留断言）

**先读现状：** `cat -n src/features/cost/ModelConfigPage.tsx`，记录：分组(按 kind)卡片、CreateModelConfigForm 字段(provider/kind/model/baseUrl/apiKey/enabled/isDefault/params)、provider→baseUrl 必填联动、paramsText JSON 校验、reveal-key、list-models。

**迁移要点：**
- `DataView layout="cards" groupBy={(c)=>c.kind}`，renderCard 用现有配置卡片。
- 创建/编辑用 `FormDialog`，字段 = `<ModelConfigFields />`：provider/kind/model/baseUrl/params + `<RevealSecretInput onReveal={()=>revealKey.mutateAsync(id)}>`(编辑态) + list-models 按钮（现有 useListModels 逻辑）。
- paramsText JSON 校验、provider 联动 baseUrl 必填**原样保留**进 schema/字段。
- rowActions：编辑、删除。

- [ ] **Step 1: 抽 ModelConfigFields.tsx**（含 list-models + RevealSecretInput）+ 单测（provider=openai-compatible 时 baseUrl 必填提示；list-models 填充建议；paramsText 非法 JSON 报错）。

- [ ] **Step 2: 重写 ModelConfigPage.tsx**：CrudResourcePage + DataView(cards,groupBy=kind) + FormDialog(children=ModelConfigFields)。对齐 mock。

- [ ] **Step 3: 跑测试**：`npx vitest run src/features/cost/ModelConfigPage.test.tsx` → PASS（保留：按 kind 分组、表单字段绑定、reveal-key 解密/未存提示、list-models 填充、paramsText 校验、create/update、删除）。

- [ ] **Step 4: 类型 + 烟雾**：`npx tsc -b`；浏览器 `/orgs/{org}/model-configs` 新增/编辑/reveal/list-models + 截图 `/tmp/sp-a-modelconfig.png`。

- [ ] **Step 5: Commit**

```bash
git add src/features/cost/ModelConfigPage.tsx src/features/cost/ModelConfigFields.tsx src/features/cost/ModelConfigFields.test.tsx src/features/cost/ModelConfigPage.test.tsx
git commit -m "refactor(model-config): 迁移到 CRUD 框架(分组卡片),抽 ModelConfigFields(reveal-key+list-models),行为不变"
```

---

## Task 11: 迁移 PlatformAdmin → 组合多原语

**Files:**
- Modify: `web/src/features/platform/PlatformAdminPage.tsx`
- Create: `web/src/features/platform/sections/AdminsSection.tsx`、`UsersSection.tsx`、`MailConfigSection.tsx`、`GlobalStorageSection.tsx`、`OrgsSection.tsx`
- Test: `web/src/features/platform/PlatformAdminPage.test.tsx`（保留断言）；可加各 section 单测

**先读现状：** `cat -n src/features/platform/PlatformAdminPage.tsx` + `src/features/platform/api.ts`，记录五区块逻辑与文案（含「最后一个管理员」「sole-admin orgs」警告、reset-password、user detail）。

**迁移要点（把 1053 行拆成区块文件，外层 PlatformAdminPage 仅做 gate + 组装）：**
- `OrgsSection`：只读 `DataView layout="table"`（usePlatformOrgs），无 rowActions/无对话框。
- `AdminsSection`：`CrudResourcePage`(或内嵌) + 添加(email 输入→grant)、撤销(ConfirmDialog→revoke)。
- `UsersSection`：`DataView layout="table"`，rowActions=[详情(UserDetailDialog)、重置密码(UserResetPasswordDialog)、删除(ConfirmDialog→deleteUser，409→「最后一个管理员」)、toggle-admin(grant/revoke)]。用户详情/重置密码这类**非 CRUD 专属对话框保留为本 section 内组件**（不塞进框架）。
- `MailConfigSection`：`SingletonConfigForm`(useGlobalMailConfig/useUpsertGlobalMailConfig)，密钥用 `RevealSecretInput`(smtpPass)。
- `GlobalStorageSection`：`SingletonConfigForm` + `StorageModeFields`（复用 Task 9 抽出的组件）。
- 外层 `PlatformAdminPage` 保留现有 `PlatformGate`/whoami 路由门禁，body 改为顺序渲染五个 section。

- [ ] **Step 1: 抽 OrgsSection + MailConfigSection + GlobalStorageSection**（较独立），各补/迁移对应测试断言。

- [ ] **Step 2: 抽 AdminsSection + UsersSection**（含其专属对话框 UserDetail/UserDelete/UserResetPassword 移入 UsersSection 同目录）。

- [ ] **Step 3: 瘦身 PlatformAdminPage.tsx** 为 gate + 五 section 组装；删除已搬走的内联实现（清理孤儿 import）。

- [ ] **Step 4: 跑测试 + 调选择器**：`npx vitest run src/features/platform/PlatformAdminPage.test.tsx` → PASS（保留：gate/whoami、全局存储 upsert、admin toggle、delete 409 文案、reset-password、user detail orgs、sole-admin 警告）。

- [ ] **Step 5: 类型 + 烟雾**：`npx tsc -b`；浏览器平台管理页逐区块 + 截图 `/tmp/sp-a-platform.png`。

- [ ] **Step 6: Commit**

```bash
git add src/features/platform/
git commit -m "refactor(platform): 拆 PlatformAdmin(1053)为五 section,复用 CRUD 框架/SingletonConfigForm/StorageModeFields,行为不变"
```

---

## 收尾验收（全部任务后）

- [ ] 全量回归：`npx tsc -b`（无错） + `npx vitest run`（全绿）。
- [ ] 五页浏览器烟雾对比迁移前截图：零视觉/行为回归。
- [ ] 文件瘦身核对：五个原页面文件显著变小，`features/common/crud/` 原语 + 各资源字段组件各司其职。
- [ ] `superpowers:finishing-a-development-branch` 完成 `refactor/config-admin-crud-framework` 分支（push + PR + squash 合并，studio 手动 lockstep）。

---

## Self-Review 记录

- **Spec 覆盖**：§4 八原语 → Task 1–6；§5 五页迁移 → Task 7–11；§3 防泄漏（字段/渲染委托）→ FormDialog children + DataView renderCard/columns + RowAction；§8 测试 → 各 Task 的 TDD + 保留页面断言；§9 顺序 → Task 顺序一致；SingletonConfigForm/RevealSecretInput/StorageModeFields 覆盖 PlatformAdmin/Storage/ModelConfig 分叉。
- **类型一致性**：`RowAction<T>`/`Column<T>`(types.ts)、`useCrudResource` 的 `dialog{mode,target}`/`deleteTarget`/`submit`/`confirmDelete`、`FormDialog` 的 `mode/schema/defaultValues/onSubmit`、`DataView` 的 `layout/columns/renderCard/groupBy/rowActions`、`CrudResourcePage` 的 `isLoading/isError/isEmpty/onCreate`、`SingletonConfigForm` 的 `values/onSubmit` —— 跨任务命名一致，桶 `index.ts` 统一导出。
- **占位符**：框架任务(1–6)含完整测试+实现代码;迁移任务(7–11)给出确切组合结构与不变量(保留断言/aria-label/文案),迁移系既有代码再组合,不重抄原 JSX 全文(子代理读现有文件)。
