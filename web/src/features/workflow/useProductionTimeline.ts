import { useEffect, useReducer, useRef, useState } from "react"
import { fetchEventSource } from "@microsoft/fetch-event-source"
import { foldLog, type LogLine } from "@/lib/timeline"
import { streamRunEvents, type SseClient } from "@/lib/sse"
import type { ProjectState } from "@/lib/projectState"
import type { ProjectStatus, SseFrame, StudioEvent } from "@/lib/types"

// 制片轨道编排：进入工作台时**先回放历史事件**（GET /events）累积左栏日志，
// **再开实时 SSE 流**续接。关键不变式（替代 Last-Event-ID）：
//   服务端 sse.go 每次（重）连都从 after=0 全量回放——回放与实时帧必有重叠区间。
//   foldLog 按 frame.seq 去重，故先回放后开流的重叠帧被吞掉，日志绝不重复。
// 本里程碑后：状态推导已移至后端 projectstate.Compute；本 hook 只累积日志 +
// 把后端权威 state 帧经 onState 透给调用方写缓存。
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

// StudioEvent（回放）→ SseFrame（日志入参）。回放元素 todoId 可缺，补空串。
function toFrame(e: StudioEvent): SseFrame {
  return { seq: e.seq, kind: e.kind, todoId: e.todoId ?? "", payload: e.payload }
}

// 可注入依赖（生产默认真实实现；单测喂 fake）。
export interface TimelineDeps {
  // 回放：拉全部历史事件（GET /events 分页累积）。
  fetchAllEvents: (id: string, planId?: string) => Promise<StudioEvent[]>
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
  planId?: string
  // 后端权威 state 帧到达时回调（容器经 setQueryData 写缓存）。
  onState?: (s: ProjectState) => void
  // 回放批次到达（一次性，全部历史帧）。计时层据此取 seq 水位线 baseline。
  onReplay?: (frames: SseFrame[]) => void
  // 每个 SSE 帧到达（含服务端 after=0 回放与实时，计时层按 seq 水位线过滤）。
  onFrame?: (frame: SseFrame) => void
}

export interface ProductionTimeline {
  log: LogLine[]
  conn: SseConnState
  // 回放是否完成（用于区分"加载中"与"空轨道"）。
  replayed: boolean
}

type Action =
  | { type: "replayed"; frames: SseFrame[] }
  | { type: "frame"; frame: SseFrame }
  | { type: "reset" }

function logReducer(state: LogLine[], action: Action): LogLine[] {
  switch (action.type) {
    case "reset":
      return []
    case "replayed":
      return foldLog(state, action.frames)
    case "frame":
      return foldLog(state, [action.frame])
  }
}

// 编排 hook。回放 → 续接实时；完成态只回放。foldLog seq-dedup 吸收重叠。
export function useProductionTimeline({
  projectId,
  accessToken,
  status,
  enabled = true,
  fetchAllEvents,
  sseClient = fetchEventSource,
  planId,
  onState,
  onReplay,
  onFrame,
}: UseProductionTimelineArgs): ProductionTimeline {
  const [log, dispatch] = useReducer(logReducer, [])
  const [conn, setConn] = useState<SseConnState>("idle")
  const [replayed, setReplayed] = useState(false)
  // 避免对已卸载组件 setState。
  const aliveRef = useRef(true)
  // 回调走 ref，避免引用变化重起整条流。
  const onStateRef = useRef(onState)
  const onReplayRef = useRef(onReplay)
  const onFrameRef = useRef(onFrame)
  // token 也走 ref：不进主 effect deps（token 刷新轮换不重起整条流、不重放日志），
  // 但下方 streamRunEvents 拿 () => tokenRef.current，令 fetch-event-source 的每次
  // 自动重连都取到刷新后的最新 token（修 R2 SSE 隐患①：长 run 跨 TTL 后重连不带过期 token）。
  const tokenRef = useRef(accessToken)
  // ref 仅在异步 SSE 回调 / 重连里读取（提交后才可能有帧/重连），故写入放进无依赖 effect
  // （每次提交后运行）以满足 react-hooks/refs；读取点为异步，行为与 render 期赋值等价。
  useEffect(() => {
    onStateRef.current = onState
    onReplayRef.current = onReplay
    onFrameRef.current = onFrame
    tokenRef.current = accessToken
  })

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
      // 重置为新项目的空日志。
      dispatch({ type: "reset" })
      setReplayed(false)
      if (!terminal) setConn("reconnecting")

      // ── 1) 回放历史事件（累积日志）──
      let replayFrames: SseFrame[] = []
      try {
        const events = await fetchAllEvents(projectId, planId)
        if (cancelled) return
        replayFrames = events.map(toFrame)
        dispatch({ type: "replayed", frames: replayFrames })
      } catch {
        // 回放失败不阻断：实时流仍会从 after=0 全量回放（服务端语义）。
      } finally {
        if (!cancelled) setReplayed(true)
      }
      // onReplay 在 try/finally 之外透出：consumer 错误正常冒泡，
      // 不会被回放的网络错误 catch 伪装成网络失败而静默吞掉。
      if (!cancelled && replayFrames.length > 0) onReplayRef.current?.(replayFrames)

      // ── 2) 完成态：只回放不开流 ──
      if (terminal || accessToken == null || accessToken === "") {
        if (!cancelled) setConn("idle")
        return
      }

      // ── 3) 续接实时流（重叠帧被 seq-dedup 吞掉）──
      try {
        await streamRunEvents(
          projectId,
          () => tokenRef.current ?? "",
          {
            onEvent: (frame) => {
              if (!cancelled) {
                dispatch({ type: "frame", frame })
                try { onFrameRef.current?.(frame) } catch { /* consumer 错误不得拖垮流 */ }
              }
            },
            onMessage: (frame) => {
              // message 兜底帧也进日志，不改节点态。
              if (!cancelled) {
                dispatch({ type: "frame", frame })
                try { onFrameRef.current?.(frame) } catch { /* consumer 错误不得拖垮流 */ }
              }
            },
            onState: (raw) => {
              if (!cancelled) onStateRef.current?.(raw as ProjectState)
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
          planId,
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
  }, [projectId, status, enabled, planId])

  return { log, conn, replayed }
}
