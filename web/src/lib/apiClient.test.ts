import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  AuthError,
  apiFetch,
  clearSessionMarker,
  getAccessToken,
  setAccessToken,
  tryRestoreSession,
} from "./apiClient"
import { installFetchRoutes, jsonResponse } from "@/test/helpers"

beforeEach(() => {
  setAccessToken("access-old") // 顺带置位会话标记（模拟已登录场景）
})

afterEach(() => {
  setAccessToken(null)
  clearSessionMarker()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe("apiFetch", () => {
  it("injects the in-memory access token as Authorization: Bearer", async () => {
    const mock = installFetchRoutes({
      "/api/projects": () => jsonResponse({ ok: true }),
    })

    await apiFetch("/api/projects/p1")

    const [, init] = mock.mock.calls[0]
    const headers = new Headers(init?.headers)
    expect(headers.get("Authorization")).toBe("Bearer access-old")
  })

  it("on 401 refreshes once then retries the original request with the new token", async () => {
    let firstCall = true
    const mock = installFetchRoutes({
      "/api/auth/refresh": () =>
        jsonResponse({ access_token: "access-new", expires_in: 900 }),
      "/api/projects": () => {
        if (firstCall) {
          firstCall = false
          return jsonResponse({ error: "unauthorized" }, { status: 401 })
        }
        return jsonResponse({ ok: true })
      },
    })

    const res = await apiFetch("/api/projects/p1")
    expect(res.status).toBe(200)
    await expect(res.json()).resolves.toEqual({ ok: true })

    // refresh request carried the CSRF header + credentials include.
    const refreshCall = mock.mock.calls.find(([url]) =>
      String(url).includes("/api/auth/refresh"),
    )
    expect(refreshCall).toBeDefined()
    const refreshInit = refreshCall![1] as RequestInit
    expect(refreshInit.method).toBe("POST")
    expect(new Headers(refreshInit.headers).get("X-CSRF")).toBe("1")
    expect(refreshInit.credentials).toBe("include")

    // in-memory token rotated.
    expect(getAccessToken()).toBe("access-new")

    // the retry of /api/projects carried the new bearer.
    const projectCalls = mock.mock.calls.filter(([url]) =>
      String(url).includes("/api/projects"),
    )
    expect(projectCalls).toHaveLength(2)
    const retryHeaders = new Headers((projectCalls[1][1] as RequestInit).headers)
    expect(retryHeaders.get("Authorization")).toBe("Bearer access-new")
  })

  it("shares ONE refresh across concurrent 401s (single-flight)", async () => {
    const expired = new Set(["access-old"])
    let refreshCount = 0
    const mock = installFetchRoutes({
      "/api/auth/refresh": () => {
        refreshCount += 1
        return jsonResponse({ access_token: "access-new", expires_in: 900 })
      },
      "/api/projects": (_url, init) => {
        const token = new Headers(init?.headers)
          .get("Authorization")
          ?.replace("Bearer ", "")
        if (token && expired.has(token)) {
          return jsonResponse({ error: "unauthorized" }, { status: 401 })
        }
        return jsonResponse({ ok: true })
      },
    })

    const [a, b, c] = await Promise.all([
      apiFetch("/api/projects/p1"),
      apiFetch("/api/projects/p2"),
      apiFetch("/api/projects/p3"),
    ])

    expect(a.status).toBe(200)
    expect(b.status).toBe(200)
    expect(c.status).toBe(200)
    // ONE refresh shared across the three concurrent 401s.
    expect(refreshCount).toBe(1)
    const refreshCalls = mock.mock.calls.filter(([url]) =>
      String(url).includes("/api/auth/refresh"),
    )
    expect(refreshCalls).toHaveLength(1)
  })

  it("on refresh failure clears the token and throws AuthError", async () => {
    installFetchRoutes({
      "/api/auth/refresh": () =>
        jsonResponse({ error: "expired" }, { status: 401 }),
      "/api/projects": () =>
        jsonResponse({ error: "unauthorized" }, { status: 401 }),
    })

    await expect(apiFetch("/api/projects/p1")).rejects.toBeInstanceOf(AuthError)
    expect(getAccessToken()).toBeNull()
  })
})

describe("tryRestoreSession", () => {
  it("no-ops to true when a token already exists (no network call)", async () => {
    setAccessToken("access-old")
    const mock = installFetchRoutes({
      "/api/auth/refresh": () =>
        jsonResponse({ access_token: "access-new", expires_in: 900 }),
    })

    await expect(tryRestoreSession()).resolves.toBe(true)
    // 幂等：已有 token 直接返回，不打刷新接口。
    expect(mock).not.toHaveBeenCalled()
    expect(getAccessToken()).toBe("access-old")
  })

  it("on no token + valid refresh cookie sets the token and returns true", async () => {
    setAccessToken(null)
    installFetchRoutes({
      "/api/auth/refresh": () =>
        jsonResponse({ access_token: "restored", expires_in: 900 }),
    })

    await expect(tryRestoreSession()).resolves.toBe(true)
    expect(getAccessToken()).toBe("restored")
  })

  it("on no token + failed refresh returns false and never throws", async () => {
    setAccessToken(null)
    installFetchRoutes({
      "/api/auth/refresh": () =>
        jsonResponse({ error: "expired" }, { status: 401 }),
    })

    await expect(tryRestoreSession()).resolves.toBe(false)
    expect(getAccessToken()).toBeNull()
  })

  it("从未登录（无会话标记）→ 跳过刷新，直接返回 false 不打网络", async () => {
    setAccessToken(null)
    clearSessionMarker() // 抹掉 beforeEach 置的标记，模拟从未登录
    const mock = installFetchRoutes({
      "/api/auth/refresh": () =>
        jsonResponse({ access_token: "should-not-happen", expires_in: 900 }),
    })

    await expect(tryRestoreSession()).resolves.toBe(false)
    // 无标记时绝不触碰 /api/auth/refresh（消除未认证冷启动的 401 噪声）。
    expect(mock).not.toHaveBeenCalled()
    expect(getAccessToken()).toBeNull()
  })

  it("setAccessToken(token) 置位会话标记 → 冷启动会尝试恢复", async () => {
    clearSessionMarker()
    setAccessToken("tok") // 模拟登录成功
    setAccessToken(null) // 冷启动：内存 token 丢失，但标记仍在
    installFetchRoutes({
      "/api/auth/refresh": () =>
        jsonResponse({ access_token: "restored", expires_in: 900 }),
    })

    await expect(tryRestoreSession()).resolves.toBe(true)
    expect(getAccessToken()).toBe("restored")
  })
})
