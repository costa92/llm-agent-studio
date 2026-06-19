# SP-E 资产库迁移至 CRUD 工作区外壳 Implementation Plan

> **✅ DONE（2026-06-19，PR #66 squash-merge）** — 3 任务完成并经 spec+代码质量两段审查 + 整特性 holistic review（Ready to merge）。全量 `vitest` 67 文件/450 用例绿、`tsc` 干净、SP-E 触及文件 `eslint` 无 error；浏览器烟雾截图确认资产库双列布局零视觉/行为回归。整特性审查另对齐了框架族 API（`CrudWorkspacePage` 补 `emptyHint`、状态切换顺序与 `CrudResourcePage` 一致）。下方勾选为完成留痕。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把资产库页（`web/src/features/library/LibraryPage.tsx` 的 `LibraryView`）的布局骨架抽成一个可复用的全高双列框架外壳 `web/src/features/common/crud/CrudWorkspacePage.tsx`，并把 `LibraryView` 重新包到该外壳上。**零视觉/行为回归**——外壳 markup 逐字搬自当前 `LibraryView`（class、间距、`py-20`、`w-56`、`overscroll-contain` 全保留），数据/过滤/keyset 加载更多/详情抽屉/移动端筛选 Sheet 均不变。

**Architecture:** 新建 sibling 外壳 `CrudWorkspacePage`（**不重载** `CrudResourcePage`，二者布局人格不兼容）。外壳拥有「`flex h-full` 双列骨架 + 右列 wrapper（独立滚动）+ 右列 header（title + headerActions 槽）+ 状态切换（error/loading/empty/children，**只换右列内容区，左侧 sidebar 常驻**）」；不知道过滤、抽屉、分页——这些由调用方组装。`LibraryView` 继续持有 `filtersOpen`/`selectedId` state 与两个 `<Sheet>`（移动端筛选 + 详情 Drawer，portal，正交于外壳），仅把布局 scaffold 搬进外壳的 `sidebar`/`headerActions`/`loadingSkeleton`/`emptyState`/`children` 槽。`CrudResourcePage` 及其 4 个单列消费者完全不动；资产网格不套 `DataView`。

**Tech Stack:** React 19 + TypeScript；Tailwind v4（CSS 变量 token）；TanStack Router（typed search params）+ React Query（infinite/detail）；Vitest + @testing-library/react + @testing-library/user-event。工作目录 `web/`；测试 `npx vitest run <path>`、类型 `npx tsc --noEmit`、lint `npx eslint <path>`。

---

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `web/src/features/common/crud/CrudWorkspacePage.tsx` | Create | 全高双列框架外壳：`flex h-full` 骨架 + sidebar 常驻槽 + 右列 wrapper + 右列 header（title + headerActions）+ 状态切换（error/loading/empty/children，只换右列） |
| `web/src/features/common/crud/CrudWorkspacePage.test.tsx` | Create | 外壳单测：sidebar 跨四态恒在；header 渲染 title + headerActions；isError → errorHint 文案 + 重试（onRetry）；isLoading → loadingSkeleton 槽；isEmpty → emptyState 槽；正常态 → children |
| `web/src/features/common/crud/index.ts` | Modify | 桶导出新增 `CrudWorkspacePage` + `CrudWorkspacePageProps` |
| `web/src/features/library/LibraryPage.tsx` | Modify | `LibraryView` 用 `CrudWorkspacePage` 包装；移除自手写双列骨架/右列 header/状态切换；保留 `FilterRail`/`FilterFields`/`FilterGroup`/`FilterChip`/`AssetDetailBody`/`AssetMedia`/`Kv` 子组件 + 两个 `<Sheet>` + `filtersOpen`/`selectedId` state |
| `web/src/features/library/library.test.tsx` | Modify（仅当选择器真漂移时） | 保留全部现有断言；只在 DOM 真变时调选择器，绝不删/弱化 |
| `web/src/routes/_authed/orgs.$org.assets.tsx` | 不动 | `LibraryView` props 接口不变，路由无需改动 |
| `web/src/features/common/crud/CrudResourcePage.tsx` | 不动（YAGNI） | 不加 `errorHint`，4 个单列消费者无一需要 |

