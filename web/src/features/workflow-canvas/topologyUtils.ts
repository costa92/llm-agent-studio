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

// 边「执行前沿」：源已完成、目标进行中。
export function computeEdgeActive(
  sourceStatus: GraphNodeStatus | undefined,
  targetStatus: GraphNodeStatus | undefined,
): boolean {
  return sourceStatus === "done" && targetStatus === "running"
}
