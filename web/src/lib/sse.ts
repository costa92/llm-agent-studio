// 制片轨道实时流客户端：GET /api/projects/{id}/events/stream over fetch-event-source。
//
// 为何用 @microsoft/fetch-event-source 而非原生 EventSource：流挂在 proj(roleViewer)
// 下，受 Authenticate 中间件包裹，必须带 Authorization: Bearer 头；而原生 EventSource
// 无法设请求头（只能带 cookie），studio 的 access token 不在 cookie 里——故用 fetch-event-source。
//
// 续传机制（核对 sse.go:51-66）：服务端不发 id: 字段、不读 Last-Event-ID，每次（重）连
// 都从 after=0 全量回放历史事件。本客户端不依赖 Last-Event-ID；重连后的重复回放帧由
// Task 8 reducer 按 frame.seq 去重。本层只负责把每帧的 seq 原样透出，让上层能去重。
import { fetchEventSource } from "@microsoft/fetch-event-source"
import type { SseFrame } from "./types"

// sse.go:22 sseEventNames 白名单（9 种命名事件）。未在白名单的 kind 以通用 `message` 事件流出。
const NAMED_EVENTS = new Set<string>([
  "planner_started",
  "todo_ready",
  "todo_started",
  "todo_finished",
  "todo_failed",
  "asset_generated",
  "asset_prescreened",
  "asset_submitted",
  "run_done",
])

// run_done 是终止帧（服务端见到后关闭流）。
const TERMINAL_EVENT = "run_done"

// 注入式 client 的签名（生产用 fetchEventSource；测试喂 fake）。
// 与 @microsoft/fetch-event-source 的 fetchEventSource 形状一致：第二参带
// onopen/onmessage/onclose/onerror/openWhenHidden/headers/signal/method。
export type SseClient = typeof fetchEventSource

// 上层（SseIndicator + Task 8 reducer）消费的回调集合。
export interface SseHandlers {
  // 每个白名单命名帧（含终止帧 run_done）。frame.seq 供 reducer 去重。
  onEvent: (frame: SseFrame) => void
  // 终止帧 run_done 额外触发（用于隐藏 SlateBar / 成功 toast 信号）。
  onDone?: (frame: SseFrame) => void
  // 非白名单 kind（服务端以 `message` 事件流出，原 kind 仍在帧里）——仅追加日志，不改节点态。
  onMessage?: (frame: SseFrame) => void
  // 连接生命周期（SseIndicator：connected/reconnecting/disconnected）。
  onOpen?: () => void
  onError?: (err: unknown) => void
  onClose?: () => void
}

// 开 SSE 流并解析帧。client 默认 fetchEventSource，可注入 fake 供单测。
// 取消：传入 AbortController.signal，abort() 即断流。
export function streamRunEvents(
  projectId: string,
  accessToken: string,
  handlers: SseHandlers,
  client: SseClient = fetchEventSource,
  signal?: AbortSignal,
  planId?: string,
): Promise<void> {
  const url = planId
    ? `/api/projects/${projectId}/events/stream?planId=${encodeURIComponent(planId)}`
    : `/api/projects/${projectId}/events/stream`
  return client(url, {
    method: "GET",
    headers: { Authorization: `Bearer ${accessToken}` },
    // 标签页隐藏时仍保持连接（默认会断流）。
    openWhenHidden: true,
    signal,
    async onopen() {
      handlers.onOpen?.()
    },
    onmessage(ev) {
      const frame = JSON.parse(ev.data) as SseFrame
      if (NAMED_EVENTS.has(ev.event)) {
        handlers.onEvent(frame)
        if (ev.event === TERMINAL_EVENT) handlers.onDone?.(frame)
      } else {
        // ev.event === "message"（或任何非白名单事件）——兜底日志，原 kind 在 frame.kind。
        handlers.onMessage?.(frame)
      }
    },
    onerror(err) {
      handlers.onError?.(err)
    },
    onclose() {
      handlers.onClose?.()
    },
  })
}