---

## Task 1: 新建 `CrudWorkspacePage` 外壳 + barrel 导出

**Files:**
- Create: `web/src/features/common/crud/CrudWorkspacePage.tsx`
- Create: `web/src/features/common/crud/CrudWorkspacePage.test.tsx`
- Modify: `web/src/features/common/crud/index.ts`

### TDD：先写失败测试

- [x] **Step 1: 写失败测试 `CrudWorkspacePage.test.tsx`**

测试风格对齐同目录 `CrudResourcePage.test.tsx`：`render` + `screen` + `userEvent`，按 testid / role / text 断言。本外壳测试的核心不变量是「sidebar 跨 loading/error/empty 三态恒在」——这正是新建外壳的理由，必须锁死。

```tsx
import { describe, it, expect, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { CrudWorkspacePage } from "./CrudWorkspacePage"

// 公共 props 工厂：默认正常态，单测按需覆盖。
function baseProps(over: Partial<React.ComponentProps<typeof CrudWorkspacePage>> = {}) {
  return {
    title: "资产库",
    sidebar: <div data-testid="rail">rail</div>,
    isLoading: false,
    isError: false,
    isEmpty: false,
    children: <div data-testid="body">grid</div>,
    ...over,
  }
}

describe("CrudWorkspacePage", () => {
  it("正常态：渲染 title + headerActions + children", () => {
    render(
      <CrudWorkspacePage {...baseProps({ headerActions: <span data-testid="actions">N</span> })} />,
    )
    expect(screen.getByText("资产库")).toBeInTheDocument()
    expect(screen.getByTestId("actions")).toBeInTheDocument()
    expect(screen.getByTestId("body")).toBeInTheDocument()
  })

  it("sidebar 跨 loading/error/empty 三态恒在（只换右列）", () => {
    const loading = render(<CrudWorkspacePage {...baseProps({ isLoading: true })} />)
    expect(screen.getByTestId("rail")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
    loading.unmount()

    const errored = render(<CrudWorkspacePage {...baseProps({ isError: true })} />)
    expect(screen.getByTestId("rail")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
    errored.unmount()

    render(<CrudWorkspacePage {...baseProps({ isEmpty: true })} />)
    expect(screen.getByTestId("rail")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("isLoading：渲染 loadingSkeleton 槽，不渲染 children", () => {
    render(
      <CrudWorkspacePage
        {...baseProps({ isLoading: true, loadingSkeleton: <div data-testid="skel" /> })}
      />,
    )
    expect(screen.getByTestId("skel")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })

  it("isError：显默认 errorHint + 重试，点击调 onRetry", async () => {
    const onRetry = vi.fn()
    render(<CrudWorkspacePage {...baseProps({ isError: true, onRetry })} />)
    expect(screen.getByText("加载失败")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "重试" }))
    expect(onRetry).toHaveBeenCalled()
  })

  it("isError + errorHint：显自定义错误文案（Library 传「资产加载失败」）", () => {
    render(<CrudWorkspacePage {...baseProps({ isError: true, errorHint: "资产加载失败" })} />)
    expect(screen.getByText("资产加载失败")).toBeInTheDocument()
  })

  it("isEmpty：渲染 emptyState 槽，不渲染 children", () => {
    render(
      <CrudWorkspacePage
        {...baseProps({ isEmpty: true, emptyState: <div data-testid="empty" /> })}
      />,
    )
    expect(screen.getByTestId("empty")).toBeInTheDocument()
    expect(screen.queryByTestId("body")).not.toBeInTheDocument()
  })
})
```

- [x] **Step 2: 运行测试，确认 FAIL（模块不存在）**

```bash
cd web && npx vitest run src/features/common/crud/CrudWorkspacePage.test.tsx
```

预期：FAIL，报错类似 `Failed to resolve import "./CrudWorkspacePage"`（文件尚未创建）。

### 实现

- [x] **Step 3: 创建 `CrudWorkspacePage.tsx`（完整实现）**

