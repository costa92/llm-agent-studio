import { describe, it, expect, beforeEach, vi } from "vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"

// useMyOrgs 是 useQuery，AppShell 测试无 QueryClientProvider → mock 成可配置态。
// 默认空（data: undefined）→ org 显示回退到 id，保持既有断言不变。
const myOrgsState: { value: { data: { id: string; name: string; role: string }[] | undefined } } = {
  value: { data: undefined },
}
vi.mock("./myOrgs", () => ({
  useMyOrgs: () => myOrgsState.value,
}))
import {
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
  RouterProvider,
  Outlet,
} from "@tanstack/react-router"
import { AppShell } from "./AppShell"
import { findActiveLabel } from "./nav"
import { ThemeProvider } from "./theme"

// AppShell 用 <Link>，需挂在 RouterProvider 下。建一个含 4 个 nav 目标的最小内存路由树，
// 把待测的 AppShell 渲染进根布局，断言导航项与角色门禁。
function renderShell(
  props: {
    isAdmin?: boolean
    isPlatformAdmin?: boolean
    org?: string
    initialEntry?: string
  } = {},
) {
  const rootRoute = createRootRoute({
    component: () => (
      <AppShell
        org={props.org ?? "acme"}
        isAdmin={props.isAdmin}
        isPlatformAdmin={props.isPlatformAdmin}
      >
        <Outlet />
      </AppShell>
    ),
  })
  const makeLeaf = (path: string) =>
    createRoute({ getParentRoute: () => rootRoute, path, component: () => null })
  const routeTree = rootRoute.addChildren([
    makeLeaf("/orgs/$org/projects"),
    makeLeaf("/orgs/$org/tasks"),
    makeLeaf("/orgs/$org/review"),
    makeLeaf("/orgs/$org/assets"),
    makeLeaf("/orgs/$org/cost"),
    makeLeaf("/orgs/$org/model-configs"),
    makeLeaf("/platform"),
    makeLeaf("/platform/orgs"),
    makeLeaf("/platform/users"),
    makeLeaf("/platform/health"),
  ])
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [props.initialEntry ?? "/orgs/acme/projects"] }),
  })
  return render(
    <ThemeProvider>
      <RouterProvider router={router as never} />
    </ThemeProvider>,
  )
}

// 桌面侧栏 nav（始终在 DOM）。「项目」既出现在侧栏又出现在面包屑，故按侧栏取范围去歧义。
// 抽屉打开时也会有一个同名 nav；这些用例不开抽屉，取第一个（桌面侧栏）即可。
const desktopNav = () => screen.getAllByRole("navigation", { name: "主导航" })[0]

