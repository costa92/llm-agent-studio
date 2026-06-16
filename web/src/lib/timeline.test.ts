import { describe, it, expect } from "vitest"
import { foldLog, logFor } from "./timeline"
import type { SseFrame } from "./types"

const f = (seq: number, kind: string, payload?: unknown, todoId = "t1"): SseFrame => ({
  seq,
  kind,
  todoId,
  payload,
})

describe("logFor 文案", () => {
  it("planner_started → 规划开始", () => {
    expect(logFor(f(1, "planner_started")).text).toBe("规划开始")
  })
  it("todo_failed 透出 error 文本", () => {
    expect(logFor(f(2, "todo_failed", { type: "asset", error: "boom" })).text).toBe("失败：boom")
  })
})

describe("foldLog 按 seq 去重（重连全量回放幂等）", () => {
  it("重复 seq 不重复追加", () => {
    let log = foldLog([], [f(1, "planner_started"), f(2, "todo_started", { type: "script" })])
    log = foldLog(log, [f(1, "planner_started"), f(3, "run_done")]) // seq 1 重复
    expect(log.map((l) => l.seq)).toEqual([1, 2, 3])
  })
})