markup 逐字复刻 `LibraryView` 当前布局：`flex h-full` 双列骨架；外壳直接渲染传入的 `sidebar`（响应式类由调用方的 `FilterRail` 自带，外壳不强加）；右列 wrapper 用 `flex min-w-0 flex-1 flex-col p-6 overflow-y-auto overscroll-contain`；右列 header 用 `mb-5 flex items-center justify-between gap-3` + `<h1 className="font-heading text-[22px] font-bold text-text-1">{title}</h1>` + `{headerActions}`；状态切换顺序 loading → error → empty → children（与 `CrudResourcePage` 一致的判断顺序无所谓，因 props 互斥；这里按 Library 原顺序 loading/error/empty）。错误态用 Library 的 `py-20`（**不是** `CrudResourcePage` 的 `py-10`）+ `<p className="text-text-2">{errorHint}</p>` + ghost 重试。house style 对齐 `CrudResourcePage.tsx`：同样 import `Skeleton`（缺省加载骨架用）与 `Button`，组件上方一行中文职责注释。仅导出组件 + props interface（react-refresh 约定，`features/common/crud` 非路由目录）。

```tsx
import type { ReactNode } from "react"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"

export interface CrudWorkspacePageProps {
  title: string
  headerActions?: ReactNode
  sidebar: ReactNode
  isLoading: boolean
  loadingSkeleton?: ReactNode
  isError: boolean
  onRetry?: () => void
  errorHint?: string
  isEmpty: boolean
  emptyState?: ReactNode
  children: ReactNode
}

// 工作区外壳（全高双列）：左 sidebar 常驻 + 右列(独立滚动) header(标题+动作槽) + 状态切换。
// 与 CrudResourcePage(居中单列)不同：error/loading/empty 只换右列内容区，sidebar 跨四态常驻。
// markup 逐字搬自资产库 LibraryView，保证零视觉回归（p-6 / overscroll-contain / py-20 等不可漂移）。
export function CrudWorkspacePage({
  title,
  headerActions,
  sidebar,
  isLoading,
  loadingSkeleton,
  isError,
  onRetry,
  errorHint = "加载失败",
  isEmpty,
  emptyState,
  children,
}: CrudWorkspacePageProps) {
  return (
    <div className="flex h-full">
      {sidebar}

      <div className="flex min-w-0 flex-1 flex-col p-6 overflow-y-auto overscroll-contain">
        <header className="mb-5 flex items-center justify-between gap-3">
          <h1 className="font-heading text-[22px] font-bold text-text-1">{title}</h1>
          {headerActions != null && <div className="flex items-center gap-3">{headerActions}</div>}
        </header>

        {isLoading ? (
          loadingSkeleton != null ? (
            loadingSkeleton
          ) : (
            <div className="flex flex-col gap-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-10 rounded-lg" />
              ))}
            </div>
          )
        ) : isError ? (
          <div className="flex flex-col items-center gap-3 py-20 text-center">
            <p className="text-text-2">{errorHint}</p>
            {onRetry && (
              <Button variant="ghost" onClick={onRetry}>
                重试
              </Button>
            )}
          </div>
        ) : isEmpty ? (
          emptyState != null ? (
            emptyState
          ) : (
            <p className="py-8 text-center text-[13px] text-text-3">暂无数据。</p>
          )
        ) : (
          children
        )}
      </div>
    </div>
  )
}
```

> 注意：`headerActions` 用 `<div className="flex items-center gap-3">` 包裹，复刻 `LibraryView` 原 header 右侧那层 `<div className="flex items-center gap-3">`（内含「筛选」按钮 + 计数）。Library 会把这两个元素作为 `headerActions` 传入，包裹层由外壳提供，保证间距像素一致。

- [x] **Step 4: 运行测试，确认 PASS**

```bash
cd web && npx vitest run src/features/common/crud/CrudWorkspacePage.test.tsx
```

预期：`Test Files  1 passed`，6 个用例全绿。

- [x] **Step 5: barrel 导出（`index.ts`）**

桶文件用「named import-export + 单独 `export type`」风格（对齐 `CrudResourcePage` 那行）。在 `CrudResourcePage` 导出行下方加：

```ts
export { CrudWorkspacePage } from "./CrudWorkspacePage"
export type { CrudWorkspacePageProps } from "./CrudWorkspacePage"
```

- [x] **Step 6: tsc + eslint（仅本任务文件）**