describe("AppShell", () => {
  beforeEach(() => {
    localStorage.clear()
    myOrgsState.value = { data: undefined }
  })

  // 面包屑里的「切换组织」按钮（抽屉关闭时它是 DOM 里唯一同名按钮）。
  // 路由异步挂载 AppShell，故须 await findBy。
  const breadcrumbOrgButton = () => screen.findByRole("button", { name: "切换组织" })

  it("renders the org NAME (not the raw id) in the breadcrumb, keeping the id in the tooltip", async () => {
    const orgId = "169278fcd0dec7d485c741215a578fab"
    myOrgsState.value = { data: [{ id: orgId, name: "小红帽工作室", role: "org_admin" }] }
    renderShell({ org: orgId, initialEntry: `/orgs/${orgId}/projects` })
    const btn = await breadcrumbOrgButton()
    expect(btn).toHaveTextContent("小红帽工作室")
    expect(btn).not.toHaveTextContent(orgId)
    expect(btn).toHaveAttribute("title", orgId)
  })

  it("falls back to the org id when no name is resolved", async () => {
    const orgId = "169278fcd0dec7d485c741215a578fab"
    myOrgsState.value = { data: [] }
    renderShell({ org: orgId, initialEntry: `/orgs/${orgId}/projects` })
    expect(await breadcrumbOrgButton()).toHaveTextContent(orgId)
  })

  it("renders all nav entries for admin", async () => {
    renderShell({ isAdmin: true })
    await screen.findByText("任务中心")
    expect(within(desktopNav()).getByText("项目")).toBeInTheDocument()
    expect(screen.getByText("任务中心")).toBeInTheDocument()
    expect(screen.getByText("审核")).toBeInTheDocument()
    expect(screen.getByText("资产")).toBeInTheDocument()
    expect(screen.getByText("成本")).toBeInTheDocument()
    expect(screen.getByText("模型")).toBeInTheDocument()
  })

  it("hides admin-only entries (审核/成本) for non-admin", async () => {
    renderShell({ isAdmin: false })
    expect(await screen.findByText("资产")).toBeInTheDocument()
    expect(within(desktopNav()).getByText("项目")).toBeInTheDocument()
    // 任务中心非 admin-only —— viewer 也应可见。
    expect(screen.getByText("任务中心")).toBeInTheDocument()
    expect(screen.queryByText("审核")).not.toBeInTheDocument()
    expect(screen.queryByText("成本")).not.toBeInTheDocument()
    expect(screen.queryByText("模型")).not.toBeInTheDocument()
  })

  it("does not render org scoped nav links when org is empty", async () => {
    renderShell({ org: "", initialEntry: "/" })

    expect(await screen.findByLabelText("选择组织")).toBeInTheDocument()
    expect(screen.queryByText("项目")).not.toBeInTheDocument()
    expect(screen.queryByText("资产")).not.toBeInTheDocument()
  })

  // T8a：移动汉堡 + 抽屉。jsdom 不跑媒体查询，故无法验断点显隐，但能验：
  // 桌面轨道始终在 DOM、汉堡控件存在、点开抽屉后里面有同一套 nav 项（且尊重 isAdmin）。
  it("renders a hamburger control alongside the desktop rail nav", async () => {
    renderShell({ isAdmin: true })
    await screen.findByText("工作区")
    expect(within(desktopNav()).getByText("项目")).toBeInTheDocument() // 桌面轨道
    expect(screen.getByLabelText("打开导航菜单")).toBeInTheDocument() // 汉堡
  })

  it("opens a drawer exposing nav items respecting isAdmin (admin sees 审核/成本)", async () => {
    const user = userEvent.setup()
    renderShell({ isAdmin: true })
    await user.click(await screen.findByLabelText("打开导航菜单"))
    // 抽屉以 dialog 形式挂载；其内仍是 aria-label=主导航 的 nav。
    const drawer = await screen.findByRole("dialog")
    expect(within(drawer).getByText("项目")).toBeInTheDocument()
    expect(within(drawer).getByText("审核")).toBeInTheDocument()
    expect(within(drawer).getByText("成本")).toBeInTheDocument()
    expect(within(drawer).getByText("模型")).toBeInTheDocument()
  })

  it("drawer hides admin-only nav items for non-admin", async () => {
    const user = userEvent.setup()
    renderShell({ isAdmin: false })
    await user.click(await screen.findByLabelText("打开导航菜单"))
    const drawer = await screen.findByRole("dialog")
    expect(within(drawer).getByText("项目")).toBeInTheDocument()
    expect(within(drawer).getByText("资产")).toBeInTheDocument()
    expect(within(drawer).queryByText("审核")).not.toBeInTheDocument()
    expect(within(drawer).queryByText("成本")).not.toBeInTheDocument()
  })

  // 平台入口（非 org-scoped）：仅平台超级管理员可见，与 org admin 解耦。
  it("shows the 平台 nav item when isPlatformAdmin", async () => {
    renderShell({ isPlatformAdmin: true })
    // 桌面轨道 + 移动抽屉触发处都渲染 → 至少出现一次。
    expect(await screen.findAllByText("平台")).not.toHaveLength(0)
  })

  // 全部组织入口（非 org-scoped）：与「平台」并列，仅平台超级管理员可见。
  it("shows the 全部组织 nav item when isPlatformAdmin", async () => {
    renderShell({ isPlatformAdmin: true })
    expect(await screen.findAllByText("全部组织")).not.toHaveLength(0)
  })

  // 监控入口（非 org-scoped）：平台监控 / 数据健康页，仅平台超级管理员可见。
  it("shows the 监控 nav item when isPlatformAdmin", async () => {
    renderShell({ isPlatformAdmin: true })
    expect(await screen.findAllByText("监控")).not.toHaveLength(0)
  })

  it("hides the 平台 / 全部组织 nav items when not a platform admin (even if org admin)", async () => {
    renderShell({ isAdmin: true, isPlatformAdmin: false })
    await screen.findByText("任务中心")
    expect(within(desktopNav()).getByText("项目")).toBeInTheDocument()
    expect(screen.queryByText("平台")).not.toBeInTheDocument()
    expect(screen.queryByText("全部组织")).not.toBeInTheDocument()
  })

  it("shows the 平台 nav item for a platform admin even with no current org", async () => {
    renderShell({ org: "", initialEntry: "/platform", isPlatformAdmin: true })
    expect(await screen.findByLabelText("选择组织")).toBeInTheDocument()
    expect(screen.getAllByText("平台").length).toBeGreaterThan(0)
  })

  // ── 宽分组侧栏改造 ──

  it("renders all 3 section titles when expanded (admin + platformAdmin)", async () => {
    renderShell({ isAdmin: true, isPlatformAdmin: true })
    expect(await screen.findByText("工作区")).toBeInTheDocument()
    expect(screen.getByText("配置")).toBeInTheDocument()
    expect(screen.getByText("平台管理")).toBeInTheDocument()
  })

  it("hides the 配置 section for non-admin (all its items are admin-only)", async () => {
    renderShell({ isAdmin: false })
    expect(await screen.findByText("工作区")).toBeInTheDocument()
    // 配置 全段 adminOnly → 非 admin 下可见项为空 → 整段隐藏。
    expect(screen.queryByText("配置")).not.toBeInTheDocument()
  })

  it("collapse toggle hides labels + section titles but keeps the link present", async () => {
    const user = userEvent.setup()
    renderShell({ isAdmin: true })
    await screen.findByText("工作区")
    expect(within(desktopNav()).getByText("项目")).toBeInTheDocument()

    // 收起 → 标题与侧栏文案消失，但链接仍在（折叠态用 title 标识）。面包屑「项目」始终在顶栏。
    await user.click(screen.getByLabelText("收起导航"))
    expect(screen.queryByText("工作区")).not.toBeInTheDocument()
    expect(within(desktopNav()).queryByText("项目")).not.toBeInTheDocument()
    expect(screen.getByTitle("项目")).toBeInTheDocument()

    // 展开 → 恢复。
    await user.click(screen.getByLabelText("展开导航"))
    expect(screen.getByText("工作区")).toBeInTheDocument()
    expect(within(desktopNav()).getByText("项目")).toBeInTheDocument()
  })

  it("persists collapse state: collapsing writes localStorage; fresh render starts collapsed", async () => {
    const user = userEvent.setup()
    const { unmount } = renderShell({ isAdmin: true })
    await user.click(await screen.findByLabelText("收起导航"))
    expect(localStorage.getItem("studio-nav-collapsed")).toBe("1")

    // 重新挂载 → 读取持久化值，初始即为折叠态。
    unmount()
    renderShell({ isAdmin: true })
    expect(await screen.findByLabelText("展开导航")).toBeInTheDocument()
    expect(within(desktopNav()).queryByText("项目")).not.toBeInTheDocument()
    expect(screen.getByTitle("项目")).toBeInTheDocument()
    localStorage.clear()
  })

  // ── 桌面顶栏（面包屑 + 控件上移）──

  it("renders the desktop header breadcrumb: org segment + active page label", async () => {
    renderShell({ isAdmin: true })
    const crumb = await screen.findByRole("navigation", { name: "面包屑" })
    // org 段是「切换组织」按钮，显示当前 org。
    const orgBtn = within(crumb).getByRole("button", { name: "切换组织" })
    expect(orgBtn).toHaveTextContent("acme")
    // 当前页（默认路由 /orgs/acme/projects）→「项目」。
    expect(within(crumb).getByText("项目")).toBeInTheDocument()
  })

  it("renders ThemeSwitcher + avatar in the header, not in the sidebar nav", async () => {
    renderShell({ isAdmin: true })
    const header = (await screen.findByRole("navigation", { name: "面包屑" }))
      .closest("header") as HTMLElement
    expect(header).not.toBeNull()
    // 主题切换器在顶栏。
    expect(within(header).getByRole("button", { name: "切换主题" })).toBeInTheDocument()
    // 侧栏 nav 不再含主题切换器。
    expect(within(desktopNav()).queryByRole("button", { name: "切换主题" })).not.toBeInTheDocument()
  })

  it("findActiveLabel: org-scoped, longest-match, and unknown", () => {
    expect(findActiveLabel("/orgs/o1/projects", "o1")).toBe("项目")
    // /platform/orgs 比 /platform 长 → 取「全部组织」而非「平台」。
    expect(findActiveLabel("/platform/orgs", "o1")).toBe("全部组织")
    expect(findActiveLabel("/platform", "o1")).toBe("平台")
    expect(findActiveLabel("/nope/nowhere", "o1")).toBeNull()
  })
})
