import { describe, expect, it } from "vitest"
import { initialTimeline, reduceTimeline, foldEvents } from "./timeline"
import type { TimelineState } from "./timeline"
import type { SseFrame } from "./types"

// 一帧工厂。type 走 payload.type（worker 的 todo_* 事件 payload 形如 {type:"script"}）。
function f(
  seq: number,
  kind: string,
  todoId = "",
  payload?: Record<string, unknown>,
): SseFrame {
  return { seq, kind, todoId, payload }
}

// 取某固定阶段（S1..S5）的态。
function stage(state: TimelineState, id: TimelineState["stages"][number]["id"]) {
  const s = state.stages.find((x) => x.id === id)
  if (!s) throw new Error(`stage ${id} not found`)
  return s
}

// 把一串帧顺序折进 reducer。
function run(frames: SseFrame[], from: TimelineState = initialTimeline()): TimelineState {
  return foldEvents(from, frames)
}

describe("timeline reducer — initial state", () => {
  it("starts with 5 固定阶段 all blocked, slate hidden, run idle", () => {
    const s = initialTimeline()
    expect(s.stages.map((x) => x.id)).toEqual(["S1", "S2", "S3", "S4", "S5"])
    expect(s.stages.every((x) => x.status === "blocked")).toBe(true)
    expect(s.slateVisible).toBe(false)
    expect(s.runStatus).toBe("idle")
    expect(s.pips).toEqual([])
    expect(s.pendingAssetCount).toBe(0)
    expect(s.log).toEqual([])
  })
})

describe("timeline reducer — seq dedup (the safety net)", () => {
  it("ignores a frame whose seq was already applied", () => {
    const once = run([f(1, "planner_started")])
    const twice = reduceTimeline(once, f(1, "planner_started"))
    // 重复 seq 不重复渲染：state 引用不变 + 日志只 1 行。
    expect(twice).toBe(once)
    expect(twice.log).toHaveLength(1)
  })

  it("dedups across out-of-order / interleaved seqs but applies new ones", () => {
    let s = run([f(1, "planner_started"), f(2, "todo_ready", "t-s", { type: "script" })])
    s = reduceTimeline(s, f(2, "todo_ready", "t-s", { type: "script" })) // dup
    s = reduceTimeline(s, f(1, "planner_started")) // dup
    s = reduceTimeline(s, f(3, "todo_started", "t-s", { type: "script" })) // new
    expect(stage(s, "S2").status).toBe("running")
    // S2 pending→running 各 1 行 + planner_started 1 行 = 3，重复 seq 未追加。
    expect(s.log).toHaveLength(3)
  })
})

describe("timeline reducer — per-kind transitions (UI-spec §6.1)", () => {
  it("planner_started → S1 running, slate shown, run running", () => {
    const s = run([f(1, "planner_started")])
    expect(stage(s, "S1").status).toBe("running")
    expect(s.slateVisible).toBe(true)
    expect(s.runStatus).toBe("running")
  })

  it("todo_ready(type) → that stage blocked→pending", () => {
    const s = run([
      f(1, "planner_started"),
      f(2, "todo_ready", "t-s", { type: "script" }),
      f(3, "todo_ready", "t-b", { type: "storyboard" }),
    ])
    expect(stage(s, "S2").status).toBe("pending")
    expect(stage(s, "S3").status).toBe("pending")
    expect(stage(s, "S2").todoId).toBe("t-s")
  })

  it("todo_started(type=script) → S2 running", () => {
    const s = run([
      f(1, "planner_started"),
      f(2, "todo_ready", "t-s", { type: "script" }),
      f(3, "todo_started", "t-s", { type: "script" }),
    ])
    expect(stage(s, "S2").status).toBe("running")
  })

  it("todo_finished(type=script) → S2 done + 连接线着色", () => {
    const s = run([
      f(1, "planner_started"),
      f(2, "todo_started", "t-s", { type: "script" }),
      f(3, "todo_finished", "t-s", { type: "script", outputRef: "sc1" }),
    ])
    expect(stage(s, "S2").status).toBe("done")
    expect(stage(s, "S2").linked).toBe(true)
  })

  it("todo_finished(type=storyboard) → S3 done + 着色", () => {
    const s = run([
      f(1, "planner_started"),
      f(2, "todo_started", "t-b", { type: "storyboard" }),
      f(3, "todo_finished", "t-b", { type: "storyboard", outputRef: "sb1" }),
    ])
    expect(stage(s, "S3").status).toBe("done")
    expect(stage(s, "S3").linked).toBe(true)
  })
})