```bash
cd web && npx tsc --noEmit
cd web && npx eslint src/features/common/crud/CrudWorkspacePage.tsx src/features/common/crud/CrudWorkspacePage.test.tsx src/features/common/crud/index.ts
```

预期：tsc 仅剩 2 个既有无关 error（`AssetGalleryModal.tsx` / `useProductionTimeline.ts`，见 Task 3）；eslint 本任务文件 0 error。

- [x] **Step 7: commit**

```bash
cd web && git add src/features/common/crud/CrudWorkspacePage.tsx src/features/common/crud/CrudWorkspacePage.test.tsx src/features/common/crud/index.ts
cd web && git commit -m "feat(crud): add CrudWorkspacePage full-height two-column shell

资产库的双列布局(常驻 rail + 独立滚动右列 + 状态只换右列)与 CrudResourcePage
单列外壳不兼容,新建专注的 sibling 外壳供 SP-E 迁移使用。markup 逐字搬自
LibraryView 以保零视觉回归;sidebar 跨四态常驻由单测锁定。"
```

---

## Task 2: `LibraryView` 重包到 `CrudWorkspacePage`

**Files:**
- Modify: `web/src/features/library/LibraryPage.tsx`
- Run: `web/src/features/library/library.test.tsx`（保全绿；仅选择器真漂移时改）

### 改造说明

把 `LibraryView` 的 `return` 中「`<div className="flex h-full">` 双列骨架 + 右列 wrapper + 右列 header + 四态切换」搬进 `CrudWorkspacePage` 的槽位；`FilterRail`、移动端筛选 `<Sheet>`、详情 `<Sheet>` 三个节点保留，但布局位置改由外壳承载（`FilterRail` → `sidebar`；右列 header 右侧两元素 →`headerActions`；12 卡骨架网格 → `loadingSkeleton`；「没有匹配的资产」两行 → `emptyState`；网格 + 加载更多 → `children`）。两个 `<Sheet>` 与 `filtersOpen`/`selectedId` 仍由 `LibraryView` 持有，作为外壳的 sibling/portal 渲染。「筛选」按钮经 `headerActions` 传入，onClick 仍 `setFiltersOpen(true)`。

`FilterRail`/`FilterFields`/`FilterGroup`/`FilterChip`/`AssetDetailBody`/`AssetMedia`/`AssetVideoAudio`/`Kv` 子组件**不动**（Task 2 只改 `LibraryView` 函数体 + 顶部 import）。

- [x] **Step 1: 顶部 import 增加外壳**

在现有 import 块加：

```tsx
import { CrudWorkspacePage } from "@/features/common/crud"
```

`Button`/`Skeleton` import 仍保留（`children` 里的「加载更多」按钮、12 卡骨架、详情 Drawer 骨架仍用它们）。

- [x] **Step 2: 替换 `LibraryView` 的 `return`（BEFORE → AFTER）**

**BEFORE（当前 `LibraryView` return，第 80–195 行，整段替换）：**

