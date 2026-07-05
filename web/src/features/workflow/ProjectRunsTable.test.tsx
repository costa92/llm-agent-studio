import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"
import type { Plan } from "@/features/workflow/api"
import { ProjectRunsTable } from "./ProjectRunsTable"

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

// 隔离挂载 ProjectRunsTable：根路由渲染表格，另注册 /workflow 桩路由让行点击导航可解析。
// 这样组件的导航目标是轻量桩（无重型 fetch），断言 pathname/search 干净。
function renderTable(plans: Plan[]) {
  const rootRoute = createRootRoute()
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => <ProjectRunsTable projectId="p1" org="acme" />,
  })
  const workflowRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/orgs/$org/projects/$id/workflow",
    component: () => <div>workflow canvas stub</div>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, workflowRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  installFetchRoutes({
    "/api/projects/p1/plans": () => jsonResponse({ items: plans }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

// 遗留 run（无 workflowId）+ 自定义工作流 run（workflowId 非空）各一条。
function fixtures(): Plan[] {
  return [
    {
      id: "plan-default",
      projectId: "p1",
      status: "completed",
      valid: true,
      fallbackUsed: false,
      createdAt: "2026-06-30T10:00:00Z",
    },
    {
      id: "plan-wf",
      projectId: "p1",
      status: "running",
      valid: true,
      fallbackUsed: true,
      createdAt: "2026-06-30T11:00:00Z",
      workflowId: "w1",
    },
  ]
}

describe("ProjectRunsTable", () => {
  it("renders a row per plan with status label and fallback badge", async () => {
    renderTable(fixtures())

    // 状态标签（completed→已完成，running→生产中）。
    expect(await screen.findByText("已完成")).toBeInTheDocument()
    expect(screen.getByText("生产中")).toBeInTheDocument()
    // 回落 run 显示「已回落」徽标。
    expect(screen.getByText("已回落")).toBeInTheDocument()
    // 仅带 workflowId 的行有操作按钮。
    expect(screen.getAllByText("进入工作台 →")).toHaveLength(1)
  })

  it("renders both a custom-workflow plan and a legacy plan as rows", async () => {
    renderTable(fixtures())

    expect(await screen.findByText("plan-default")).toBeInTheDocument()
    expect(screen.getByText("plan-wf")).toBeInTheDocument()
  })

  it("renders the empty state when there are no plans", async () => {
    renderTable([])

    expect(await screen.findByText("暂无生成记录")).toBeInTheDocument()
  })

  it("navigates straight to the workflow canvas run mode when a row is clicked", async () => {
    const user = userEvent.setup()
    const router = renderTable(fixtures())

    // 唯一按钮属于 plan-wf（workflowId=w1），点击直达画布运行模式。
    await user.click(await screen.findByText("进入工作台 →"))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe(
        "/orgs/acme/projects/p1/workflow",
      )
      expect(router.state.location.search).toEqual({ wf: "w1", run: "plan-wf" })
    })
  })

  it("renders no navigation for legacy rows without a workflowId", async () => {
    const router = renderTable(fixtures())

    await screen.findByText("plan-default")
    // 遗留行无按钮，只有占位「—」；路由停留原地。
    const legacyRow = screen.getByText("plan-default").closest("tr")!
    expect(legacyRow).toHaveTextContent("—")
    expect(legacyRow.querySelector("button")).toBeNull()
    expect(router.state.location.pathname).toBe("/")
  })
})
