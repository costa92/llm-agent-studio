import { describe, expect, it, vi } from "vitest"
import { renderHook, waitFor } from "@testing-library/react"
import { useProductionTimeline } from "./useProductionTimeline"
import type { SseClient } from "@/lib/sse"
import type { ProjectStatus, StudioEvent } from "@/lib/types"

// 脚本化 fake SSE client：开 onopen → 顺序喂 frames → onclose。
function fakeSse(
  frames: Array<{ event: string; data: string }>,
): SseClient {
  return async (_input, init) => {
    await init.onopen?.(new Response(null, { status: 200 }))
    for (const f of frames) {
      init.onmessage?.({ id: "", event: f.event, data: f.data, retry: undefined })
    }
    init.onclose?.()
  }
}

function ev(seq: number, kind: string, todoId = "", payload?: unknown): StudioEvent {
  return { seq, kind, todoId, payload }
}

function frameData(seq: number, kind: string, todoId = "", payload?: unknown) {
  return JSON.stringify({ seq, kind, todoId, payload })
}

describe("useProductionTimeline (replay-then-live orchestration)", () => {
  it("replays history first, then streams live; seq-dedup absorbs the overlap", async () => {
    // 历史回放：S1 规划 + S2 剧本就绪/开始（seq 1-3）。
    const history: StudioEvent[] = [
      ev(1, "planner_started"),
      ev(2, "todo_ready", "t-script", { type: "script" }),
      ev(3, "todo_started", "t-script", { type: "script" }),
    ]
    const fetchAllEvents = vi.fn().mockResolvedValue(history)

    // 实时流：故意**重发** seq 2-3（重连全量回放语义）+ 新帧 seq 4（剧本完成）。
    const sseClient = fakeSse([
      { event: "planner_started", data: frameData(1, "planner_started") },
      { event: "todo_ready", data: frameData(2, "todo_ready", "t-script", { type: "script" }) },
      { event: "todo_started", data: frameData(3, "todo_started", "t-script", { type: "script" }) },
      { event: "todo_finished", data: frameData(4, "todo_finished", "t-script", { type: "script" }) },
    ])

    const { result } = renderHook(() =>
      useProductionTimeline({
        projectId: "p1",
        accessToken: "tok",
        status: "running" as ProjectStatus,
        fetchAllEvents,
        sseClient,
      }),
    )

    // 回放先发生。
    await waitFor(() => expect(fetchAllEvents).toHaveBeenCalledWith("p1", undefined))

    // 续接实时后：S2 应为 done（seq 4），且**没有重复渲染**——日志里 seq 1/2/3 各仅一条。
    await waitFor(() => {
      const s2 = result.current.state.stages.find((s) => s.id === "S2")
      expect(s2?.status).toBe("done")
    })

    const log = result.current.state.log
    const seqs = log.map((l) => l.seq).sort((a, b) => a - b)
    // 重发的 seq 1/2/3 被 dedup：每个 seq 恰好一条日志（1,2,3,4），无重复。
    expect(seqs).toEqual([1, 2, 3, 4])
    expect(result.current.state.appliedSeqs.size).toBe(4)
    // 流跑完后 fake client 调 onclose → disconnected（生产中 run_done 后流亦关闭）。
    // 关键是实时流确被开启并喂入了新帧（S2 done 来自实时 seq 4）。
    expect(result.current.conn).toBe("disconnected")
  })

  it("terminal-status projects replay only and do NOT open a live stream", async () => {
    const history: StudioEvent[] = [
      ev(1, "planner_started"),
      ev(2, "run_done"),
    ]
    const fetchAllEvents = vi.fn().mockResolvedValue(history)
    const sseClient = vi.fn() as unknown as SseClient

    const { result } = renderHook(() =>
      useProductionTimeline({
        projectId: "p9",
        accessToken: "tok",
        status: "completed" as ProjectStatus,
        fetchAllEvents,
        sseClient,
      }),
    )

    await waitFor(() => expect(result.current.replayed).toBe(true))
    // 回放重建了全态（run_done → runStatus done）。
    expect(result.current.state.runStatus).toBe("done")
    // 但**没有开流**：SSE client 从未被调用，连接态保持 idle。
    expect(sseClient).not.toHaveBeenCalled()
    expect(result.current.conn).toBe("idle")
  })

  it("does not open the stream until the project status is known (undefined)", async () => {
    const fetchAllEvents = vi.fn().mockResolvedValue([])
    const sseClient = vi.fn() as unknown as SseClient

    renderHook(() =>
      useProductionTimeline({
        projectId: "p1",
        accessToken: "tok",
        status: undefined,
        fetchAllEvents,
        sseClient,
      }),
    )

    // status 未知 → 既不回放也不开流。
    await new Promise((r) => setTimeout(r, 10))
    expect(fetchAllEvents).not.toHaveBeenCalled()
    expect(sseClient).not.toHaveBeenCalled()
  })
})
