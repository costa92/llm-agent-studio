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

describe("useRole", () => {
  it("infers isAdmin=true when the admin-only probe returns 200", async () => {
    setAccessToken("tok")
    installFetchRoutes({
      "/api/orgs/acme/model-configs": () => jsonResponse({ items: [] }),
    })

    const { result } = renderHook(() => useRole("acme"), {
      wrapper: wrapper(),
    })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.isAdmin).toBe(true)
  })

  it("infers isAdmin=false when the admin-only probe returns 403", async () => {
    setAccessToken("tok")
    installFetchRoutes({
      "/api/orgs/acme/model-configs": () =>
        jsonResponse({ error: "forbidden" }, { status: 403 }),
    })

    const { result } = renderHook(() => useRole("acme"), {
      wrapper: wrapper(),
    })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    expect(result.current.isAdmin).toBe(false)
  })

  it("hits the model-configs probe for the given org", async () => {
    setAccessToken("tok")
    const mock = installFetchRoutes({
      "/api/orgs/beta/model-configs": () => jsonResponse({ items: [] }),
    })

    const { result } = renderHook(() => useRole("beta"), {
      wrapper: wrapper(),
    })

    await waitFor(() => expect(result.current.isLoading).toBe(false))
    const probed = mock.mock.calls.some(([url]) =>
      String(url).includes("/api/orgs/beta/model-configs"),
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
    expect(result.current.isAdmin).toBe(false)
    expect(mock).not.toHaveBeenCalled()
  })
})
