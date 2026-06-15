import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import {
  useCoverOptions,
  useGenerateCover,
  useSetCover,
  useUploadCover,
} from "./coverApi"
import { setAccessToken } from "@/lib/apiClient"
import type { Asset } from "@/lib/types"

afterEach(() => {
  vi.restoreAllMocks()
  setAccessToken(null)
})

// 每个用例新建一个 QueryClient，并 spy invalidateQueries 以断言失效。
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const invalidateSpy = vi.spyOn(client, "invalidateQueries")
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client }, children)
  return { wrapper, invalidateSpy }
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}

const ASSET: Asset = {
  id: "a1",
  projectId: "p1",
  shotId: "",
  todoId: "",
  type: "image",
  blobKey: "",
  url: "",
  prompt: "",
  style: "",
  provider: "",
  model: "",
  status: "approved",
  version: 1,
  parentAssetId: "",
  tags: [],
  prescreenScore: 0,
  prescreenFlags: [],
  prescreenNote: "",
  externalJobId: "",
}

describe("useGenerateCover", () => {
  it("POSTs the generate endpoint and invalidates the projects list", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ coverAssetId: "c1" }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useGenerateCover("o1"), { wrapper })
    result.current.mutate({ projectId: "p1", prompt: "海边日落" })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/cover/generate",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST")
    expect(result.current.data?.coverAssetId).toBe("c1")
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["projects", "o1"] })
  })
})

describe("useUploadCover", () => {
  it("POSTs FormData (no JSON Content-Type) and invalidates the projects list", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ coverAssetId: "c2" }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useUploadCover("o1"), { wrapper })
    const file = new File(["x"], "cover.png", { type: "image/png" })
    result.current.mutate({ projectId: "p1", file })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/cover/upload",
    )
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.method).toBe("POST")
    // body 必须是 FormData，且绝不能手动设 application/json（否则浏览器无法附 multipart boundary）。
    expect(init.body).toBeInstanceOf(FormData)
    expect((init.body as FormData).get("file")).toBe(file)
    const headers = new Headers(init.headers)
    expect(headers.get("Content-Type")).not.toBe("application/json")
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["projects", "o1"] })
  })
})

describe("useSetCover", () => {
  it("PUTs the cover endpoint with {assetId} and invalidates the projects list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 200 }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useSetCover("o1"), { wrapper })
    result.current.mutate({ projectId: "p1", assetId: "a1" })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe("/api/projects/p1/cover")
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.method).toBe("PUT")
    expect(init.body).toBe(JSON.stringify({ assetId: "a1" }))
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["projects", "o1"] })
  })
})

describe("useCoverOptions", () => {
  it("GETs the options endpoint and returns env.items", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ items: [ASSET] }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper } = setup()
    const { result } = renderHook(() => useCoverOptions("p1", true), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(result.current.data).toEqual([ASSET])
    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/cover/options",
    )
  })

  it("is disabled when enabled is false", () => {
    const fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    const { wrapper } = setup()
    renderHook(() => useCoverOptions("p1", false), { wrapper })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
