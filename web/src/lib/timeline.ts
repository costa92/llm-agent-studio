// 事件 → 日志文案（左栏事件日志）。本里程碑后：状态推导已移至后端 projectstate.Compute，
// 前端经 useProjectState 拿权威 ProjectState 渲染。本文件只剩"事件流 → 人类可读日志行"
// 这一纯表现职责 + 阶段着色映射。
import type { SseFrame } from "./types"

// 阶段着色 id（供日志 emphasis；与 ProjectState 的语义 role 一一对应：
// planner→S1 script→S2 storyboard→S3 asset→S4 review→S5）。纯前端表现。
export type StageId = "S1" | "S2" | "S3" | "S4" | "S5"

// EventLog 行（左栏日志）。emphasis = 阶段标签供着色。
export interface LogLine {
  seq: number
  kind: string
  text: string
  emphasis?: StageId
}

// todo 的 type（payload.type）→ 阶段着色 id。
const TYPE_TO_STAGE: Record<string, StageId> = {
  script: "S2",
  storyboard: "S3",
}

function payloadType(frame: SseFrame): string | undefined {
  const p = frame.payload
  if (p && typeof p === "object" && "type" in p) {
    const t = (p as { type?: unknown }).type
    if (typeof t === "string") return t
  }
  return undefined
}

function payloadStr(frame: SseFrame, key: string): string | undefined {
  const p = frame.payload
  if (p && typeof p === "object" && key in p) {
    const v = (p as Record<string, unknown>)[key]
    if (typeof v === "string") return v
  }
  return undefined
}

function emphasisFor(t: string | undefined): StageId | undefined {
  if (!t) return undefined
  return TYPE_TO_STAGE[t] ?? (t === "asset" ? "S4" : undefined)
}

// 单帧 → 日志行（纯表现：状态正确性已由后端 ProjectState 保证）。
export function logFor(frame: SseFrame): LogLine {
  const t = payloadType(frame)
  let text: string
  let emphasis: StageId | undefined
  switch (frame.kind) {
    case "planner_started":
      text = "规划开始"
      emphasis = "S1"
      break
    case "todo_ready":
      text = `todo_ready（${t ?? "?"}）`
      emphasis = emphasisFor(t)
      break
    case "todo_started":
      text = `开始：${t ?? frame.todoId}`
      emphasis = emphasisFor(t)
      break
    case "todo_finished":
      text = `完成：${t ?? frame.todoId}`
      emphasis = emphasisFor(t)
      break
    case "asset_generated":
      text = "asset_generated · 待审"
      emphasis = "S4"
      break
    case "asset_submitted":
      text = "asset_submitted · 已提交"
      emphasis = "S4"
      break
    case "asset_prescreened":
      text = "asset_prescreened · 预筛"
      emphasis = "S4"
      break
    case "todo_failed": {
      // 后端现已在 payload 带 {type,error}（见 worker.go fail/terminalFail）。
      const err = payloadStr(frame, "error")
      text = err ? `失败：${err}` : `失败：${frame.todoId} · 退避重试`
      emphasis = emphasisFor(t)
      break
    }
    case "run_done":
      text = "运行结束"
      break
    default:
      text = frame.kind
      break
  }
  return { seq: frame.seq, kind: frame.kind, text, emphasis }
}

// 累积日志，按 seq 去重（替代 Last-Event-ID：重连全量回放的旧帧在此被吞掉，幂等）。
export function foldLog(log: LogLine[], frames: SseFrame[]): LogLine[] {
  const seen = new Set(log.map((l) => l.seq))
  const next = [...log]
  for (const f of frames) {
    if (seen.has(f.seq)) continue
    seen.add(f.seq)
    next.push(logFor(f))
  }
  return next
}
