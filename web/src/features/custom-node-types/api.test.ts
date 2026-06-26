import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import {
  useCreateCustomNodeType,
  useUpdateCustomNodeType,
  useDeleteCustomNodeType,
} from "./api"
import { setAccessToken } from "@/lib/apiClient"
import type { CustomNodeType, UpsertCustomNodeTypeInput } from "@/lib/types"

afterEach(() => {
  vi.restoreAllMocks()
  setAccessToken(null)
})

// 每个用例新建一个 QueryClient，并 spy invalidateQueries 以断言失效（与 coverApi.test 同源）。
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

const INPUT: UpsertCustomNodeTypeInput = {
  label: "翻译",
  color: "#7c93ff",
  kind: "llm",
  params: { userPrompt: "翻译 {{draft}}" },
}

const TYPE: CustomNodeType = {
  id: "ct1",
  orgId: "o1",
  slug: "translate",
  label: "翻译",
  color: "#7c93ff",
  kind: "llm",
  params: { userPrompt: "翻译 {{draft}}" },
}

// Bug 3 回归：三个写 mutation 必须同时失效 ["custom-node-types", org] 与 ["node-types", org]。
// 后者驱动 PropertiesPanel 可编辑表单 + 字段 outputSchema；只失效前者会让刚快建/改名的
// typed 节点在页面重载前只读、无表单。
describe("custom-node-types mutations invalidate BOTH catalogs", () => {
  it("useCreateCustomNodeType invalidates custom-node-types + node-types", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(TYPE)))
    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useCreateCustomNodeType("o1"), { wrapper })
    result.current.mutate(INPUT)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["custom-node-types", "o1"],
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["node-types", "o1"] })
  })

  it("useUpdateCustomNodeType invalidates custom-node-types + node-types", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse(TYPE)))
    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useUpdateCustomNodeType("o1"), { wrapper })
    result.current.mutate({ id: "ct1", input: INPUT })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["custom-node-types", "o1"],
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["node-types", "o1"] })
  })

  it("useDeleteCustomNodeType invalidates custom-node-types + node-types", async () => {
    // useDeleteCustomNodeType 走 apiJSON（res.json()）→ mock 须返回可解析 JSON 体。
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({})))
    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useDeleteCustomNodeType("o1"), { wrapper })
    result.current.mutate("ct1")
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["custom-node-types", "o1"],
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["node-types", "o1"] })
  })
})
