import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import { useReviewQueue, useRegenerate } from "./api"
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

    expect(result.current.data).toHaveLength(1)
    const url = String(fetchMock.mock.calls[0][0])
    // status 过滤项已核实真实存在（m2handlers.go:175 → store.go:244）。
    expect(url).toContain("/api/orgs/acme/assets")
    expect(url).toContain("status=pending_acceptance")
    // Phase 3 T4：移除硬编码 type=image，让 video/audio 待审资产也进队列。
    expect(url).not.toContain("type=image")
    // 无 project 参数时不带 &project=。
    expect(url).not.toContain("project=")
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
