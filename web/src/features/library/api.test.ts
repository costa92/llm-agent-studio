import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import { useLibrary } from "./api"
import { flattenPages } from "./keyset"
import { setAccessToken } from "@/lib/apiClient"
import type { Asset } from "@/lib/types"

afterEach(() => {
  vi.restoreAllMocks()
  setAccessToken(null)
})

function wrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}

function asset(id: string): Asset {
  return { id, type: "image", status: "accepted", version: 1 } as Asset
}

describe("useLibrary", () => {
  it("sends backend-supported filters + limit and returns the first page", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ items: [asset("a1")], next_cursor: "" }))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(
      () => useLibrary("acme", { type: "image", status: "accepted" }),
      { wrapper: wrapper() },
    )
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const url = String(fetchMock.mock.calls[0][0])
    expect(url).toContain("/api/orgs/acme/assets")
    expect(url).toContain("type=image")
    expect(url).toContain("status=accepted")
    expect(url).toContain("limit=24")
    // 首页无 cursor。
    expect(url).not.toContain("cursor=")
  })

  it("accumulates across pages via next_cursor and stops on empty cursor", async () => {
    const fetchMock = vi
      .fn()
      // 第一页：满页，next_cursor=a2。
      .mockResolvedValueOnce(
        jsonResponse({ items: [asset("a1"), asset("a2")], next_cursor: "a2" }),
      )
      // 第二页：next_cursor 空 → 到底。
      .mockResolvedValueOnce(
        jsonResponse({ items: [asset("a3")], next_cursor: "" }),
      )
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useLibrary("acme", {}), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.hasNextPage).toBe(true)

    await result.current.fetchNextPage()
    await waitFor(() => expect(result.current.isFetchingNextPage).toBe(false))

    // 串接成 3 条，且第二页请求带上 cursor=a2。
    expect(flattenPages(result.current.data?.pages).map((a) => a.id)).toEqual([
      "a1",
      "a2",
      "a3",
    ])
    expect(result.current.hasNextPage).toBe(false)
    expect(String(fetchMock.mock.calls[1][0])).toContain("cursor=a2")
  })

  it("uses a distinct query key per filter (filter change resets accumulation)", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ items: [asset("a1")], next_cursor: "" }))
    vi.stubGlobal("fetch", fetchMock)

    // 不同过滤态 = 不同 queryKey → 各自独立缓存（不与旧页串接）。
    const { result, rerender } = renderHook(
      ({ status }: { status?: string }) => useLibrary("acme", { status }),
      { wrapper: wrapper(), initialProps: { status: "accepted" } },
    )
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    rerender({ status: "rejected" })
    // 过滤变更 = 不同 queryKey → 新 key 重新发首页请求（cursor 从空开始，
    // 不沿用 accepted 的累积游标）。验证最后一次请求带新过滤且无 cursor。
    await waitFor(() => {
      const last = String(fetchMock.mock.calls.at(-1)?.[0])
      expect(last).toContain("status=rejected")
    })
    const last = String(fetchMock.mock.calls.at(-1)?.[0])
    expect(last).not.toContain("cursor=")
  })
})
