// 制片轨道状态机：把 SSE 事件流（历史回放 + 实时）归约成轨道全态。
// 本里程碑最核心的逻辑单元——纯函数 reducer (state, frame) => state。
//
// 关键不变式（计划强制 · 替代 Last-Event-ID 的安全网）：
//   服务端 sse.go 每次（重）连都从 after=0 全量回放历史事件、不发 id: 字段。
//   故 reducer 必须按 frame.seq 去重——已应用过的 seq 直接忽略（返回原 state 引用）。
//   重连后的重复回放帧被 seq-dedup 吞掉，再无缝接实时帧，绝不重复渲染。
//
// 固定阶段语义（UI-spec §6.1）：
//   S1 Planner（amber/规划态）· S2 Script（蓝）· S3 Storyboard（紫）
//   · S4 Asset×N（琥珀 pip 组）· S5 Review（绿，admin 门禁）。
//   S4 的 N = worker 每 shot 发的 todo_ready{type:asset} 数（真实后端语义——
//   todo_finished(storyboard) payload 只含 {type,outputRef}，不带 shots 数）。
import type { SseFrame } from "./types"

// ── 节点态 ──────────────────────────────────────────────────────────
export type StageId = "S1" | "S2" | "S3" | "S4" | "S5"
export type StageStatus = "blocked" | "pending" | "running" | "done" | "failed"

export interface Stage {
  id: StageId
  // 该 stage 绑定的 todo 类型（S4 是 pip 组，无单一 todoId）。
  kind: "planner" | "script" | "storyboard" | "asset" | "review"
  status: StageStatus
  // 驱动该 stage 的 todoId（S1/S5 无；S4 见 pips）。
  todoId?: string
  // 连接线着色（todo_finished 后该 stage 的连线着 agent 色）。
  linked: boolean
}

// S4 PipGroup 中的单个并行 asset pip（每 shot 一个）。
export type PipStatus = "idle" | "running" | "done" | "failed"
export interface Pip {
  todoId: string
  status: PipStatus
  // asset_generated 落地的 assetId（供右栏预览 / 审核跳转）。
  assetId?: string
}

// EventLog 行（左栏日志）。emphasis = 阶段标签（S1..S5）供着色。
export interface LogLine {
  seq: number
  kind: string
  text: string
  emphasis?: StageId
}

export type RunStatus = "idle" | "running" | "done"

export interface TimelineState {
  stages: Stage[]
  pips: Pip[]
  // N = 已知 asset pip 数（distinct asset todoId）。
  pipCount: number
  // 已完成 asset 数（done/N 计数）。
  doneAssetCount: number
  // 待审核资产数（asset_generated 累计）——run_done 后徽标「待审核 · N」。
  pendingAssetCount: number
  // SlateBar：planner_started 显示 / run_done 隐藏。
  slateVisible: boolean
  runStatus: RunStatus
  log: LogLine[]
  // 已应用的 seq 集合——seq-dedup 的安全网（重连全量回放靠它去重）。
  appliedSeqs: Set<number>
}

// 固定 5 阶段的初态：全 blocked、无连线。
export function initialTimeline(): TimelineState {
  return {
    stages: [
      { id: "S1", kind: "planner", status: "blocked", linked: false },
      { id: "S2", kind: "script", status: "blocked", linked: false },
      { id: "S3", kind: "storyboard", status: "blocked", linked: false },
      { id: "S4", kind: "asset", status: "blocked", linked: false },
      { id: "S5", kind: "review", status: "blocked", linked: false },
    ],
    pips: [],
    pipCount: 0,
    doneAssetCount: 0,
    pendingAssetCount: 0,
    slateVisible: false,
    runStatus: "idle",
    log: [],
    appliedSeqs: new Set(),
  }
}

// todo 的 type（payload.type）→ 对应阶段。
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

// 不可变更新单个 stage。
function patchStage(stages: Stage[], id: StageId, patch: Partial<Stage>): Stage[] {
  return stages.map((s) => (s.id === id ? { ...s, ...patch } : s))
}

