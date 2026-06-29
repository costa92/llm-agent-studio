import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react"
import type { SseFrame } from "@/lib/types"

export interface NodeTiming {
  startedAt: number
  finishedAt?: number
  elapsedMs: number
}

// 耗时格式化：<60s 显秒一位小数（"3.4s"），>=60s 显 "1m03s"。
export function formatDuration(ms: number): string {
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  const total = Math.floor(ms / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return `${m}m${String(s).padStart(2, "0")}s`
}

interface State {
  watermark: number
  started: Record<string, number>
  finished: Record<string, number>
}
const INIT: State = { watermark: 0, started: {}, finished: {} }

type Action =
  | { type: "reset" }
  | { type: "replay"; maxSeq: number }
  | { type: "frame"; seq: number; kind: string; todoId: string; at: number }

function reducer(state: State, action: Action): State {
  switch (action.type) {
    case "reset":
      return INIT
    case "replay":
      // 回放批次只抬高 baseline 水位线，绝不产生耗时。
      return { ...state, watermark: Math.max(state.watermark, action.maxSeq) }
    case "frame": {
      // seq 水位线：<=水位线 = 回放/重连重放/重复 → 忽略（幂等）。
      if (action.seq <= state.watermark) return state
      const next: State = {
        watermark: action.seq,
        started: state.started,
        finished: state.finished,
      }
      if (action.kind === "todo_started" && !(action.todoId in state.started)) {
        next.started = { ...state.started, [action.todoId]: action.at }
      } else if (action.kind === "todo_finished") {
        next.finished = { ...state.finished, [action.todoId]: action.at }
      }
      return next
    }
  }
}

// 运行态节点耗时：仅实时（seq>水位线）帧产生耗时，覆盖「回放假耗时」与「重连回跳」。
// onReplay/onFrame 交给 useProductionTimeline 的同名出口。planId 变更重置。
export function useNodeTiming(planId: string): {
  timingByTodoId: Map<string, NodeTiming>
  onReplay: (frames: SseFrame[]) => void
  onFrame: (frame: SseFrame) => void
} {
  const [state, dispatch] = useReducer(reducer, INIT)
  // running 跳秒：仅在有未完成节点时启动 interval，bump now。
  const [now, setNow] = useState(() => Date.now())

  // planId 变更：重置累积。
  const planRef = useRef(planId)
  useEffect(() => {
    if (planRef.current !== planId) {
      planRef.current = planId
      dispatch({ type: "reset" })
    }
  }, [planId])

  const onReplay = useCallback((frames: SseFrame[]) => {
    const maxSeq = frames.reduce((m, f) => Math.max(m, f.seq), 0)
    dispatch({ type: "replay", maxSeq })
  }, [])

  const onFrame = useCallback((frame: SseFrame) => {
    dispatch({
      type: "frame",
      seq: frame.seq,
      kind: frame.kind,
      todoId: frame.todoId,
      at: Date.now(),
    })
  }, [])

  const hasRunning = useMemo(
    () => Object.keys(state.started).some((id) => !(id in state.finished)),
    [state.started, state.finished],
  )

  useEffect(() => {
    if (!hasRunning) return
    setNow(Date.now())
    const h = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(h)
  }, [hasRunning])

  const timingByTodoId = useMemo(() => {
    const out = new Map<string, NodeTiming>()
    for (const [todoId, startedAt] of Object.entries(state.started)) {
      const finishedAt = state.finished[todoId]
      const elapsedMs = (finishedAt ?? now) - startedAt
      out.set(todoId, { startedAt, finishedAt, elapsedMs })
    }
    return out
  }, [state.started, state.finished, now])

  return { timingByTodoId, onReplay, onFrame }
}