```tsx
  return (
    <div className="flex h-full">
      <FilterRail
        filter={filter}
        onToggle={toggle}
        onTagChange={(tag) => onFilterChange({ ...filter, tag: tag || undefined })}
        onProjectChange={(project) =>
          onFilterChange({ ...filter, project: project || undefined })
        }
        projects={projects}
        styles={styles}
      />

      {/* 移动端筛选入口：≥md 隐藏（左 FilterRail 代之）。 */}
      <Sheet open={filtersOpen} onOpenChange={setFiltersOpen}>
        <SheetContent
          side="left"
          className="w-72 gap-5 overflow-y-auto border-line bg-bg-surface p-4 pt-12"
        >
          <SheetTitle className="sr-only">筛选</SheetTitle>
          {filterFields}
        </SheetContent>
      </Sheet>

      <div className="flex min-w-0 flex-1 flex-col p-6 overflow-y-auto overscroll-contain">
        <header className="mb-5 flex items-center justify-between gap-3">
          <h1 className="font-heading text-[22px] font-bold text-text-1">资产库</h1>
          <div className="flex items-center gap-3">
            <button
              type="button"
              aria-label="筛选"
              onClick={() => setFiltersOpen(true)}
              className="inline-flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1.5 text-[12px] text-text-2 transition-colors hover:border-text-3 hover:text-text-1 md:hidden"
            >
              <SlidersHorizontal className="h-[14px] w-[14px]" />
              筛选
            </button>
            <span className="text-[12px] text-text-3">{assets.length} 个资产</span>
          </div>
        </header>

        {isLoading ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
            {Array.from({ length: 12 }).map((_, i) => (
              <Skeleton key={i} className="aspect-square rounded-[10px]" />
            ))}
          </div>
        ) : isError ? (
          <div className="flex flex-col items-center gap-3 py-20 text-center">
            <p className="text-text-2">资产加载失败</p>
            <Button variant="ghost" onClick={onRetry}>
              重试
            </Button>
          </div>
        ) : assets.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-20 text-center">
            <p className="text-text-1">没有匹配的资产</p>
            <p className="text-[12.5px] text-text-3">调整筛选条件试试</p>
          </div>
        ) : (
          <>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
              {assets.map((asset) => (
                <div key={asset.id} className="relative">
                  <AssetCard
                    assetId={asset.id}
                    alt={asset.prompt}
                    type={asset.type}
                    caption={`v${asset.version}`}
                    selected={asset.id === selectedId}
                    onSelect={() => onSelect(asset.id)}
                    className="w-full"
                  />
                  <Badge
                    variant={assetStatusVariant(asset.status)}
                    className="pointer-events-none absolute left-1.5 top-1.5 max-w-[calc(100%-12px)] truncate"
                  >
                    {assetStatusLabel(asset.status)}
                  </Badge>
                </div>
              ))}
            </div>
            {hasNextPage && (
              <div className="mt-5 flex justify-center">
                <Button
                  variant="ghost"
                  onClick={onLoadMore}
                  disabled={isFetchingNextPage}
                >
                  {isFetchingNextPage ? "加载中…" : "加载更多"}
                </Button>
              </div>
            )}
          </>
        )}
      </div>

      {/* 资产详情 Drawer（?asset= 控制开合）—— 含版本血缘、播放器/缩略图。 */}
      <Sheet
        open={selectedId != null}
        onOpenChange={(open) => {
          if (!open) onSelect(null)
        }}
      >
        <SheetContent className="w-full gap-0 overflow-y-auto bg-bg-surface p-0 sm:max-w-[520px]">
          {detailLoading || detail == null ? (
            <div className="p-6">
              <Skeleton className="aspect-square w-full rounded-[10px]" />
            </div>
          ) : (
            <AssetDetailBody detail={detail} />
          )}
        </SheetContent>
      </Sheet>
    </div>
  )
```

**AFTER（替换为，完整）：**

