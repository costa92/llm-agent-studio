import type { RunNodeStatus } from "./runOverlay"

// 运行模式画布选中态：点节点 → 看其工件（剧本/分镜抽屉 或 资产预览 或自定义文本产物）。
export type RunSelection =
  | { kind: "script"; todoId?: string }
  | { kind: "storyboard"; todoId?: string }
  | { kind: "asset"; assetId: string }
  | { kind: "custom"; output: string; outputFormat?: "text" | "json" | "http-status" }
  | null

// 纯函数：节点类型 + overlay map 命中项 → 选中态。
//   script/storyboard → 携 todoId（按节点级工件精确拉取）。
//   asset 或带 assetId → 资产预览。
//   custom 节点有 output → 文本/JSON 块预览（T3 minimal）。
//   未命中（entry undefined，即 pending/未匹配节点）→ null（点击无操作）。
export function resolveSelection(
  nodeType: string,
  entry: RunNodeStatus | undefined,
): RunSelection {
  if (!entry) return null
  if (nodeType === "script") return { kind: "script", todoId: entry.todoId }
  if (nodeType === "storyboard") return { kind: "storyboard", todoId: entry.todoId }
  if (entry.assetId) return { kind: "asset", assetId: entry.assetId }
  if (entry.output) return { kind: "custom", output: entry.output, outputFormat: entry.outputFormat }
  return null
}
