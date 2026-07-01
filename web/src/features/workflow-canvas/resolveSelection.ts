import type { RunNodeStatus } from "./runOverlay"
import type { InspectorItem } from "@/lib/projectState"

// 运行模式画布选中态：点节点 → 看其工件（剧本/分镜抽屉 或 资产预览 或自定义文本产物）。
// P5d：每个选中态都可携 items[]（node_outputs.items 逐字透传），供 per-item inspector
// 渲染；items 缺省（老后端 / 标量节点）→ 字段不出现，调用方回落今天的标量面板。
// OQ1 决策 = 所有节点类型都带 items（内建 script/storyboard/asset executor 现也产 items，
// storyboard fan-out 是头号用例）。
export type RunSelection =
  | { kind: "script"; todoId?: string; items?: InspectorItem[] }
  | { kind: "storyboard"; todoId?: string; items?: InspectorItem[] }
  | { kind: "asset"; assetId: string; items?: InspectorItem[] }
  | {
      kind: "custom"
      output: string
      outputFormat?: "text" | "json" | "http-status"
      items?: InspectorItem[]
    }
  // 选中大功能容器（有逐页扇出资产的 storyboard）→ 右栏渲 Run Matrix。
  // selectedPageKey：在矩阵里点选的某一页 key（高亮 + 下方渲该页图+音产物）。
  // 注：resolveSelection() 自身不产 group（分组在 RunCanvas.onNodeClick 里设）。
  | { kind: "group"; groupId: string; selectedPageKey?: string }
  | null

// 纯函数：节点类型 + overlay map 命中项 → 选中态。
//   script/storyboard → 携 todoId（按节点级工件精确拉取）。
//   asset 或带 assetId → 资产预览。
//   custom 节点有 output → 文本/JSON 块预览（T3 minimal）。
//   未命中（entry undefined，即 pending/未匹配节点）→ null（点击无操作）。
// items 始终从 entry 透传（缺省 undefined）；toEqual 视 undefined 字段为缺省。
export function resolveSelection(
  nodeType: string,
  entry: RunNodeStatus | undefined,
): RunSelection {
  if (!entry) return null
  if (nodeType === "script") return { kind: "script", todoId: entry.todoId, items: entry.items }
  if (nodeType === "storyboard")
    return { kind: "storyboard", todoId: entry.todoId, items: entry.items }
  if (entry.assetId) return { kind: "asset", assetId: entry.assetId, items: entry.items }
  if (entry.output)
    return { kind: "custom", output: entry.output, outputFormat: entry.outputFormat, items: entry.items }
  return null
}