```tsx
  // 左过滤栏：外壳直接渲染此节点，桌面 hidden…md:flex 响应式类由 FilterRail 自带。
  const sidebar = (
    <FilterRail
      filter={filter}
      onToggle={toggle}
      onTagChange={(tag) => onFilterChange({ ...filter, tag: tag || undefined })}
      onProjectChange={(project) =>
        onFilterChange({ ...filter, project: project || undefined })
      }
      projects={projects}
      styles={styles}
    />
  )

  // 右列 header 右侧：移动端「筛选」按钮(≥md 隐藏) + 资产计数。onClick 仍调 LibraryView 持有的 setFiltersOpen。
  const headerActions = (
    <>
      <button
        type="button"
        aria-label="筛选"
        onClick={() => setFiltersOpen(true)}
        className="inline-flex items-center gap-1.5 rounded-md border border-line px-2.5 py-1.5 text-[12px] text-text-2 transition-colors hover:border-text-3 hover:text-text-1 md:hidden"
      >
        <SlidersHorizontal className="h-[14px] w-[14px]" />
        筛选
      </button>
      <span className="text-[12px] text-text-3">{assets.length} 个资产</span>
    </>
  )

  // 12 卡骨架网格（加载态）。
  const loadingSkeleton = (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
      {Array.from({ length: 12 }).map((_, i) => (
        <Skeleton key={i} className="aspect-square rounded-[10px]" />
      ))}
    </div>
  )

  // 空态：两行文案。
  const emptyState = (
    <div className="flex flex-col items-center gap-3 py-20 text-center">
      <p className="text-text-1">没有匹配的资产</p>
      <p className="text-[12.5px] text-text-3">调整筛选条件试试</p>
    </div>
  )

  return (
    <>
      <CrudWorkspacePage
        title="资产库"
        sidebar={sidebar}
        headerActions={headerActions}
        isLoading={isLoading}
        loadingSkeleton={loadingSkeleton}
        isError={isError}
        onRetry={onRetry}
        errorHint="资产加载失败"
        isEmpty={assets.length === 0}
        emptyState={emptyState}
      >
        <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
          {assets.map((asset) => (
            <div key={asset.id} className="relative">
              <AssetCard
                assetId={asset.id}
                alt={asset.prompt}
                type={asset.type}
                caption={`v${asset.version}`}
                selected={asset.id === selectedId}
                onSelect={() => onSelect(asset.id)}
                className="w-full"
              />
              <Badge
                variant={assetStatusVariant(asset.status)}
                className="pointer-events-none absolute left-1.5 top-1.5 max-w-[calc(100%-12px)] truncate"
              >
                {assetStatusLabel(asset.status)}
              </Badge>
            </div>
          ))}
        </div>
        {hasNextPage && (
          <div className="mt-5 flex justify-center">
            <Button variant="ghost" onClick={onLoadMore} disabled={isFetchingNextPage}>
              {isFetchingNextPage ? "加载中…" : "加载更多"}
            </Button>
          </div>
        )}
      </CrudWorkspacePage>

      {/* 移动端筛选入口：≥md 隐藏（左 FilterRail 代之）。外壳的 sibling，portal 渲染。 */}
      <Sheet open={filtersOpen} onOpenChange={setFiltersOpen}>
        <SheetContent
          side="left"
          className="w-72 gap-5 overflow-y-auto border-line bg-bg-surface p-4 pt-12"
        >
          <SheetTitle className="sr-only">筛选</SheetTitle>
          {filterFields}
        </SheetContent>
      </Sheet>

      {/* 资产详情 Drawer（?asset= 控制开合）—— 含版本血缘、播放器/缩略图。 */}
      <Sheet
        open={selectedId != null}
        onOpenChange={(open) => {
          if (!open) onSelect(null)
        }}
      >
        <SheetContent className="w-full gap-0 overflow-y-auto bg-bg-surface p-0 sm:max-w-[520px]">
          {detailLoading || detail == null ? (
            <div className="p-6">
              <Skeleton className="aspect-square w-full rounded-[10px]" />
            </div>
          ) : (
            <AssetDetailBody detail={detail} />
          )}
        </SheetContent>
      </Sheet>
    </>
  )
```

> 关键校验点：
> - 网格 + 加载更多 的 className 与原 `children` 分支**逐字相同**（`grid-cols-[repeat(auto-fill,minmax(150px,1fr))]`、`mt-5 flex justify-center` 等）。
> - 12 卡骨架与空态 markup 逐字搬入槽，不改一个 class。
> - 外壳右列 wrapper（`p-6 overflow-y-auto overscroll-contain`）+ header（`mb-5 … gap-3` + `h1 font-heading text-[22px] font-bold text-text-1`）由 Task 1 外壳提供，与原 Library 像素一致——「资产库」标题 + 计数 + 筛选按钮位置不变。
> - `headerActions` 外壳包了一层 `<div className="flex items-center gap-3">`（见 Task 1 Step 3），与原 header 右侧那层 `div` 一致；故 `headerActions` 这里**只传两个元素本体**（按钮 + span），不再自带外层 div，避免双层嵌套。

- [x] **Step 3: 运行 `library.test.tsx`，确认全绿**

```bash
cd web && npx vitest run src/features/library/library.test.tsx
```

