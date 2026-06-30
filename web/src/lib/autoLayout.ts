import { layerize } from "@/lib/graphLayout"
import type { GraphNode, GraphEdge } from "@/lib/projectState"
import type { WorkflowNode } from "@/lib/types"

export type LayoutDirection = "TB" | "LR"

const PRIMARY = 140 // 层间距（主轴）
const CROSS = 240 // 层内间距（交叉轴）

// 自动分层布局：复用 layerize（边由 dependsOn 建 GraphEdge{from:id,to:dep}）。
// TB：layer i 第 j 个 → {x: j*CROSS, y: i*PRIMARY}（自顶向下，等价旧 seedPositions）。
// LR：交换主/交叉轴 → layer 沿 x 推进、层内沿 y 展开。
export function autoLayout(
  nodes: WorkflowNode[],
  direction: LayoutDirection,
): Map<string, { x: number; y: number }> {
  const graphNodes: GraphNode[] = nodes.map((n) => ({
    id: n.id,
    label: n.id,
    type: n.type,
    status: "pending",
  }))
  const edges: GraphEdge[] = []
  for (const n of nodes) {
    for (const dep of n.dependsOn) {
      edges.push({ from: n.id, to: dep })
    }
  }
  const layers = layerize(graphNodes, edges)
  const out = new Map<string, { x: number; y: number }>()
  layers.forEach((layer, layerIndex) => {
    layer.forEach((gn, indexWithinLayer) => {
      if (direction === "TB") {
        out.set(gn.id, { x: indexWithinLayer * CROSS, y: layerIndex * PRIMARY })
      } else {
        out.set(gn.id, { x: layerIndex * CROSS, y: indexWithinLayer * PRIMARY })
      }
    })
  })
  return out
}
