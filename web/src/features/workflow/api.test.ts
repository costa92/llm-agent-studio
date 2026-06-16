import { afterEach, describe, expect, it, vi } from "vitest"
import { fetchAllEvents, fetchProjectState, fetchScript } from "./api"
import { setAccessToken } from "@/lib/apiClient"
import { jsonResponse } from "@/test/helpers"

afterEach(() => {
  vi.restoreAllMocks()
  setAccessToken(null)
})

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
