import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import {
  useWorkflowTemplates,
  useInstantiateTemplate,
} from "./workflowTemplateApi"
import { setAccessToken } from "@/lib/apiClient"
import type { Workflow, WorkflowTemplateMeta } from "@/lib/types"

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

const TEMPLATES: WorkflowTemplateMeta[] = [
  { id: "standard", name: "通用管线", description: "脚本→分镜", group: "通用" },
  { id: "music", name: "音乐创作", description: "歌词→编曲", group: "创作" },
]

const WF: Workflow = {
  id: "wf-new",
  projectId: "p1",
  name: "音乐创作",
  nodes: [{ id: "llm-1", type: "custom:llm", promptId: "", dependsOn: [] }],
  version: 1,
  createdAt: "2026-06-01T00:00:00Z",
  updatedAt: "2026-06-01T00:00:00Z",
}

describe("useWorkflowTemplates", () => {
  it("GETs /api/orgs/{org}/workflow-templates and returns env.items", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ items: TEMPLATES }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper } = setup()
    const { result } = renderHook(() => useWorkflowTemplates("org1"), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(result.current.data).toEqual(TEMPLATES)
    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/orgs/org1/workflow-templates",
    )
  })

  it("is disabled when org is empty", () => {
    const fetchMock = vi.fn()
    vi.stubGlobal("fetch", fetchMock)
    const { wrapper } = setup()
    renderHook(() => useWorkflowTemplates(""), { wrapper })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe("useInstantiateTemplate", () => {
  it("POSTs {templateId} to from-template and invalidates the workflows list", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(WF))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useInstantiateTemplate("p1"), { wrapper })
    result.current.mutate({ templateId: "music" })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/projects/p1/workflows/from-template",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST")
    expect(
      JSON.parse(String((fetchMock.mock.calls[0][1] as RequestInit).body)),
    ).toEqual({ templateId: "music" })
    expect(result.current.data).toEqual(WF)
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["workflows", "p1"],
    })
  })
})