describe("timeline reducer — S4 asset PipGroup (N from per-shot asset todos)", () => {
  // worker 每 shot 发一个 todo_ready{type:asset}；N=不同 asset todoId 数（真实后端语义）。
  it("todo_ready(type=asset) ×N seeds N pips (idle) and S4 pending", () => {
    const s = run([
      f(1, "planner_started"),
      f(2, "todo_finished", "t-b", { type: "storyboard", outputRef: "sb1" }),
      f(3, "todo_ready", "a1", { type: "asset" }),
      f(4, "todo_ready", "a2", { type: "asset" }),
      f(5, "todo_ready", "a3", { type: "asset" }),
    ])
    expect(s.pipCount).toBe(3)
    expect(s.pips.map((p) => p.todoId)).toEqual(["a1", "a2", "a3"])
    expect(s.pips.every((p) => p.status === "idle")).toBe(true)
    expect(stage(s, "S4").status).toBe("pending")
  })

  it("todo_started(type=asset) → that pip running; asset_generated → that pip done + done/N 计数", () => {
    let s = run([
      f(1, "planner_started"),
      f(2, "todo_ready", "a1", { type: "asset" }),
      f(3, "todo_ready", "a2", { type: "asset" }),
    ])
    s = reduceTimeline(s, f(4, "todo_started", "a1", { type: "asset" }))
    expect(s.pips.find((p) => p.todoId === "a1")?.status).toBe("running")
    expect(stage(s, "S4").status).toBe("running")

    s = reduceTimeline(s, f(5, "asset_generated", "a1", { assetId: "img1", status: "pending_acceptance" }))
    expect(s.pips.find((p) => p.todoId === "a1")?.status).toBe("done")
    expect(s.doneAssetCount).toBe(1)
    expect(s.pendingAssetCount).toBe(1)
    // 未全部 done：S4 仍 running，S5 仍 blocked。
    expect(stage(s, "S4").status).toBe("running")
    expect(stage(s, "S5").status).toBe("blocked")
  })

  it("all asset pips done → S4 done, S5 Review pending", () => {
    const s = run([
      f(1, "planner_started"),
      f(2, "todo_ready", "a1", { type: "asset" }),
      f(3, "todo_ready", "a2", { type: "asset" }),
      f(4, "todo_started", "a1", { type: "asset" }),
      f(5, "asset_generated", "a1", { assetId: "img1" }),
      f(6, "todo_started", "a2", { type: "asset" }),
      f(7, "asset_generated", "a2", { assetId: "img2" }),
    ])
    expect(s.pips.every((p) => p.status === "done")).toBe(true)
    expect(s.doneAssetCount).toBe(2)
    expect(stage(s, "S4").status).toBe("done")
    expect(stage(s, "S5").status).toBe("pending")
  })
})

describe("timeline reducer — failure + retry (原型第6个 pip 重试范式)", () => {
  it("todo_failed(type=asset) → pip failed; same todoId todo_started → pip back to running", () => {
    let s = run([
      f(1, "planner_started"),
      f(2, "todo_ready", "a1", { type: "asset" }),
      f(3, "todo_started", "a1", { type: "asset" }),
    ])
    s = reduceTimeline(s, f(4, "todo_failed", "a1", { type: "asset", error: "boom" }))
    expect(s.pips.find((p) => p.todoId === "a1")?.status).toBe("failed")
    // 重试：同 todoId 再来 todo_started → pip 回 running。
    s = reduceTimeline(s, f(5, "todo_started", "a1", { type: "asset" }))
    expect(s.pips.find((p) => p.todoId === "a1")?.status).toBe("running")
  })

  it("todo_failed(stage-level, type=script) → S2 failed, downstream stays blocked", () => {
    const s = run([
      f(1, "planner_started"),
      f(2, "todo_started", "t-s", { type: "script" }),
      f(3, "todo_failed", "t-s", { type: "script", error: "retries exhausted" }),
    ])
    expect(stage(s, "S2").status).toBe("failed")
    expect(stage(s, "S3").status).toBe("blocked")
    expect(stage(s, "S4").status).toBe("blocked")
  })
})

