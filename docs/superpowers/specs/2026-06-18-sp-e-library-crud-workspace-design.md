# SP-E 资产库迁移至 CRUD 工作区外壳设计

> 子项目 E，「重新审核接口服务 → 重构 web 页面」总工程收尾追加项。
> 进度：SP-A 配置/管理页 CRUD 框架（PR #59）✅ · SP-B 项目/工作流对话框去重（PR #61）✅ · SP-C API 一致性（PR #63）✅ · SP-D 运行页 UX（PR #64）✅ · **SP-E 资产库迁移（本文）**。

**目标：** 把资产库页（`features/library/LibraryPage.tsx` 的 `LibraryView`）迁入 `features/common/crud` 框架——这是 SP-B 刻意暂缓项。**零视觉/行为回归**：所有数据、过滤、keyset 加载更多、详情抽屉、移动端筛选 Sheet 均不变；只把布局骨架抽成一个可复用的框架外壳。

**关键决策（brainstorm 已定）：**
1. **完整迁移**（用户选定）——Library 走框架外壳，而非仅蹭共享原子。
2. **新建 sibling 外壳**而非重载 `CrudResourcePage`（用户选定）——Library 是全高双列（常驻 rail + 独立滚动右列 + 列内 header + 状态只换右列），与 `CrudResourcePage` 的居中单列外壳结构不兼容；强行重载会给单列原语增加第二套布局人格（仅一处消费者使用），违反 YAGNI。故新增专注的 `CrudWorkspacePage`，`CrudResourcePage` 与其 4 个单列消费者完全不动。

**非目标：**
- 不动 `CrudResourcePage`（不加 `errorHint`——4 个单列消费者无一需要自定义错误文案；按需才加）。
- 不动后端 / API / 数据 hooks（`useLibrary`/`useProjects`/`usePromptStyles`/`useAsset` 全保留）。
- 不把网格塞进 `DataView`（资产网格是自定义 `AssetCard` + 状态 Badge 叠加 + 点击开抽屉，无 per-row actions；套 `DataView` 只增间接层、无收益）。
- 不重画任何视觉——这是结构搬迁，像素须一致。

**技术栈：** React 19 + TS；Tailwind v4（CSS 变量 token）；TanStack Router（typed search params）+ React Query（infinite/detail）；vitest + @testing-library/react。

---

## 现状（已审 + 截图基线）

资产库 = 路由 `src/routes/_authed/orgs.$org.assets.tsx`（容器，组装数据 + typed search params）→ 渲染 `features/library/LibraryPage.tsx` 的 `LibraryView`（432 LOC）。当前 `LibraryView` 自有整套布局：

```
<div className="flex h-full">                              ← 全高双列行
  <FilterRail/>  (aside hidden w-56 … md:flex border-r)    ← 左常驻 rail（桌面）
  <Sheet/>       (移动端筛选 off-canvas，同 FilterFields)   ← 移动端筛选入口
  <div className="flex min-w-0 flex-1 flex-col p-6 …">      ← 右列（独立滚动）
    <header className="mb-5 flex items-center justify-between …">
      <h1>资产库</h1>
      <div> [移动端「筛选」按钮 md:hidden] + [N 个资产 计数] </div>
    </header>
    {isLoading ? 12 卡骨架网格
      : isError ? "资产加载失败" + 重试   (py-20)
      : assets.length===0 ? "没有匹配的资产" + "调整筛选条件试试" (py-20)
      : 网格(AssetCard + 状态 Badge) + 加载更多(keyset)}
  </div>
  <Sheet/>  (详情 Drawer，?asset= 控制；版本血缘/播放器)   ← 详情抽屉
</div>
```

与 `CrudResourcePage`（`mx-auto max-w-[1200px] flex-col gap-6 p-6`，header 横跨顶部，整块内容区随状态切换）结构不兼容——故新建外壳。

---

## 设计

### 1. 新外壳 `features/common/crud/CrudWorkspacePage.tsx`

把 `LibraryView` 的布局骨架抽成按槽位参数化的全高双列外壳。Props：

```ts
export interface CrudWorkspacePageProps {
  title: string                 // 右列 header 标题（"资产库"）
  headerActions?: ReactNode     // 右列 header 右侧槽（资产计数 + 移动端「筛选」按钮）
  sidebar: ReactNode            // 左侧常驻 rail（<FilterRail>）——跨 loading/error/empty 都在
  isLoading: boolean
  loadingSkeleton?: ReactNode   // 调用方给（12 卡骨架网格）；缺省给 3 行通用骨架
  isError: boolean
  onRetry?: () => void
  errorHint?: string            // 默认 "加载失败"；Library 传 "资产加载失败"
  isEmpty: boolean
  emptyState?: ReactNode        // 调用方给（"没有匹配的资产" 两行）；缺省单行 "暂无数据。"
  children: ReactNode           // 网格 + 加载更多
}
```

**外壳拥有：** `flex h-full` 双列骨架、sidebar 摆位（外壳直接渲染传入的 `sidebar`，由调用方决定 `aside` 的 `hidden … md:flex` 响应式类）、右列 wrapper（`flex min-w-0 flex-1 flex-col p-6 overflow-y-auto overscroll-contain`）+ header（title + headerActions）、状态切换（error/loading/empty/children，**只换右列内容区，rail 常驻**）。

