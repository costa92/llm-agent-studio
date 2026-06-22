import type { Node, Edge } from "@xyflow/react"
import { layerize } from "@/lib/graphLayout"
import type { GraphNode, GraphEdge } from "@/lib/projectState"
import type { WorkflowNode } from "@/lib/types"

// 纯模型适配层（无 React）：把 studio 工作流 DAG（WorkflowNode[]）转成 ReactFlow
// 的 nodes/edges。节点缺省 position 时用 layerize 分层种子坐标兜底。
// 复用 lib/graphLayout 的拓扑分层 + lib/projectState 的 GraphEdge{from,to} 形状。

// 自定义 studio 节点的 data 形状：透传原始 WorkflowNode 供节点组件渲染。
export interface StudioNodeData {
  node: WorkflowNode
  [key: string]: unknown
}

export type RFNode = Node<StudioNodeData, "studio">
export type RFEdge = Edge

// 种子布局：节点无显式 position 时的兜底坐标。
// 构造 throwaway GraphEdge{from: n.id, to: dep}（from 依赖 to，对齐 layerize 语义），
// 调 layerize 分层；layer i 内第 j 个节点 → {x: j*240, y: i*140}（自顶向下）。
export function seedPositions(
  nodes: WorkflowNode[],
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
      out.set(gn.id, { x: indexWithinLayer * 240, y: layerIndex * 140 })
    })
  })
  return out
}

// position 类型（WorkflowNode 当前线缆类型尚无 position 字段，Phase 1 只读不写——
// 用结构化读取兜底，未来 Phase 2 加字段时无需改本函数签名）。
interface MaybePositioned {
  position?: { x: number; y: number }
}

// 主转换：每个 WorkflowNode → RFNode（type="studio"，data.node 透传原节点）；
// 每个 dep ∈ n.dependsOn → 边 {id: `${dep}->${n.id}`, source: dep, target: n.id}
//（source = 上游依赖，target = 依赖它的节点）。
export function toReactFlow(nodes: WorkflowNode[]): {
  nodes: RFNode[]
  edges: RFEdge[]
} {
  const seeded = seedPositions(nodes)
  const rfNodes: RFNode[] = nodes.map((n) => {
    const explicit = (n as MaybePositioned).position
    return {
      id: n.id,
      type: "studio",
      position: explicit ?? seeded.get(n.id)!,
      data: { node: n },
    }
  })
  const rfEdges: RFEdge[] = []
  for (const n of nodes) {
    for (const dep of n.dependsOn) {
      rfEdges.push({ id: `${dep}->${n.id}`, source: dep, target: n.id })
    }
  }
  return { nodes: rfNodes, edges: rfEdges }
}