预期：`Test Files  1 passed`，全部用例绿。逐项核对这些断言为何仍成立（DOM 未变）：
- **status badge 变体/文案**（`assetStatus` describe）：纯函数测试，与布局无关 → 不受影响。
- **2 卡网格 + v2**（`renders the asset grid…`）：`AssetCard`（`data-slot="asset-card"`）+ `v2` caption + `<span>` Badge 仍在 `children` 内，DOM 不变 → 绿。
- **空态两行**（`renders empty state`）：`assets:[]` → `isEmpty` → `emptyState` 槽渲染「没有匹配的资产」+「调整筛选条件试试」 → 绿。
- **错误态 + 重试**（`renders error state with retry`）：`isError` → 外壳显 `errorHint="资产加载失败"` + ghost「重试」按钮调 `onRetry` → 绿。
- **加载更多仅 hasNextPage**（`shows load-more only when hasNextPage`）：「加载更多」按钮在 `children` 内、`hasNextPage` 时才渲染 → 绿。
- **filter chip toggle 载荷**（`toggles a status filter chip`）：「已采纳」chip 在 `FilterRail`/`FilterFields`（sidebar 槽，未改）→ 点击仍 `onFilterChange({status:"accepted"})` → 绿。
- **video/audio 禁用**（`disables video/audio type chips`）：类型 chip 在 `FilterFields`（未改）→ 绿。
- **版本血缘 v1→v2**（`renders version lineage…`）：详情 `<Sheet>` + `AssetDetailBody` 未改 → 绿。

> 选择器风险评估：现有断言用 role / text / `data-slot` / tagName 查询，**均不依赖被搬动节点的祖先结构**（如 `getByRole("button",{name:"重试"})`、`getByText("没有匹配的资产")`）。搬迁只改包裹层级，不改这些可查询特征 → 预期**零选择器改动**。若个别断言因层级真漂移而失败，只允许收窄/校正选择器到等价的真实 DOM 特征，**绝不删除或弱化断言**（不得把精确文案/role 改成宽松 testid 兜底）。

- [x] **Step 4: tsc + eslint（仅本任务文件）**

```bash
cd web && npx tsc --noEmit
cd web && npx eslint src/features/library/LibraryPage.tsx
```

预期：tsc 仅剩 2 个既有无关 error（见 Task 3）；eslint `LibraryPage.tsx` 0 error。

- [x] **Step 5: commit**

```bash
cd web && git add src/features/library/LibraryPage.tsx
cd web && git commit -m "refactor(library): rewrap LibraryView onto CrudWorkspacePage shell

布局骨架(双列+右列 header+四态切换)搬入 CrudWorkspacePage 槽位;FilterRail、
移动端筛选 Sheet、详情 Drawer 及 filtersOpen/selectedId state 保留在 LibraryView。
markup 逐字未改,library.test.tsx 全绿,零视觉/行为回归。"
```

---

## Task 3: 收尾 — 全量验证 + 浏览器烟雾 + 结束分支

**Files:**
- 无新增/修改（仅验证）

- [x] **Step 1: 全量 tsc**

```bash
cd web && npx tsc --noEmit
```

预期：仅剩 **2 个既有、与 SP-E 无关的 error**（超范围，不动）：`src/features/.../AssetGalleryModal.tsx` 与 `src/features/.../useProductionTimeline.ts`。除这 2 处外应为 0。若出现新 error，回到对应 Task 修复。

- [x] **Step 2: 全量 eslint**

```bash
cd web && npx eslint .
```

预期：SP-E 触及文件（`CrudWorkspacePage.tsx`/`CrudWorkspacePage.test.tsx`/`index.ts`/`LibraryPage.tsx`）0 error。仓库其余既有告警/error 保持原状（不在本次范围）。特别确认 `CrudWorkspacePage.tsx` 无 `react-refresh/only-export-components` 告警（只导出组件 + props interface）。

- [x] **Step 3: 全量 vitest**

```bash
cd web && npx vitest run
```

预期：所有测试文件通过（含新增 `CrudWorkspacePage.test.tsx` 6 例 + `library.test.tsx` 全例 + `CrudResourcePage.test.tsx` 未受影响全绿）。

- [x] **Step 4: 浏览器烟雾截图（资产库页像素比对）**

复用 playwright-core 无头 harness（系统 chrome）。先确保本地 studiod(:8083) + Vite(:5173) 已起（参见 MEMORY「Studio dev runtime」：持久化密钥 `/tmp/studio-enc-key.txt` + `/tmp/studio-jwt-secret.txt`；账号 `demo@studio.com` / `DevReveal#123`；org `169278fcd0dec7d485c741215a578fab`；dev 无 `/healthz`）。

