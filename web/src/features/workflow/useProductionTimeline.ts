import { useEffect, useReducer, useRef, useState } from "react"
import { fetchEventSource } from "@microsoft/fetch-event-source"
import {
  foldEvents,
  initialTimeline,
  reduceTimeline,
  type TimelineState,
} from "@/lib/timeline"
import { streamRunEvents, type SseClient } from "@/lib/sse"
import type { ProjectStatus, SseFrame, StudioEvent } from "@/lib/types"

// 制片轨道编排：进入工作台时**先回放历史事件**（GET /events）重建轨道全态，
// **再开实时 SSE 流**续接。关键不变式（替代 Last-Event-ID）：
//   服务端 sse.go 每次（重）连都从 after=0 全量回放——回放与实时帧必有重叠区间。
//   Task 8 reducer 按 frame.seq 去重，故先回放后开流的重叠帧被 seq-dedup 吞掉，绝不重复渲染。
// 完成态项目（completed/review/failed/canceled）**只回放、不开流**（UI-spec §11 决策 6）。

// SSE 连接态（喂 SseIndicator）。
export type SseConnState = "idle" | "connected" | "reconnecting" | "disconnected"

// 完成态：只回放不开流。
const TERMINAL_STATUSES: ReadonlySet<ProjectStatus> = new Set<ProjectStatus>([
  "completed",
  "review",
  "failed",
  "canceled",
])

export function isTerminalStatus(status: ProjectStatus | undefined): boolean {
  return status != null && TERMINAL_STATUSES.has(status)
}

// StudioEvent（回放）→ SseFrame（reducer 入参）。回放元素 todoId 可缺，补空串。
function toFrame(e: StudioEvent): SseFrame {
  return { seq: e.seq, kind: e.kind, todoId: e.todoId ?? "", payload: e.payload }
}

// 可注入依赖（生产默认真实实现；单测喂 fake）。
export interface TimelineDeps {
  // 回放：拉全部历史事件（GET /events 分页累积）。
  fetchAllEvents: (id: string) => Promise<StudioEvent[]>
  // SSE 客户端（默认 fetchEventSource；测试喂脚本化 fake）。
  sseClient?: SseClient
}

export interface UseProductionTimelineArgs extends TimelineDeps {
  projectId: string
  // 当前 access token（SSE Bearer 头）。null/空 → 不开流。
  accessToken: string | null
  // 项目状态——完成态只回放不开流。undefined（加载中）暂不开流。
  status: ProjectStatus | undefined
  // 是否启用（projectId 就绪后）。
  enabled?: boolean
}

export interface ProductionTimeline {
  state: TimelineState
  conn: SseConnState
  // 回放是否完成（用于区分"加载中"与"空轨道"）。
  replayed: boolean
}

type Action =
  | { type: "replayed"; frames: SseFrame[] }
  | { type: "frame"; frame: SseFrame }
  | { type: "reset" }

function timelineReducer(state: TimelineState, action: Action): TimelineState {
  switch (action.type) {
    case "reset":
      return initialTimeline()
    case "replayed":
      return foldEvents(state, action.frames)
    case "frame":
      return reduceTimeline(state, action.frame)
  }
}

// 编排 hook。回放 → 续接实时；完成态只回放。seq-dedup 吸收重叠。
export function useProductionTimeline({
  projectId,
  accessToken,
  status,
  enabled = true,
  fetchAllEvents,
  sseClient = fetchEventSource,
}: UseProductionTimelineArgs): ProductionTimeline {
  const [state, dispatch] = useReducer(timelineReducer, undefined, initialTimeline)
  const [conn, setConn] = useState<SseConnState>("idle")
  const [replayed, setReplayed] = useState(false)
  // 避免对已卸载组件 setState。
  const aliveRef = useRef(true)

  useEffect(() => {
    aliveRef.current = true
    return () => {
      aliveRef.current = false
    }
  }, [])

  useEffect(() => {
    if (!enabled || projectId === "" || status === undefined) return

    const controller = new AbortController()
    const terminal = isTerminalStatus(status)
    let cancelled = false

    async function run() {
      // 让出一个微任务再 setState，避免 effect 体内同步 setState 触发级联渲染
      // （react-hooks/set-state-in-effect）。
      await Promise.resolve()
      if (cancelled) return
      // 重置为新项目的初态。
      dispatch({ type: "reset" })
      setReplayed(false)
      if (!terminal) setConn("reconnecting")

      // ── 1) 回放历史事件（重建全态）──
      try {
        const events = await fetchAllEvents(projectId)
        if (cancelled) return
        dispatch({ type: "replayed", frames: events.map(toFrame) })
      } catch {
        // 回放失败不阻断：实时流仍会从 after=0 全量回放（服务端语义）。
      } finally {
        if (!cancelled) setReplayed(true)
      }

      // ── 2) 完成态：只回放不开流 ──
      if (terminal || accessToken == null || accessToken === "") {
        if (!cancelled) setConn("idle")
        return
      }

      // ── 3) 续接实时流（重叠帧被 seq-dedup 吞掉）──
      try {
        await streamRunEvents(
          projectId,
          accessToken,
          {
            onEvent: (frame) => {
              if (!cancelled) dispatch({ type: "frame", frame })
            },
            onMessage: (frame) => {
              // message 兜底帧也进 reducer（日志），不改节点态。
              if (!cancelled) dispatch({ type: "frame", frame })
            },
            onOpen: () => {
              if (!cancelled) setConn("connected")
            },
            onError: () => {
              // fetch-event-source 自动重连——标记重连态。
              if (!cancelled) setConn("reconnecting")
            },
            onClose: () => {
              if (!cancelled) setConn("disconnected")
            },
          },
          sseClient,
          controller.signal,
        )
      } catch {
        if (!cancelled) setConn("disconnected")
      }
    }

    void run()

    return () => {
      cancelled = true
      controller.abort()
    }
    // accessToken 变化（刷新轮换）不应重起整条流——SSE 客户端用首次 token；
    // 重连由 fetch-event-source 处理。故 deps 只含 projectId/status/enabled。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, status, enabled])

  return { state, conn, replayed }
}
