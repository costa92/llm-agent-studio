import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import { useReviewQueue, useRegenerate, useAccept, useReject } from "./api"
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

// 暴露 invalidateQueries spy 以断言 HITL 成功后的失效集合。
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const invalidateSpy = vi.spyOn(client, "invalidateQueries")
  const wrap = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
  return { wrap, invalidateSpy }
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}

describe("useReviewQueue", () => {
  it("requests status=pending_acceptance WITHOUT type=image (surface video/audio too)", async () => {
    const asset = { id: "as1", type: "image", status: "pending_acceptance" } as Asset
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ items: [asset], next_cursor: "" }))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useReviewQueue("acme"), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    // P1：infinite query——首页信封在 data.pages[0]。
    expect(result.current.data?.pages[0].items).toHaveLength(1)
    const url = String(fetchMock.mock.calls[0][0])
    // status 过滤项已核实真实存在（m2handlers.go:175 → store.go:244）。
    expect(url).toContain("/api/orgs/acme/assets")
    expect(url).toContain("status=pending_acceptance")
    // Phase 3 T4：移除硬编码 type=image，让 video/audio 待审资产也进队列。
    expect(url).not.toContain("type=image")
    // 无 project 参数时不带 &project=。
    expect(url).not.toContain("project=")
    // 首页无 cursor（keyset 从头拉）。
    expect(url).not.toContain("cursor=")
  })

  it("appends &project= when a project filter is given", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ items: [], next_cursor: "" }))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useReviewQueue("acme", "proj-1"), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const url = String(fetchMock.mock.calls[0][0])
    expect(url).toContain("status=pending_acceptance")
    expect(url).toContain("project=proj-1")
    expect(url).not.toContain("type=image")
  })

  it("follows next_cursor to accumulate the second page (backlog > one page)", async () => {
    const page1 = { id: "a1", type: "image", status: "pending_acceptance" } as Asset
    const page2 = { id: "a2", type: "image", status: "pending_acceptance" } as Asset
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ items: [page1], next_cursor: "a1" }))
      .mockResolvedValueOnce(jsonResponse({ items: [page2], next_cursor: "" }))
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useReviewQueue("acme"), {
      wrapper: wrapper(),
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    // 首页 next_cursor 非空 → 还有下一页。
    expect(result.current.hasNextPage).toBe(true)

    await result.current.fetchNextPage()
    await waitFor(() => expect(result.current.data?.pages).toHaveLength(2))
    // 第二页带上一页 next_cursor 作为 cursor。
    expect(String(fetchMock.mock.calls[1][0])).toContain("cursor=a1")
    // 第二页 next_cursor 空 → 到底。
    expect(result.current.hasNextPage).toBe(false)
    expect(result.current.data?.pages[1].items[0].id).toBe("a2")
  })
})

describe("useAccept / useReject (HITL 失效集合)", () => {
  // 工作台右栏内联采纳/拒绝后，「待审核 · N」徽标取自 project-state（assets.pending）。
  // accept/reject 不发 run_event，SSE 版本门不会推 state 帧，故必须主动失效
  // ["project-state"] 让徽标刷新——否则计数停在旧值直到手动刷新。
  it("useAccept invalidates review-queue + library + project-state", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ id: "as1", status: "accepted" }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrap, invalidateSpy } = setup()
    const { result } = renderHook(() => useAccept("acme"), { wrapper: wrap })
    result.current.mutate("as1")
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe("/api/assets/as1/accept")
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["review-queue", "acme"] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["library", "acme"] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["project-state"] })
  })

  it("useReject invalidates review-queue + library + project-state", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ id: "as1", status: "rejected" }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrap, invalidateSpy } = setup()
    const { result } = renderHook(() => useReject("acme"), { wrapper: wrap })
    result.current.mutate("as1")
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe("/api/assets/as1/reject")
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["review-queue", "acme"] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["library", "acme"] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["project-state"] })
  })
})

describe("useRegenerate", () => {
  it("POSTs {prompt} only (backend ignores params) and returns the new asset id", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ newAssetId: "as2", todoId: "t9", status: "generating" }),
    )
    vi.stubGlobal("fetch", fetchMock)

    const { result } = renderHook(() => useRegenerate("acme"), {
      wrapper: wrapper(),
    })
    const res = await result.current.mutateAsync({ id: "as1", prompt: "改后的 prompt" })

    expect(res.newAssetId).toBe("as2")
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toBe("/api/assets/as1/regenerate")
    expect((init as RequestInit).method).toBe("POST")
    expect(JSON.parse((init as RequestInit).body as string)).toEqual({
      prompt: "改后的 prompt",
    })
  })
})
