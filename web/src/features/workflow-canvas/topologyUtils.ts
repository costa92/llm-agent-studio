import type { GraphNodeStatus } from "@/lib/projectState"
import type { TopologySettings } from "./useTopologySettings"

// 节点可见性：hideCompleted 隐藏 done；focus 把非聚焦目标降透明。
export function computeNodeVisibility(
  status: GraphNodeStatus,
  s: TopologySettings,
): { hidden: boolean; dimmed: boolean } {
  const hidden = s.hideCompleted && status === "done"
  let dimmed = false
  if (s.focus === "failed") dimmed = status !== "failed"
  else if (s.focus === "running") dimmed = status !== "running"
  return { hidden, dimmed }
}

// 注：边「执行前沿」active 判定已统一到 runEdges.markActiveEdges（与原 computeEdgeActive
// 等价：源 done & 目标 running），运行态边走 RunEdge 渲流动粒子。此处不再重复导出。
