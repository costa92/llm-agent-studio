import type { GraphNodeStatus } from "@/lib/projectState"

// 运行状态 → 语义色 token（运行态画布的状态条 / Run Matrix / 子卡共用）。
// ⚠️ done 用 --review（绿）而非 --asset：项目里 --asset === --amber（同一 hex），
// 若 done 也用 --asset，则 done 与 running 在状态条里同色不可分。故 done→review。
export const STATUS_VAR: Record<GraphNodeStatus, string> = {
  done: "var(--review)",
  running: "var(--amber)",
  failed: "var(--danger)",
  pending: "var(--line)",
  blocked: "var(--line)",
}

// 运行态 minimap 节点色：按 run 状态着色（done 绿 / running 琥珀 / failed 红 / pending·blocked 线灰）。
// 对齐原型 minimap「导航 · 按状态着色」。运行态每节点都有 overlay 状态；理论兜底 undefined → 当 pending（灰）。
export function minimapStatusColor(status: GraphNodeStatus | undefined): string {
  return STATUS_VAR[status ?? "pending"]
}

// 运行状态 → 中文标签（图例 / tooltip）。
export const STATUS_LABEL: Record<GraphNodeStatus, string> = {
  done: "完成",
  running: "运行中",
  failed: "失败",
  pending: "待运行",
  blocked: "阻塞",
}