// 不可变更新/插入单个 pip（按 todoId）。
function patchPip(pips: Pip[], todoId: string, patch: Partial<Pip>): Pip[] {
  if (!pips.some((p) => p.todoId === todoId)) {
    return [...pips, { todoId, status: "idle", ...patch }]
  }
  return pips.map((p) => (p.todoId === todoId ? { ...p, ...patch } : p))
}

// 日志文案（按 kind）。emphasis 给阶段着色。
function logFor(frame: SseFrame): LogLine {
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
      emphasis = t ? (TYPE_TO_STAGE[t] ?? (t === "asset" ? "S4" : undefined)) : undefined
      break
    case "todo_started":
      text = `开始：${t ?? frame.todoId}`
      emphasis = t ? (TYPE_TO_STAGE[t] ?? (t === "asset" ? "S4" : undefined)) : undefined
      break
    case "todo_finished":
      text = `完成：${t ?? frame.todoId}`
      emphasis = t ? (TYPE_TO_STAGE[t] ?? (t === "asset" ? "S4" : undefined)) : undefined
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
    case "todo_failed":
      text = `失败：${t ?? frame.todoId} · 退避重试`
      emphasis = t ? (TYPE_TO_STAGE[t] ?? (t === "asset" ? "S4" : undefined)) : undefined
      break
    case "run_done":
      text = "运行结束"
      break
    default:
      // message 兜底：原 kind 在 frame.kind。
      text = frame.kind
      break
  }
  return { seq: frame.seq, kind: frame.kind, text, emphasis }
}

// 全部 asset pip done → S4 done、S5 pending。
function settleAssetStage(state: TimelineState): TimelineState {
  if (state.pipCount === 0) return state
  const allDone = state.pips.length === state.pipCount && state.pips.every((p) => p.status === "done")
  if (allDone) {
    return {
      ...state,
      stages: patchStage(
        patchStage(state.stages, "S4", { status: "done", linked: true }),
        "S5",
        { status: "pending" },
      ),
    }
  }
  return state
}

