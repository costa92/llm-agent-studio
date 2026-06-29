import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { act, renderHook } from "@testing-library/react"
import { useNodeTiming, formatDuration } from "./useNodeTiming"
import type { SseFrame } from "@/lib/types"

const f = (seq: number, kind: string, todoId: string): SseFrame => ({
  seq, kind, todoId, payload: {},
})

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe("formatDuration", () => {
  it("亚分钟显秒一位小数", () => expect(formatDuration(1234)).toBe("1.2s"))
  it("满分钟显 m s", () => expect(formatDuration(63000)).toBe("1m03s"))
})

describe("useNodeTiming seq 水位线", () => {
  it("回放帧不计时（baseline 之内的 started/finished 不产生耗时）", () => {
    const { result } = renderHook(() => useNodeTiming("plan1"))
    act(() => {
      result.current.onReplay([f(1, "todo_started", "t1"), f(2, "todo_finished", "t1")])
    })
    expect(result.current.timingByTodoId.get("t1")).toBeUndefined()
  })

  it("实时 started→finished 算耗时", () => {
    const { result } = renderHook(() => useNodeTiming("plan1"))
    act(() => result.current.onReplay([])) // baseline = 0
    act(() => {
      result.current.onFrame(f(1, "todo_started", "t1"))
    })
    act(() => vi.advanceTimersByTime(3000))
    act(() => {
      result.current.onFrame(f(2, "todo_finished", "t1"))
    })
    const t = result.current.timingByTodoId.get("t1")
    expect(t?.finishedAt).toBeDefined()
    expect(t!.elapsedMs).toBeGreaterThanOrEqual(3000)
  })

  it("running 节点随时间跳秒（无 finished 时 elapsed 增长）", () => {
    const { result } = renderHook(() => useNodeTiming("plan1"))
    act(() => result.current.onReplay([]))
    act(() => result.current.onFrame(f(1, "todo_started", "t1")))
    const e0 = result.current.timingByTodoId.get("t1")!.elapsedMs
    act(() => vi.advanceTimersByTime(2000))
    const e1 = result.current.timingByTodoId.get("t1")!.elapsedMs
    expect(e1).toBeGreaterThan(e0)
  })

  it("重连全量回放幂等：实时 startedAt 不被重放的同 todoId started 覆盖/回跳", () => {
    const { result } = renderHook(() => useNodeTiming("plan1"))
    act(() => result.current.onReplay([]))
    act(() => result.current.onFrame(f(5, "todo_started", "t1")))
    act(() => vi.advanceTimersByTime(4000))
    const before = result.current.timingByTodoId.get("t1")!.elapsedMs
    act(() => {
      result.current.onFrame(f(1, "todo_started", "t1"))
      result.current.onFrame(f(5, "todo_started", "t1"))
    })
    const after = result.current.timingByTodoId.get("t1")!.elapsedMs
    expect(after).toBeGreaterThanOrEqual(before)
  })

  it("planId 变更重置累积", () => {
    const { result, rerender } = renderHook(({ p }) => useNodeTiming(p), {
      initialProps: { p: "plan1" },
    })
    act(() => result.current.onReplay([]))
    act(() => result.current.onFrame(f(1, "todo_started", "t1")))
    expect(result.current.timingByTodoId.get("t1")).toBeDefined()
    rerender({ p: "plan2" })
    expect(result.current.timingByTodoId.get("t1")).toBeUndefined()
  })
})
