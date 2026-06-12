import { describe, it, expect } from "vitest"
import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
  RouterProvider,
  Outlet,
} from "@tanstack/react-router"
import { AppShell } from "./AppShell"

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
    makeLeaf("/orgs/$org/review"),
    makeLeaf("/orgs/$org/assets"),
    makeLeaf("/orgs/$org/cost"),
    makeLeaf("/orgs/$org/model-configs"),
    makeLeaf("/platform"),
    makeLeaf("/platform/orgs"),
  ])
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [props.initialEntry ?? "/orgs/acme/projects"] }),
  })
  return render(<RouterProvider router={router as never} />)
}

describe("AppShell", () => {
  it("renders all nav entries for admin", async () => {
    renderShell({ isAdmin: true })
    expect(await screen.findByText("项目")).toBeInTheDocument()
    expect(screen.getByText("审核")).toBeInTheDocument()
    expect(screen.getByText("资产")).toBeInTheDocument()
    expect(screen.getByText("成本")).toBeInTheDocument()
    expect(screen.getByText("模型")).toBeInTheDocument()
  })

  it("hides admin-only entries (审核/成本) for non-admin", async () => {
    renderShell({ isAdmin: false })
    expect(await screen.findByText("项目")).toBeInTheDocument()
    expect(screen.getByText("资产")).toBeInTheDocument()
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
    expect(await screen.findByText("项目")).toBeInTheDocument() // 桌面轨道
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

  it("hides the 平台 / 全部组织 nav items when not a platform admin (even if org admin)", async () => {
    renderShell({ isAdmin: true, isPlatformAdmin: false })
    expect(await screen.findByText("项目")).toBeInTheDocument()
    expect(screen.queryByText("平台")).not.toBeInTheDocument()
    expect(screen.queryByText("全部组织")).not.toBeInTheDocument()
  })

  it("shows the 平台 nav item for a platform admin even with no current org", async () => {
    renderShell({ org: "", initialEntry: "/platform", isPlatformAdmin: true })
    expect(await screen.findByLabelText("选择组织")).toBeInTheDocument()
    expect(screen.getAllByText("平台").length).toBeGreaterThan(0)
  })
})
