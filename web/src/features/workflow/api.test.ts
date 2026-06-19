import { afterEach, describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createElement, type ReactNode } from "react"
import { fetchAllEvents, fetchProjectState, fetchScript, useCancel, useRun } from "./api"
import { setAccessToken } from "@/lib/apiClient"
import { jsonResponse } from "@/test/helpers"

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

describe("fetchAllEvents (keyset replay accumulation)", () => {
  it("accumulates pages by afterSeq until a short page signals the end", async () => {
    // 第一页满 200 行（seq 1..200）→ 继续；第二页 2 行 → 到底。
    const page1 = Array.from({ length: 200 }, (_, i) => ({
      seq: i + 1,
      kind: "todo_ready",
      todoId: `t${i + 1}`,
    }))
    const page2 = [
      { seq: 201, kind: "asset_generated", todoId: "t201" },
      { seq: 202, kind: "run_done", todoId: "" },
    ]
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ items: page1 }))
      .mockResolvedValueOnce(jsonResponse({ items: page2 }))
    vi.stubGlobal("fetch", fetchMock)

    const all = await fetchAllEvents("p1")

    expect(all).toHaveLength(202)
    // 第二次请求 afterSeq = 上一页末 seq (200)。
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(String(fetchMock.mock.calls[0][0])).toContain("afterSeq=0")
    expect(String(fetchMock.mock.calls[1][0])).toContain("afterSeq=200")
  })

  it("stops after a single short page", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ items: [{ seq: 1, kind: "planner_started", todoId: "" }] }))
    vi.stubGlobal("fetch", fetchMock)

    const all = await fetchAllEvents("p1")
    expect(all).toHaveLength(1)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

describe("fetchProjectState (GET /state authoritative snapshot)", () => {
  it("fetches /state and returns the ProjectState", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          projectId: "p1",
          version: 5,
          status: "running",
          runStatus: "running",
          stages: [],
          pips: [],
          assets: { total: 0, done: 0, pending: 0 },
        }),
      ),
    )
    const st = await fetchProjectState("p1")
    expect(st.status).toBe("running")
    expect(st.version).toBe(5)
  })
})

describe("fetchScript (bare JSON, 404 → null, tolerant parse)", () => {
  it("returns null on 404 (script not generated yet)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("no script yet", { status: 404 })),
    )
    expect(await fetchScript("p1")).toBeNull()
  })

  it("parses bare script JSON (NOT an {items} envelope)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          title: "茶馆黄昏",
          logline: "传承故事",
          scenes: [{ heading: "S1", description: "黄昏", dialogue: "…" }],
        }),
      ),
    )
    const doc = await fetchScript("p1")
    expect(doc?.title).toBe("茶馆黄昏")
    expect(doc?.scenes).toHaveLength(1)
    expect(doc?.scenes?.[0].heading).toBe("S1")
  })

  it("tolerates extra/unknown fields via passthrough", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ title: "x", extra: 1 })),
    )
    const doc = await fetchScript("p1")
    expect(doc?.title).toBe("x")
  })
})

describe("useRun (POST /run)", () => {
  // 重新运行会新建一个 plan（res.planId），运行页随即导航到新 runId。
  // isLatestPlan 由 usePlans()[0].id === runId 推导——若不失效 ["plans"]，
  // 列表仍是旧 plan，新页 isLatestPlan=false → Run/Cancel 静默禁用直至手动刷新。
  it("invalidates both the project AND the plans list on success", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ planId: "plan9", valid: true, fallbackUsed: false }),
    )
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useRun("p1"), { wrapper })
    result.current.mutate()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe("/api/projects/p1/run")
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST")
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["project", "p1"] })
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["plans", "p1"] })
  })
})

describe("useCancel (POST /cancel)", () => {
  // 取消不新建 plan，isLatestPlan/列表顺序不变，故仅失效项目即可。
  it("invalidates the project on success", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(jsonResponse({ status: "canceled" }))
    vi.stubGlobal("fetch", fetchMock)

    const { wrapper, invalidateSpy } = setup()
    const { result } = renderHook(() => useCancel("p1"), { wrapper })
    result.current.mutate()
    await waitFor(() => expect(result.current.isSuccess).toBe(true))

    expect(String(fetchMock.mock.calls[0][0])).toBe("/api/projects/p1/cancel")
    expect((fetchMock.mock.calls[0][1] as RequestInit).method).toBe("POST")
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["project", "p1"] })
  })
})
