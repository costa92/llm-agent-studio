import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import {
  useWorkflows,
  useCreateWorkflow,
  useUpdateWorkflow,
  useDeleteWorkflow,
  useRunWorkflow,
} from "./workflowApi"
import { setAccessToken } from "@/lib/apiClient"
import type { Workflow } from "@/lib/types"

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

const WF: Workflow = {
  id: "wf1",
  projectId: "p1",
  name: "默认工作流",
  nodes: [{ id: "script-1", type: "script", promptId: "", dependsOn: [] }],
  createdAt: "2026-06-01T00:00:00Z",
  updatedAt: "2026-06-01T00:00:00Z",
}

describe("useWorkflows", () => {
  it("GETs /api/projects/{id}/workflows and returns env.items", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ items: [WF] }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper } = setup()
    const { result } = renderHook(() => useWorkflows("p1"), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(result.current.data).toEqual([WF])
    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/workflows",
    )
  })

  it("is disabled when projectId is empty", () => {
    const fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    const { wrapper } = setup()
    renderHook(() => useWorkflows(""), { wrapper })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe("useCreateWorkflow", () => {
  it("POSTs the body and invalidates the workflows list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(WF))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useCreateWorkflow("p1"), { wrapper })
    result.current.mutate({ name: "x", nodes: WF.nodes })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/workflows",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST")
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["workflows", "p1"],
    })
  })
})

describe("useUpdateWorkflow", () => {
  it("PUTs /api/projects/{id}/workflows/{wfId} and invalidates the list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(WF))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useUpdateWorkflow("p1"), { wrapper })
    result.current.mutate({ wfId: "wf1", input: { name: "y", nodes: WF.nodes } })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/workflows/wf1",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("PUT")
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["workflows", "p1"],
    })
  })
})

describe("useDeleteWorkflow", () => {
  it("DELETEs /api/projects/{id}/workflows/{wfId} and invalidates the list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useDeleteWorkflow("p1"), { wrapper })
    result.current.mutate("wf1")
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/workflows/wf1",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("DELETE")
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["workflows", "p1"],
    })
  })
})

describe("useRunWorkflow", () => {
  it("POSTs the run endpoint and invalidates workflows + project + plans", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        planId: "plan9",
        valid: true,
        fallbackUsed: false,
        workflowId: "wf1",
      }),
    )
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useRunWorkflow("p1"), { wrapper })
    result.current.mutate({ wfId: "wf1" })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/workflows/wf1/run",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST")
    // 无 inputs → 不带 body（与历史行为一致，零回归）。
    expect((fetchMock.mock.calls[0][1] as RequestInit).body).toBeUndefined()
    expect(result.current.data?.planId).toBe("plan9")
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["workflows", "p1"],
    })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["project", "p1"] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["plans", "p1"] })
  })

  it("带 inputs → POST body 携带 {inputs}", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ planId: "plan9", valid: true, fallbackUsed: false, workflowId: "wf1" }),
    )
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper } = setup()
    const { result } = renderHook(() => useRunWorkflow("p1"), { wrapper })
    result.current.mutate({ wfId: "wf1", inputs: { heroName: "阿力" } })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    const body = (fetchMock.mock.calls[0][1] as RequestInit).body
    expect(JSON.parse(String(body))).toEqual({ inputs: { heroName: "阿力" } })
  })
})
