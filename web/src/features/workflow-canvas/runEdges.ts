import type { RunNodeStatus } from "./runOverlay"
import type { RFEdge } from "./canvasModel"

// 运行态「活动边」标记（纯函数，无 React）：数据正从已完成的上游流向运行中的下游时，
// 该边为活动态（渲琥珀描边 + 流动粒子，见 RunEdge）。
//
// 活动判定 = 上游 source.status==="done" 且下游 target.status==="running"。
// overlay 是画布节点 id → run 状态的唯一源（见 runOverlay）。未命中（pending/未匹配）的
// 端点状态为 undefined → 非活动。enabled=false（顶栏「流动效果」关）→ 全部非活动。
export function markActiveEdges(
  edges: RFEdge[],
  overlay: Map<string, RunNodeStatus>,
  enabled = true,
): RFEdge[] {
  return edges.map((e) => {
    const active =
      enabled &&
      overlay.get(e.source)?.status === "done" &&
      overlay.get(e.target)?.status === "running"
    return { ...e, data: { ...e.data, active } }
  })
}
