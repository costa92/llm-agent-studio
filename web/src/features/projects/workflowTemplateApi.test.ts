import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import {
  useWorkflowTemplates,
  useInstantiateTemplate,
  useSaveTemplate,
  useDeleteTemplate,
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

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

const TEMPLATES: WorkflowTemplateMeta[] = [
  { id: "standard", name: "通用管线", description: "脚本→分镜", group: "通用", source: "builtin" },
  { id: "music", name: "音乐创作", description: "歌词→编曲", group: "创作", source: "builtin" },
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

describe("useSaveTemplate", () => {
  it("POSTs {name,description,projectId,workflowId} and invalidates the templates list", async () => {
    const created: WorkflowTemplateMeta = {
      id: "tpl-1",
      name: "我的管线",
      description: "自定义",
      group: "我的模板",
      source: "org",
      deletable: true,
      createdBy: "u1",
    }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(created, 201))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useSaveTemplate("org1"), { wrapper })
    result.current.mutate({
      name: "我的管线",
      description: "自定义",
      projectId: "p1",
      workflowId: "wf-1",
    })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/orgs/org1/workflow-templates",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST")
    expect(
      JSON.parse(String((fetchMock.mock.calls[0][1] as RequestInit).body)),
    ).toEqual({
      name: "我的管线",
      description: "自定义",
      projectId: "p1",
      workflowId: "wf-1",
    })
    expect(result.current.data).toEqual(created)
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["workflow-templates", "org1"],
    })
  })
})

describe("useDeleteTemplate", () => {
  it("DELETEs the template by id (204 no body) and invalidates the templates list", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useDeleteTemplate("org1"), { wrapper })
    result.current.mutate("tpl-1")
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe(
      "/api/orgs/org1/workflow-templates/tpl-1",
    )
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("DELETE")
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: ["workflow-templates", "org1"],
    })
  })
})
