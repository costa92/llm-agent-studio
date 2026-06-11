import { afterEach, describe, expect, it, vi } from "vitest"
import { act, renderHook, waitFor } from "@testing-library/react"
import type { ReactNode } from "react"
import { getAccessToken, setAccessToken } from "@/lib/apiClient"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"
import { AuthProvider, useAuth } from "./auth"

afterEach(() => {
  setAccessToken(null)
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

function wrapper({ children }: { children: ReactNode }) {
  return <AuthProvider>{children}</AuthProvider>
}

describe("AuthProvider / useAuth", () => {
  it("starts unauthenticated", () => {
    const { result } = renderHook(() => useAuth(), { wrapper })
    expect(result.current.isAuthenticated).toBe(false)
  })

  it("login posts {email,password} with credentials, stores the token, flips the flag", async () => {
    const mock = installFetchRoutes({
      "/api/auth/login": () =>
        jsonResponse({ access_token: "tok-1", expires_in: 900 }),
    })

    const { result } = renderHook(() => useAuth(), { wrapper })

    await act(async () => {
      await result.current.login("a@b.com", "pw")
    })

    await waitFor(() => expect(result.current.isAuthenticated).toBe(true))
    expect(getAccessToken()).toBe("tok-1")

    const [url, init] = mock.mock.calls[0]
    expect(String(url)).toContain("/api/auth/login")
    expect((init as RequestInit).method).toBe("POST")
    expect((init as RequestInit).credentials).toBe("include")
    expect(JSON.parse((init as RequestInit).body as string)).toEqual({
      email: "a@b.com",
      password: "pw",
    })
  })

  it("login with bad credentials throws and stays unauthenticated", async () => {
    installFetchRoutes({
      "/api/auth/login": () =>
        jsonResponse({ error: "invalid" }, { status: 401 }),
    })

    const { result } = renderHook(() => useAuth(), { wrapper })

    await expect(
      act(async () => {
        await result.current.login("a@b.com", "wrong")
      }),
    ).rejects.toBeTruthy()

    expect(result.current.isAuthenticated).toBe(false)
    expect(getAccessToken()).toBeNull()
  })

  it("logout posts with X-CSRF:1 + credentials and clears the token", async () => {
    setAccessToken("tok-1")
    const mock = installFetchRoutes({
      "/api/auth/logout": () => jsonResponse(null, { status: 204 }),
    })

    const { result } = renderHook(() => useAuth(), { wrapper })
    // 已有 token —— 视为已认证。
    expect(result.current.isAuthenticated).toBe(true)

    await act(async () => {
      await result.current.logout()
    })

    await waitFor(() => expect(result.current.isAuthenticated).toBe(false))
    expect(getAccessToken()).toBeNull()

    const [url, init] = mock.mock.calls[0]
    expect(String(url)).toContain("/api/auth/logout")
    expect((init as RequestInit).method).toBe("POST")
    expect(new Headers((init as RequestInit).headers).get("X-CSRF")).toBe("1")
    expect((init as RequestInit).credentials).toBe("include")
  })

  it("logout clears the token even if the request fails", async () => {
    setAccessToken("tok-1")
    installFetchRoutes({
      "/api/auth/logout": () =>
        jsonResponse({ error: "boom" }, { status: 500 }),
    })

    const { result } = renderHook(() => useAuth(), { wrapper })

    await act(async () => {
      await result.current.logout()
    })

    await waitFor(() => expect(result.current.isAuthenticated).toBe(false))
    expect(getAccessToken()).toBeNull()
  })
})
