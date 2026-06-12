import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor, within } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { routeTree } from "@/routeTree.gen"
import { AuthProvider } from "@/app/auth"
import { setAccessToken } from "@/lib/apiClient"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"

afterEach(() => {
  setAccessToken(null)
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function renderRoute(path: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <AuthProvider>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </AuthProvider>,
  )
  return router
}

// Phase 1：工作台迁到 /orgs/$org/projects/$id —— 验证 org 命名空间内可达、
// 且 AppShell 左侧导航轨（项目/资产 …）随真实 org param 点亮。
describe("workbench under org namespace", () => {
  it("project list resolves at /orgs/$org/projects and shows the full nav rail", async () => {
    setAccessToken("tok")
    installFetchRoutes({
      // admin 探针：200 → admin（点亮全部 nav）。
      "/model-configs": () => jsonResponse({ items: [] }),
      // 项目列表（裸 items 信封）。
      "/api/orgs/acme/projects": () => jsonResponse({ items: [] }),
      "/api/": () => jsonResponse({ items: [] }),
    })

    renderRoute("/orgs/acme/projects")

    // 左侧导航轨随 org param 点亮（admin 项可见）；作用域限定到 nav 地标，避开页面标题里的同名文字。
    const nav = await screen.findByRole("navigation", { name: "主导航" })
    expect(await within(nav).findByText("项目")).toBeInTheDocument()
    expect(within(nav).getByText("资产")).toBeInTheDocument()
    expect(within(nav).getByText("审核")).toBeInTheDocument()
  })

  it("workbench resolves at /orgs/$org/projects/$id with the full nav rail (org param survives)", async () => {
    setAccessToken("tok")
    const project = {
      id: "p1",
      orgId: "acme",
      name: "国风茶饮宣传短片",
      description: "为新中式茶饮品牌做一支 30 秒宣传短片",
      contentType: "短视频",
      targetPlatform: "抖音",
      style: "国风",
      status: "completed",
      createdBy: "u1",
    }
    installFetchRoutes({
      "/model-configs": () => jsonResponse({ items: [] }),
      "/api/projects/p1/events": () => jsonResponse({ items: [] }),
      "/api/projects/p1": () => jsonResponse(project),
      "/api/": () => jsonResponse({ items: [] }),
    })

    const router = renderRoute("/orgs/acme/projects/p1")

    // 路由解析到工作台（matched route id = 工作台，而非 not-found / 落地页）。
    await waitFor(() =>
      expect(router.state.matches.map((m) => m.routeId)).toContain(
        "/_authed/orgs/$org/projects/$id",
      ),
    )
    expect(router.state.location.pathname).toBe("/orgs/acme/projects/p1")
    // 工作台正文渲染（项目名）。
    expect(await screen.findByText("国风茶饮宣传短片")).toBeInTheDocument()
    // 关键：org param 存活 → AppShell hasOrg=true → 左侧导航轨点亮（不再塌成"选择组织"）。
    const nav = screen.getByRole("navigation", { name: "主导航" })
    expect(within(nav).getByText("项目")).toBeInTheDocument()
    expect(within(nav).getByText("资产")).toBeInTheDocument()
    expect(screen.queryByLabelText("选择组织")).not.toBeInTheDocument()
  })
})
