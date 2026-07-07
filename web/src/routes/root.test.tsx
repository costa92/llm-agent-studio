import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { routeTree } from "@/routeTree.gen"
import { AuthProvider } from "@/app/auth"
import { setAccessToken, clearSessionMarker } from "@/lib/apiClient"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"

afterEach(() => {
  setAccessToken(null)
  clearSessionMarker()
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

  return render(
    <AuthProvider>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </AuthProvider>,
  )
}

describe("root not found handling", () => {
  it("renders the org landing for empty org paths", async () => {
    renderRoute("/orgs//projects")

    expect(await screen.findByText("进入组织")).toBeInTheDocument()
    expect(screen.queryByText("Not Found")).not.toBeInTheDocument()
  })
})

describe("silent session restore on cold boot", () => {
  it("keeps an authed deep link on-page when the refresh cookie is valid (token cleared)", async () => {
    // 冷启动：内存无 token，但刷新 cookie 仍有效。曾登录过 → 会话标记仍在 localStorage
    //（setAccessToken(token) 置位），故 tryRestoreSession 会尝试恢复而非直接跳过。
    setAccessToken("prev-token")
    setAccessToken(null)
    installFetchRoutes({
      // __root beforeLoad 的 tryRestoreSession 命中这条 → 恢复内存 token。
      "/api/auth/refresh": () =>
        jsonResponse({ access_token: "restored", expires_in: 900 }),
      // 进入 /_authed 后子页/角色探针发起的请求，给空信封即可。
      "/api/": () => jsonResponse({ items: [] }),
    })

    const router = createRouter({
      routeTree,
      history: createMemoryHistory({
        initialEntries: ["/orgs/org_hex_123/projects"],
      }),
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

    // 关键断言：守卫前已静默恢复 → 不应被踢到 /login，停在深链上。
    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/orgs/org_hex_123/projects"),
    )
    expect(router.state.location.pathname).not.toBe("/login")
  })

  it("redirects to /login when there is no valid refresh cookie", async () => {
    setAccessToken(null)
    installFetchRoutes({
      "/api/auth/refresh": () =>
        jsonResponse({ error: "expired" }, { status: 401 }),
      "/api/": () => jsonResponse({ items: [] }),
    })

    const router = createRouter({
      routeTree,
      history: createMemoryHistory({
        initialEntries: ["/orgs/org_hex_123/projects"],
      }),
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

    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/login"),
    )
  })
})
