import { describe, it, expect } from "vitest"
import { render, screen } from "@testing-library/react"
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
function renderShell(props: { isAdmin?: boolean }) {
  const rootRoute = createRootRoute({
    component: () => (
      <AppShell org="acme" isAdmin={props.isAdmin}>
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
  ])
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ["/orgs/acme/projects"] }),
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
})
