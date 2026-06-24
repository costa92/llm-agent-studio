import { layerize } from "@/lib/graphLayout"
import type { GraphNode, GraphEdge, ProjectState } from "@/lib/projectState"
import type { WorkflowNode } from "@/lib/types"

// run 状态叠加（纯函数，无 React）。
//
// ⚠️ 关键事实：run 状态节点 id（ProjectState.nodes[].id）是全新 todo UUID，
// 与工作流节点 id（如 "script-1"）**无持久回链**（todos 表无 local_id 列）。
// 因此不能按 id 叠加。解法：按 `(type, 该类型在拓扑序中的序号)` 结构映射。
// 画布侧用 seedPositions 同款 layerize 拓扑序；run 侧 state.nodes 已是后端拓扑序
// （数组序即可）。两侧由同一工作流定义建的同构 DAG、同款分层，故 (type, 序号) join 正确。

export interface RunNodeStatus {
  status: GraphNode["status"]
  assetId?: string
  todoId?: string
  // custom 节点 node_outputs 产物（T3 — minimal output panel）。
  // "http-status" = http 节点响应体被安全策略抑制，content 仅含 {"status":N}。
  output?: string
  outputFormat?: "text" | "json" | "http-status"
}

// 画布工作流节点 → 该类型在拓扑序中的序号（script:0,1…; storyboard:0,1…; asset:0,1…）。
// 构造与 seedPositions 逐字一致的 GraphEdge{from:n.id, to:dep}，调 layerize 分层后展平。
function canvasOrdinals(nodes: WorkflowNode[]): Map<string, number> {
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
  // type → 节点 id 列表（拓扑序）。
  const perType = new Map<string, string[]>()
  for (const layer of layers) {
    for (const gn of layer) {
      const list = perType.get(gn.type) ?? []
      list.push(gn.id)
      perType.set(gn.type, list)
    }
  }
  const out = new Map<string, number>()
  for (const ids of perType.values()) {
    ids.forEach((id, ordinal) => out.set(id, ordinal))
  }
  return out
}

// run 状态节点 → (type, 序号) → RunNodeStatus（state.nodes 已是后端拓扑序，按数组序算序号）。
function runByTypeOrdinal(state: ProjectState): Map<string, RunNodeStatus> {
  const counters = new Map<string, number>()
  const out = new Map<string, RunNodeStatus>()
  for (const rn of state.nodes) {
    const ordinal = counters.get(rn.type) ?? 0
    counters.set(rn.type, ordinal + 1)
    out.set(`${rn.type}#${ordinal}`, {
      status: rn.status,
      assetId: rn.assetId,
      todoId: rn.id,
      output: rn.output,
      outputFormat: rn.outputFormat,
    })
  }
  return out
}

// 主映射：画布节点 id → 该 run 节点状态。
// 未匹配（计数不齐 / 类型缺失）的画布节点从 map 中省略 —— 调用方当 pending/中性，绝不抛。
export function overlayRunStatus(
  nodes: WorkflowNode[],
  state: ProjectState,
): Map<string, RunNodeStatus> {
  const ordinals = canvasOrdinals(nodes)
  const runMap = runByTypeOrdinal(state)
  const out = new Map<string, RunNodeStatus>()
  for (const n of nodes) {
    const ordinal = ordinals.get(n.id)
    if (ordinal == null) continue
    const run = runMap.get(`${n.type}#${ordinal}`)
    if (run) out.set(n.id, run)
  }
  return out
}
