import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { routeTree } from "@/routeTree.gen"
import { AuthProvider } from "@/app/auth"
import { ThemeProvider } from "@/app/theme"
import { setAccessToken } from "@/lib/apiClient"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"

afterEach(() => {
  setAccessToken(null)
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const PLAN = {
  id: "run-1",
  projectId: "p1",
  status: "completed",
  valid: true,
  fallbackUsed: false,
  createdAt: "2026-06-30T10:00:00Z",
}

function mockRoutes() {
  installFetchRoutes({
    // 具体 plans 路由须先于 /api/ 兜底命中。
    "/api/projects/p1/plans": () => jsonResponse({ items: [PLAN] }),
    "/members/me": () => jsonResponse({ role: "admin" }),
    "/model-configs": () => jsonResponse({ items: [] }),
    "/node-types/builtin": () => jsonResponse({ items: [] }),
    "/node-types": () => jsonResponse({ version: 1, nodeTypes: [] }),
    "/api/": () => jsonResponse({ items: [] }),
  })
}

function renderRoute(path: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <ThemeProvider>
      <AuthProvider>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </AuthProvider>
    </ThemeProvider>,
  )
  return router
}

describe("/runs index route", () => {
  // 守护：裸 /runs 曾无条件 redirect 回项目详情；现应渲染完整运行记录列表且不回跳。
  it("renders the runs list and does NOT redirect back to the overview", async () => {
    setAccessToken("tok")
    mockRoutes()

    const router = renderRoute("/orgs/acme/projects/p1/runs")

    // 新页标题 + 列表行（只在 /runs 页出现的判别文案）。
    expect(await screen.findByText("运行记录")).toBeInTheDocument()
    expect(await screen.findByText("run-1")).toBeInTheDocument()
    // pathname 停在 /runs（若仍有 redirect 会变成 /orgs/acme/projects/p1）。
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/orgs/acme/projects/p1/runs")
    })
  })

  // 回归：概览页重构后仍渲染其运行历史表。
  it("overview still renders its runs table after the refactor", async () => {
    setAccessToken("tok")
    mockRoutes()

    renderRoute("/orgs/acme/projects/p1")

    expect(await screen.findByText("run-1")).toBeInTheDocument()
  })
})