**外壳错误/空/加载态 markup 须复刻 Library 当前外观**（error `py-20` + `<p className="text-text-2">{errorHint}</p>` + ghost 重试；loading/empty 经槽位由调用方传精确 markup）。

**职责单一：** 外壳只管「双列骨架 + 右列 header + 状态切换」，不知道过滤、抽屉、分页——这些由调用方组装。

### 2. `LibraryView` 改造（`features/library/LibraryPage.tsx`）

`LibraryView` 内部用 `CrudWorkspacePage` 包装，移除自己手写的双列骨架/右列 header/状态切换：
- `title="资产库"`
- `sidebar={<FilterRail …/>}`（`FilterRail`/`FilterFields` 子组件保留不变）
- `headerActions={移动端「筛选」按钮 + "N 个资产" 计数}`（原 header 右侧两元素原样搬入槽）
- `isLoading/isError/onRetry/isEmpty` 透传现有 props
- `errorHint="资产加载失败"`
- `loadingSkeleton={12 卡骨架网格}`、`emptyState={"没有匹配的资产" 两行}`（原 markup 原样搬入槽）
- `children`={网格(AssetCard + Badge) + 加载更多}
- **移动端筛选 Sheet + 详情 Drawer 留在 LibraryView**（portal，正交于外壳）；其开合 state（`filtersOpen`/`selectedId`）仍由 LibraryView 持有，「筛选」按钮经 `headerActions` 传入、onClick 仍 `setFiltersOpen(true)`。

`FilterRail`、`FilterFields`、`FilterGroup`、`FilterChip`、`AssetDetailBody`、`AssetMedia`、`Kv` 等子组件**不变**。

### 3. barrel 导出

`features/common/crud/index.ts` 加 `export * from "./CrudWorkspacePage"`（与现有原语同列）。

---

## 数据流 / 错误处理

- **数据流不变：** 路由 `orgs.$org.assets.tsx` 的数据装配、typed search params（`?type=&status=&style=&project=&tag=&asset=`）、`LibraryView` 的 props 接口全不变。
- **错误处理不变：** retry → `refetch`；错误文案经 `errorHint` 传入仍是「资产加载失败」+「重试」。

## 测试

- **新增 `CrudWorkspacePage.test.tsx`：** sidebar 恒渲染（跨四态）；`isError` → 显 `errorHint` 文案 + 重试（onRetry 触发）；`isLoading` → 显 loadingSkeleton 槽；`isEmpty` → 显 emptyState 槽；正常态 → 显 children；header 渲染 title + headerActions。
- **保留 `library.test.tsx` 全部断言**（status badge 变体/文案、网格 2 卡、空态两行、错误「资产加载失败」+重试、加载更多仅 hasNextPage 时显、filter chip toggle 载荷、video/audio 禁用、版本血缘 v1→v2）——DOM 不变，断言应全绿；仅在结构真变时调选择器，**绝不删/弱化断言**。
- **全量回归：** `tsc --noEmit` 干净；`eslint` SP-E 触及文件无 error（既有 2 个无关 error 在 `AssetGalleryModal.tsx`/`useProductionTimeline.ts`，超范围不动）；`vitest run` 全绿。
- **浏览器烟雾：** 资产库页截图比对——双列布局、rail、网格、状态、详情抽屉、移动端筛选入口与迁移前像素一致、行为零回归。

## 风险 / 边界

- **像素保真是核心约束：** 外壳 markup 必须逐字搬自当前 `LibraryView`（class、间距、`py-20`、`w-56`、`overscroll-contain` 等）。任何 class 漂移都是回归——以截图比对兜底。
- **sidebar 响应式归属：** 外壳只渲染传入的 `sidebar` 节点，桌面 `hidden … md:flex` 的响应式类仍由 `FilterRail`（调用方）自带，外壳不强加——保证移动端 rail 仍隐藏、Sheet 仍承载。
- **状态切换只换右列：** 外壳须确保 error/loading/empty 时 sidebar 仍在（区别于 `CrudResourcePage` 整块替换）——这正是新建外壳的理由，须在外壳测试中锁定。
- **react-refresh 约定：** `CrudWorkspacePage.tsx` 只导出组件（+ 其 props interface）；若有非组件导出须拆 `*.schema.ts`（本设计无）。`features/common/crud` 非路由目录，react-refresh 规则生效。
- **YAGNI 复核：** 不给 `CrudResourcePage` 加 `errorHint`（无消费者需要）；不套 `DataView`；新外壳 props 只服务 Library 实需，不预留投机参数。

## 任务拆分（交 writing-plans 细化）
1. 新建 `CrudWorkspacePage`（TDD：先写四态 + sidebar 常驻 + header 槽测试）+ barrel 导出。
2. 改造 `LibraryView` 用 `CrudWorkspacePage` 包装（搬骨架/header/状态入槽，留 Sheet/Drawer），保 `library.test.tsx` 全绿。
3. 收尾：tsc/eslint/vitest 全绿 + 浏览器烟雾截图比对零回归 + finishing-a-development-branch。