> 注意：MEMORY 与本任务提示给出的 dev 登录口令不一致（`DevReveal#123` vs `demo12345`）。**先试 `DevReveal#123`（MEMORY 为准），失败再试 `demo12345`。** 见末尾「歧义解决」。

```bash
cat > /tmp/sp-e-smoke.mjs <<'EOF'
import { chromium } from "/home/hellotalk/code/web/sentinel-web/node_modules/playwright-core/index.js"

const ORG = "169278fcd0dec7d485c741215a578fab"
const BASE = "http://localhost:5173"
const EMAIL = "demo@studio.com"
const PASS = process.env.STUDIO_PASS || "DevReveal#123"

const browser = await chromium.launch({ executablePath: "/usr/bin/google-chrome", headless: true })
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })

await page.goto(`${BASE}/login`, { waitUntil: "networkidle" })
await page.fill('input[type="email"]', EMAIL)
await page.fill('input[type="password"]', PASS)
await page.click('button[type="submit"]')
await page.waitForLoadState("networkidle")

await page.goto(`${BASE}/orgs/${ORG}/assets`, { waitUntil: "networkidle" })
await page.waitForTimeout(1500)
await page.screenshot({ path: "/tmp/sp-e-library.png", fullPage: false })
console.log("screenshot -> /tmp/sp-e-library.png")
await browser.close()
EOF
node /tmp/sp-e-smoke.mjs
```

- [x] **Step 5: 人工比对截图**（Read `/tmp/sp-e-library.png`）核对：左 `FilterRail`（`w-56` 边框）、右列网格（150px min cards + 状态 Badge 叠加）、右列 header（「资产库」+「N 个资产」+ 移动端筛选按钮在 ≥md 隐藏）、独立滚动、详情抽屉入口、移动端筛选入口与迁移前**像素一致、零行为回归**。如有任何 class 漂移导致视觉差异，回 Task 1/2 修正。

- [x] **Step 6: 结束开发分支**

调用 superpowers:finishing-a-development-branch，按其引导给出 merge / PR / cleanup 选项（SP-A/B/C/D 均经 PR 合入 main，SP-E 同此节奏）。

---

## 风险 / 边界（贯穿全计划）

- **像素保真是核心约束：** 外壳 markup 逐字搬自当前 `LibraryView`，任何 class/间距漂移（`py-20`、`w-56`、`p-6`、`overscroll-contain`、`mb-5 … gap-3`、`text-[22px]`）都是回归 —— 以 Step 4/5 截图比对兜底。
- **错误态用 `py-20` 不是 `py-10`：** 外壳错误态复刻 Library（`py-20` + `<p className="text-text-2">{errorHint}</p>` + ghost 重试），**不照搬** `CrudResourcePage` 的 `py-10`。
- **状态切换只换右列，sidebar 常驻：** 这是新建外壳的全部理由，由 Task 1 Step 1 的「sidebar 跨四态恒在」测试锁定。
- **sidebar 响应式归属调用方：** 外壳只渲染传入的 `sidebar` 节点；桌面 `hidden … md:flex` 仍由 `FilterRail` 自带，外壳不强加 —— 保证移动端 rail 隐藏、Sheet 承载。
- **两个 `<Sheet>` 留在 LibraryView：** `filtersOpen`/`selectedId` state 与移动端筛选 Sheet + 详情 Drawer 由 `LibraryView` 持有，作为外壳 sibling/portal 渲染，正交于外壳。「筛选」按钮经 `headerActions` 传入，onClick 仍 `setFiltersOpen(true)`。
- **YAGNI：** 不给 `CrudResourcePage` 加 `errorHint`（无消费者需要）；资产网格不套 `DataView`；外壳 props 只服务 Library 实需。
- **react-refresh 约定：** `CrudWorkspacePage.tsx` 只导出组件 + props interface；`features/common/crud` 非路由目录，规则生效。
- **保留 `library.test.tsx` 全部断言：** 仅在结构真漂移时收窄/校正选择器到等价真实 DOM 特征，绝不删除/弱化。
