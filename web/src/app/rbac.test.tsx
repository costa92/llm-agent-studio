import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { setAccessToken } from "@/lib/apiClient"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"
import { useRole } from "./rbac"

afterEach(() => {
  setAccessToken(null)
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

// 每个 hook 测试用全新 QueryClient（关 retry，避免 403 触发重试拖慢）。
function wrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
}

// meRoute 装一个返回指定角色的 /members/me 探针。
function meRoute(org: string, role: string) {
  return installFetchRoutes({
    [`/api/orgs/${org}/members/me`]: () => jsonResponse({ userId: "u1", role }),
  })
}

describe("useRole", () => {
  it("viewer → canWrite=false, isAdmin=false", async () => {
    setAccessToken("tok")
    meRoute("acme", "viewer")
    const { result } = renderHook(() => useRole("acme"), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.role).toBe("viewer")
    expect(result.current.canWrite).toBe(false)
    expect(result.current.isAdmin).toBe(false)
  })

  it("editor → canWrite=true, isAdmin=false", async () => {
    setAccessToken("tok")
    meRoute("acme", "editor")
    const { result } = renderHook(() => useRole("acme"), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.canWrite).toBe(true)
    expect(result.current.isAdmin).toBe(false)
  })

  it("admin → canWrite=true, isAdmin=true", async () => {
    setAccessToken("tok")
    meRoute("acme", "admin")
    const { result } = renderHook(() => useRole("acme"), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.canWrite).toBe(true)
    expect(result.current.isAdmin).toBe(true)
  })

  it("org_admin → canWrite=true, isAdmin=true", async () => {
    setAccessToken("tok")
    meRoute("acme", "org_admin")
    const { result } = renderHook(() => useRole("acme"), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.canWrite).toBe(true)
    expect(result.current.isAdmin).toBe(true)
  })

  it("treats a 403 (non-member) as the most restrictive viewer", async () => {
    setAccessToken("tok")
    installFetchRoutes({
      "/api/orgs/acme/members/me": () =>
        jsonResponse({ error: "forbidden" }, { status: 403 }),
    })
    const { result } = renderHook(() => useRole("acme"), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.role).toBe("")
    expect(result.current.canWrite).toBe(false)
    expect(result.current.isAdmin).toBe(false)
  })

  it("hits the members/me probe for the given org", async () => {
    setAccessToken("tok")
    const mock = meRoute("beta", "viewer")
    const { result } = renderHook(() => useRole("beta"), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    const probed = mock.mock.calls.some(([url]) =>
      String(url).includes("/api/orgs/beta/members/me"),
    )
    expect(probed).toBe(true)
  })

  it("does not probe when org is empty", async () => {
    setAccessToken("tok")
    const mock = installFetchRoutes({
      "/api/orgs": () => jsonResponse({ items: [] }),
    })
    const { result } = renderHook(() => useRole(""), { wrapper: wrapper() })
    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.canWrite).toBe(false)
    expect(result.current.isAdmin).toBe(false)
    expect(mock).not.toHaveBeenCalled()
  })
})
