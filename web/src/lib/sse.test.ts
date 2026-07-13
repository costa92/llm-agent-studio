import { describe, expect, it, vi } from "vitest"
import { streamRunEvents } from "./sse"
import type { SseFrame } from "./types"
import type { SseClient, SseHandlers } from "./sse"

// 注入式 fake client（同 kb M5b 范式）：脚本化 {id,event,data} 帧序列喂 onmessage，
// 末了 onclose。返回最近一次拿到的 (input, init) 供断言请求形态。
function fakeClient(frames: Array<{ id?: string; event: string; data: string }>): {
  client: SseClient
  calls: Array<{ input: RequestInfo; init: Parameters<SseClient>[1] }>
} {
  const calls: Array<{ input: RequestInfo; init: Parameters<SseClient>[1] }> = []
  const client: SseClient = async (input, init) => {
    calls.push({ input, init })
    if (init.onopen) await init.onopen(new Response(null, { status: 200 }))
    for (const f of frames) {
      init.onmessage?.({ id: f.id ?? "", event: f.event, data: f.data, retry: undefined })
    }
    init.onclose?.()
  }
  return { client, calls }
}

function frame(seq: number, kind: string, todoId = "", payload?: unknown): SseFrame {
  return { seq, kind, todoId, payload }
}

describe("streamRunEvents", () => {
  it("invokes onEvent in order for each named frame, parsing {seq,kind,todoId,payload}", async () => {
    const got: SseFrame[] = []
    const handlers: SseHandlers = { onEvent: (f) => got.push(f) }
    const { client } = fakeClient([
      { event: "planner_started", data: JSON.stringify(frame(1, "planner_started")) },
      {
        event: "todo_ready",
        data: JSON.stringify(frame(2, "todo_ready", "t1", { type: "script" })),
      },
      {
        event: "todo_started",
        data: JSON.stringify(frame(3, "todo_started", "t1", { type: "script" })),
      },
    ])

    await streamRunEvents("p1", () => "tok", handlers, client)

    expect(got.map((f) => f.seq)).toEqual([1, 2, 3])
    expect(got.map((f) => f.kind)).toEqual([
      "planner_started",
      "todo_ready",
      "todo_started",
    ])
    expect(got[1]).toMatchObject({ seq: 2, kind: "todo_ready", todoId: "t1" })
    expect(got[1].payload).toEqual({ type: "script" })
  })

  it("opens a GET stream (openWhenHidden) whose injected fetch stamps a FRESH Bearer on each (re)connect", async () => {
    // 注入的 fetch 在每次（重）连时从 getToken() 现取 token —— 模拟 token 刷新后
    // fetch-event-source 自动重连须带新 token（R2 SSE 隐患①）。用 mock 全局 fetch 观察 header。
    // 泛型标注调用签名（实现不带形参，避免 no-unused-vars），使 mock.calls[i][1] 类型为 RequestInit。
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () => new Response(null, { status: 200 }),
    )
    vi.stubGlobal("fetch", fetchMock)
    let token = "tok-1"
    const { client, calls } = fakeClient([])

    await streamRunEvents("proj-42", () => token, { onEvent: () => {} }, client)

    expect(calls).toHaveLength(1)
    const { input, init } = calls[0]
    expect(input).toBe("/api/projects/proj-42/events/stream")
    expect(init.method ?? "GET").toBe("GET")
    expect(init.openWhenHidden).toBe(true)
    // 首连：注入的 fetch 用当前 token。
    await init.fetch!("/api/projects/proj-42/events/stream", {})
    expect(fetchMock.mock.calls[0][1]?.headers).toMatchObject({
      Authorization: "Bearer tok-1",
    })
    // token 刷新后重连：再次调用注入的 fetch，必须带刷新后的 token（非首连时捕获的旧值）。
    token = "tok-2"
    await init.fetch!("/api/projects/proj-42/events/stream", {})
    expect(fetchMock.mock.calls[1][1]?.headers).toMatchObject({
      Authorization: "Bearer tok-2",
    })
    vi.unstubAllGlobals()
  })

  it("fires onDone when the terminal run_done frame arrives", async () => {
    const events: SseFrame[] = []
    const onDone = vi.fn()
    const { client } = fakeClient([
      { event: "asset_generated", data: JSON.stringify(frame(7, "asset_generated", "t3")) },
      { event: "run_done", data: JSON.stringify(frame(8, "run_done")) },
    ])

    await streamRunEvents(
      "p1",
      () => "tok",
      { onEvent: (f) => events.push(f), onDone },
      client,
    )

    // run_done is still surfaced as an event (so the reducer sees its seq), AND triggers onDone.
    expect(events.map((f) => f.kind)).toEqual(["asset_generated", "run_done"])
    expect(onDone).toHaveBeenCalledTimes(1)
    expect(onDone.mock.calls[0][0]).toMatchObject({ seq: 8, kind: "run_done" })
  })

  it("routes unknown kinds through onMessage fallback (original kind kept in the frame)", async () => {
    const events: SseFrame[] = []
    const messages: SseFrame[] = []
    const { client } = fakeClient([
      // server emits non-whitelisted kinds as the generic `message` event; kind stays in payload/frame.
      { event: "message", data: JSON.stringify(frame(4, "heartbeat")) },
      { event: "todo_finished", data: JSON.stringify(frame(5, "todo_finished", "t1")) },
    ])

    await streamRunEvents(
      "p1",
      () => "tok",
      { onEvent: (f) => events.push(f), onMessage: (f) => messages.push(f) },
      client,
    )

    expect(events.map((f) => f.kind)).toEqual(["todo_finished"])
    expect(messages.map((f) => f.kind)).toEqual(["heartbeat"])
    expect(messages[0]).toMatchObject({ seq: 4, kind: "heartbeat" })
  })

  it("surfaces connection lifecycle (onConnecting/onOpen/onClose) to the caller", async () => {
    const phases: string[] = []
    const { client } = fakeClient([])

    await streamRunEvents(
      "p1",
      () => "tok",
      {
        onEvent: () => {},
        onOpen: () => phases.push("open"),
        onClose: () => phases.push("close"),
      },
      client,
    )

    expect(phases).toEqual(["open", "close"])
  })

  it("forwards the abort signal to the client for cancellation", async () => {
    const controller = new AbortController()
    const { client, calls } = fakeClient([])

    await streamRunEvents(
      "p1",
      () => "tok",
      { onEvent: () => {} },
      client,
      controller.signal,
    )

    expect(calls[0].init.signal).toBe(controller.signal)
  })
})
