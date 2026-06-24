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

// Phase 1：只读工作流画布路由——验证选中工作流的 DAG 渲染（节点 + 至少一条边）。
describe("workflow canvas route", () => {
  it("renders the selected workflow's nodes and an edge", async () => {
    setAccessToken("tok")
    const workflow = {
      id: "w1",
      projectId: "p1",
      name: "国风短片管线",
      nodes: [
        { id: "script-1", type: "script", promptId: "", dependsOn: [] },
        {
          id: "storyboard-1",
          type: "storyboard",
          promptId: "",
          dependsOn: ["script-1"],
        },
      ],
      createdAt: "2026-06-22T00:00:00Z",
      updatedAt: "2026-06-22T00:00:00Z",
    }
    installFetchRoutes({
      "/model-configs": () => jsonResponse({ items: [] }),
      "/api/projects/p1/workflows": () => jsonResponse({ items: [workflow] }),
      // The legacy builtin endpoint (#107) returns an {items} shape — keep it
      // matching FIRST (first include() wins) so the org node-types route below
      // doesn't hijack /api/node-types/builtin.
      "/node-types/builtin": () => jsonResponse({ items: [] }),
      // P1: WorkflowCanvas resolves node descriptions via useNodeTypes →
      // GET /api/orgs/{org}/node-types, whose envelope is {version, nodeTypes}
      // (not {items}). Must precede the "/api/" catch-all.
      "/node-types": () => jsonResponse({ version: 1, nodeTypes: [] }),
      "/api/": () => jsonResponse({ items: [] }),
    })

    renderRoute("/orgs/acme/projects/p1/workflow?wf=w1")

    // 工作流名渲染（顶栏）。
    expect(await screen.findByText("国风短片管线")).toBeInTheDocument()
    // 节点 id label 渲染。
    expect(await screen.findByText("script-1")).toBeInTheDocument()
    expect(await screen.findByText("storyboard-1")).toBeInTheDocument()
    // 边层挂载：jsdom 无布局，单条 .react-flow__edge 路径不绘制，但 edges SVG
    // 容器会渲染——它的存在即证明 toReactFlow 产出的边进入了画布。
    //（边的 source/target 正确性由 canvasModel.test.ts 守护。）
    await waitFor(() => {
      expect(document.querySelector(".react-flow__edges")).not.toBeNull()
    })
    // 画布容器存在（主题作用域）。
    expect(document.querySelector(".workflow-canvas")).not.toBeNull()
  })
})
