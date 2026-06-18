import type { ProjectState } from "@/lib/projectState"
import type { StudioBadgeProps } from "@/components/studio/Badge"

// BadgeVariant 直接从 Badge props 类型推导，确保编译期对齐。
type BadgeVariant = NonNullable<StudioBadgeProps["variant"]>

export interface RunSummaryData {
  runLabel: string
  variant: BadgeVariant
  stagesDone: number
  stagesTotal: number
  assetsDone: number
  assetsTotal: number
  ratio: number
}

// runStatus + 终止态 → 概览文案。
// 失败/取消态优先判断（避免 runStatus=done 时误显「已完成」）。
// statusVariant 接受 ProjectStatus（7 种），但 RunStatus2 与 ProjectStatus 字符串域
// 不完全相同（RunStatus2="idle"|"running"|"done"；无 "completed" 等），
// 故 running/done 情形直接给 literal Badge variant 而非经 statusVariant 转换。
function runLabel(state: ProjectState): { label: string; variant: BadgeVariant } {
  if (state.status === "failed") return { label: "失败", variant: "rejected" }
  if (state.status === "canceled") return { label: "已取消", variant: "rejected" }
  if (state.runStatus === "running") return { label: "生产中", variant: "running" }
  if (state.runStatus === "done") return { label: "已完成", variant: "done" }
  // idle：用项目状态（draft/planning/review 等）的着色，通过 statusVariant 映射。
  // 注：statusVariant 类型要求 ProjectStatus；此处 state.status 已是 ProjectStatus。
  // import 保持最小：直接把 draft/review 映射到已知 variant，避免引入无关依赖。
  // idle 下 status 通常为 draft，badge 用 pending 色。
  return { label: "空闲", variant: "pending" }
}

// 阶段进度：固定管线用 stages；isCustom 工作流用图节点计数。
// 判别依据：state.isCustom。
// 注意：后端对「有 plan 的自定义工作流」会同时下发 stages 与 nodes，
// 故必须按 isCustom 判别，不能用 stages.length。
// GraphNode.status 与 StageStatus2 同域（"done"|"blocked"|"pending"|"running"|"failed"）。
export function computeRunSummary(state: ProjectState): RunSummaryData {
  // 自定义工作流（isCustom）用图节点计数；固定管线用 5 段 stages。
  // 注意：后端对「有 plan 的自定义工作流」会同时下发 stages 与 nodes，
  // 故必须按 isCustom 判别，不能用 stages.length。
  const units = state.isCustom
    ? state.nodes.map((n) => n.status)
    : state.stages.map((s) => s.status)
  const stagesTotal = units.length
  const stagesDone = units.filter((s) => s === "done").length
  const ratio = stagesTotal === 0 ? 0 : stagesDone / stagesTotal
  const { label, variant } = runLabel(state)
  return {
    runLabel: label,
    variant,
    stagesDone,
    stagesTotal,
    assetsDone: state.assets.done,
    assetsTotal: state.assets.total,
    ratio,
  }
}