// 核心 reducer：(state, frame) => state。已应用 seq 直接返回原引用（dedup）。
export function reduceTimeline(state: TimelineState, frame: SseFrame): TimelineState {
  // ── seq-dedup 安全网 ── 重连全量回放的旧帧在此被吞掉。
  if (state.appliedSeqs.has(frame.seq)) return state

  const appliedSeqs = new Set(state.appliedSeqs)
  appliedSeqs.add(frame.seq)
  // 每帧都进日志（含 message 兜底）。
  let next: TimelineState = {
    ...state,
    appliedSeqs,
    log: [...state.log, logFor(frame)],
  }

  const t = payloadType(frame)

  switch (frame.kind) {
    case "planner_started": {
      next = {
        ...next,
        slateVisible: true,
        runStatus: "running",
        stages: patchStage(next.stages, "S1", { status: "running" }),
      }
      break
    }

    case "todo_ready": {
      if (t === "asset") {
        // 每 shot 一个 asset todo → seed pip（idle）、N+1、S4 pending。
        next = {
          ...next,
          pips: patchPip(next.pips, frame.todoId, {}),
          pipCount: next.pipCount + (next.pips.some((p) => p.todoId === frame.todoId) ? 0 : 1),
          stages:
            next.stages.find((s) => s.id === "S4")?.status === "blocked"
              ? patchStage(next.stages, "S4", { status: "pending" })
              : next.stages,
        }
      } else if (t && TYPE_TO_STAGE[t]) {
        const id = TYPE_TO_STAGE[t]
        next = {
          ...next,
          stages: patchStage(next.stages, id, { status: "pending", todoId: frame.todoId }),
        }
      }
      break
    }

    case "todo_started": {
      if (t === "asset") {
        // pip → running（失败重试时也走这里，pip 从 failed 回 running）。
        next = {
          ...next,
          pips: patchPip(next.pips, frame.todoId, { status: "running" }),
          stages:
            next.stages.find((s) => s.id === "S4")?.status !== "done"
              ? patchStage(next.stages, "S4", { status: "running" })
              : next.stages,
        }
      } else if (t && TYPE_TO_STAGE[t]) {
        next = {
          ...next,
          stages: patchStage(next.stages, TYPE_TO_STAGE[t], {
            status: "running",
            todoId: frame.todoId,
          }),
        }
      }
      break
    }

    case "todo_finished": {
      if (t && TYPE_TO_STAGE[t]) {
        // script/storyboard 完成 → 该 stage done + 连接线着 agent 色。
        next = {
          ...next,
          stages: patchStage(next.stages, TYPE_TO_STAGE[t], { status: "done", linked: true }),
        }
      }
      // asset 的完成态由 asset_generated 驱动（见下），此处不处理 asset。
      break
    }

    case "asset_generated": {
      // 对应 pip → done（asset 色）；done/N +1；pendingAssetCount +1（待审）。
      next = {
        ...next,
        pips: patchPip(next.pips, frame.todoId, {
          status: "done",
          assetId: payloadStr(frame, "assetId"),
        }),
        doneAssetCount: next.doneAssetCount + 1,
        pendingAssetCount: next.pendingAssetCount + 1,
      }
      next = settleAssetStage(next)
      break
    }

    case "todo_failed": {
      // ⚠ 真实后端 todo_failed 的 payload 只含 {error:msg}，不带 type 键——
      //   与同族 todo_ready/started/finished（都带 {type:c.typ}）不一致。
      //   见 internal/worker/worker.go fail()（:713）与 pollAsync.terminalFail（:1012），
      //   两处都发 map[string]any{"error": msg}。故此处**必须按 frame.todoId 定位目标**，
      //   不能复用 payload.type（生产环境恒为 undefined，会漏掉失败态、让 stage 永久卡 running
      //   而 run_done 又把 runStatus 翻成 done，UI 自相矛盾）。
      //   注：后端 todo_failed 缺 type 是已知不一致，列为后端跟进项（本里程碑前端独立修复）。
      //   todoId 已由先行的 todo_ready/started 落在 pip.todoId / stage.todoId 上，足以定位。
      if (next.pips.some((p) => p.todoId === frame.todoId)) {
        // asset pip 失败 → pip failed（danger）；worker 重试时该 pip 由后续 todo_started 回 running。
        next = {
          ...next,
          pips: patchPip(next.pips, frame.todoId, { status: "failed" }),
        }
      } else {
        const failedStage = next.stages.find((s) => s.todoId === frame.todoId)
        if (failedStage) {
          // 阶段级失败（重试耗尽）→ 该 stage failed；后继 stage 维持 blocked。
          next = {
            ...next,
            stages: patchStage(next.stages, failedStage.id, { status: "failed" }),
          }
        }
        // 既非已知 pip 亦非已知 stage → 仅日志（上面已加），不改节点态。
      }
      break
    }

    case "asset_submitted":
    case "asset_prescreened": {
      // M4 异步提交 / M3 预筛 —— 仅日志，不改节点态、不改审核态（审核走 HITL）。
      break
    }

    case "run_done": {
      // 终止帧：SlateBar 隐藏、run done（徽标「待审核 · N」用 pendingAssetCount）。
      next = { ...next, slateVisible: false, runStatus: "done" }
      break
    }

    default: {
      // message 兜底（未在白名单的 kind）—— 仅追加日志（上面已加），不改节点态。
      break
    }
  }

  return next
}

// 批量折叠（历史回放）：把一串帧顺序喂进 reducer。
// 与逐帧 reduceTimeline 等价——seq-dedup 保证重连全量回放幂等。
export function foldEvents(state: TimelineState, frames: SseFrame[]): TimelineState {
  return frames.reduce(reduceTimeline, state)
}