describe("timeline reducer — log-only events (no node-state change)", () => {
  it("asset_submitted (M4 async) → pip stays running, log appended", () => {
    let s = run([
      f(1, "planner_started"),
      f(2, "todo_ready", "a1", { type: "asset" }),
      f(3, "todo_started", "a1", { type: "asset" }),
    ])
    const before = s.pips.find((p) => p.todoId === "a1")?.status
    s = reduceTimeline(s, f(4, "asset_submitted", "a1", { assetId: "img1", externalJobId: "job-1" }))
    expect(s.pips.find((p) => p.todoId === "a1")?.status).toBe(before) // running, unchanged
    expect(s.log.at(-1)?.kind).toBe("asset_submitted")
  })

  it("asset_prescreened (M3 预筛) → pip unchanged, review state untouched, log appended", () => {
    let s = run([
      f(1, "planner_started"),
      f(2, "todo_ready", "a1", { type: "asset" }),
      f(3, "todo_started", "a1", { type: "asset" }),
      f(4, "asset_generated", "a1", { assetId: "img1" }),
    ])
    const s4before = stage(s, "S5").status
    s = reduceTimeline(s, f(5, "asset_prescreened", "a1", { assetId: "img1", score: 0.9, flags: [] }))
    expect(s.pips.find((p) => p.todoId === "a1")?.status).toBe("done")
    expect(stage(s, "S5").status).toBe(s4before) // 审核态不变（HITL 走 review board）
    expect(s.log.at(-1)?.kind).toBe("asset_prescreened")
  })

  it("message fallback (unknown kind) → log only, no node-state change", () => {
    const before = run([f(1, "planner_started")])
    const after = reduceTimeline(before, f(2, "heartbeat"))
    expect(after.stages).toEqual(before.stages)
    expect(after.slateVisible).toBe(before.slateVisible)
    expect(after.log.at(-1)?.kind).toBe("heartbeat")
  })
})

describe("timeline reducer — run_done terminal frame", () => {
  it("run_done → slate hidden, run done, terminal signal flips once", () => {
    const s = run([
      f(1, "planner_started"),
      f(2, "todo_ready", "a1", { type: "asset" }),
      f(3, "todo_started", "a1", { type: "asset" }),
      f(4, "asset_generated", "a1", { assetId: "img1" }),
      f(5, "run_done"),
    ])
    expect(s.slateVisible).toBe(false)
    expect(s.runStatus).toBe("done")
    expect(s.pendingAssetCount).toBe(1) // 徽标「待审核 · N」
  })
})

describe("timeline reducer — replay → live continuity (the core invariant)", () => {
  it("replaying full history then receiving live frames does not double-apply", () => {
    // 服务端每次（重）连都从 after=0 全量回放。模拟：先回放 1..4，再「重连」回放 1..4 + 实时 5..6。
    const history = [
      f(1, "planner_started"),
      f(2, "todo_ready", "a1", { type: "asset" }),
      f(3, "todo_started", "a1", { type: "asset" }),
      f(4, "asset_generated", "a1", { assetId: "img1" }),
    ]
    const afterFirstConnect = run(history)

    // 重连：全量回放（1..4 重复）+ 续接实时（5 run_done）。
    const replayThenLive = [...history, f(5, "run_done")]
    const afterReconnect = foldEvents(afterFirstConnect, replayThenLive)

    // 旧帧被 seq-dedup 吞掉：doneAssetCount 不翻倍，pip 仍单个 done。
    expect(afterReconnect.doneAssetCount).toBe(1)
    expect(afterReconnect.pendingAssetCount).toBe(1)
    expect(afterReconnect.pips.filter((p) => p.todoId === "a1")).toHaveLength(1)
    // 实时 run_done 仍被吸收。
    expect(afterReconnect.runStatus).toBe("done")
    expect(afterReconnect.slateVisible).toBe(false)
    // 日志无重复回放行：history 5 帧各 1 行（planner/ready/started/generated）+ run_done 1 行。
    expect(afterReconnect.log.map((l) => l.seq)).toEqual([1, 2, 3, 4, 5])
  })

  it("a fresh reducer fed the full replay reaches the same state as incremental live", () => {
    const frames = [
      f(1, "planner_started"),
      f(2, "todo_ready", "t-s", { type: "script" }),
      f(3, "todo_started", "t-s", { type: "script" }),
      f(4, "todo_finished", "t-s", { type: "script", outputRef: "sc1" }),
      f(5, "run_done"),
    ]
    // incremental (live, one at a time)
    let live = initialTimeline()
    for (const fr of frames) live = reduceTimeline(live, fr)
    // bulk replay
    const replay = foldEvents(initialTimeline(), frames)

    expect(replay.stages).toEqual(live.stages)
    expect(replay.runStatus).toBe(live.runStatus)
    expect(replay.slateVisible).toBe(live.slateVisible)
    expect(replay.log).toEqual(live.log)
  })
})
