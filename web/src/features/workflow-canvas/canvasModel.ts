import type { Node, Edge } from "@xyflow/react"
import { layerize } from "@/lib/graphLayout"
import type { GraphNode, GraphEdge } from "@/lib/projectState"
import type { Prompt, WorkflowNode } from "@/lib/types"

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

// 主转换：每个 WorkflowNode → RFNode（type="studio"，data.node 透传原节点）；
// 每个 dep ∈ n.dependsOn → 边 {id: `${dep}->${n.id}`, source: dep, target: n.id}
//（source = 上游依赖，target = 依赖它的节点）。
export function toReactFlow(nodes: WorkflowNode[]): {
  nodes: RFNode[]
  edges: RFEdge[]
} {
  const seeded = seedPositions(nodes)
  const rfNodes: RFNode[] = nodes.map((n) => ({
    id: n.id,
    type: "studio",
    position: n.position ?? seeded.get(n.id)!,
    data: { node: n },
  }))
  const rfEdges: RFEdge[] = []
  for (const n of nodes) {
    for (const dep of n.dependsOn) {
      rfEdges.push({
        id: `${dep}->${n.id}`,
        source: dep,
        target: n.id,
        type: "studio",
      })
    }
  }
  return { nodes: rfNodes, edges: rfEdges }
}

// 反向转换：RF 状态 → studio 工作流模型（保存载荷）。
// 单一真源约定：EDGES 是 dependsOn 的唯一真源——每个节点的 dependsOn 仅由
// 「以它为 target 的边的 source 集合」推导，不读 data.node.dependsOn。
// 这样连线/断线/重命名级联只需维护边，不必双写 dependsOn。
// 其余可编辑字段（id/type/promptId/promptText）取自 rfNode.data.node；
// position 取自 live 的 rfNode.position（四舍五入为整数）。
export function toStudioNodes(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
): WorkflowNode[] {
  return rfNodes.map((rf) => {
    const n = rf.data.node
    const dependsOn = rfEdges
      .filter((e) => e.target === rf.id)
      .map((e) => e.source)
    const out: WorkflowNode = {
      id: rf.id,
      type: n.type,
      promptId: n.promptId,
      dependsOn,
      position: {
        x: Math.round(rf.position.x),
        y: Math.round(rf.position.y),
      },
    }
    // promptText 为空则省略（与既有保存载荷一致）。
    if (n.promptText) out.promptText = n.promptText
    return out
  })
}

// 某 kind 的 org 默认提示词 id（无则空串）。从 WorkflowNodesEditor 原样移植。
export function defaultPromptIdFor(
  prompts: Prompt[] | undefined,
  kind: string,
): string {
  return prompts?.find((p) => p.kind === kind && p.isDefault)?.id ?? ""
}

// 标准管线：脚本 → 分镜（storyboard 完成后 worker 自动为每个镜头扇出图片任务）。
// 与旧 WorkflowNodesEditor.fillStandardPipeline 的形状逐字一致：
// script-1（dependsOn []）→ storyboard-1（dependsOn ["script-1"]），promptId 取 org 默认。
// 返回纯 WorkflowNode[]（含种子坐标），交由 toReactFlow 转 RF 状态。
export function standardPipeline(prompts?: Prompt[]): WorkflowNode[] {
  const base: WorkflowNode[] = [
    {
      id: "script-1",
      type: "script",
      promptId: defaultPromptIdFor(prompts, "script"),
      dependsOn: [],
    },
    {
      id: "storyboard-1",
      type: "storyboard",
      promptId: defaultPromptIdFor(prompts, "storyboard"),
      dependsOn: ["script-1"],
    },
  ]
  const seeded = seedPositions(base)
  return base.map((n) => ({ ...n, position: seeded.get(n.id)! }))
}

// 生成一个不与现有 rfNodes 冲突的新 id（形如 `node-${n}`，从 1 起递增）。
// 纯函数：供 addNodeAt / duplicateNode / insertNodeOnEdge 共用。
export function nextNodeId(rfNodes: RFNode[]): string {
  const existing = new Set(rfNodes.map((n) => n.id))
  let i = 1
  let id = `node-${i}`
  while (existing.has(id)) {
    i += 1
    id = `node-${i}`
  }
  return id
}

// 在 pos 处追加一个新节点（纯 reducer，避免 DnD 事件测试抖动）。
// 默认 id 由 nextNodeId 生成；调用方可传 id 覆盖（B2 拖到空白后需与新建的边对齐）。
// promptId 取该 type 的 org 默认提示词，无则空串。
export function addNodeAt(
  rfNodes: RFNode[],
  type: string,
  pos: { x: number; y: number },
  prompts?: Prompt[],
  id?: string,
): RFNode[] {
  const nodeId = id ?? nextNodeId(rfNodes)
  const node: WorkflowNode = {
    id: nodeId,
    type,
    promptId: defaultPromptIdFor(prompts, type),
    promptText: "",
    dependsOn: [],
    position: pos,
  }
  return [
    ...rfNodes,
    { id: nodeId, type: "studio", position: pos, data: { node } },
  ]
}

// 复制节点（纯函数）：克隆 data.node 为新对象，分配新 id（nextNodeId），
// data.node.id 同步为新 id，position 偏移 +{40,40}。dependsOn 置空——
// 副本作为未连接的孤立节点（边是 dependsOn 真源，不复制任何边）。
export function duplicateNode(
  rfNodes: RFNode[],
  id: string,
  _prompts?: Prompt[],
): { nodes: RFNode[]; id: string } {
  const src = rfNodes.find((n) => n.id === id)
  if (!src) return { nodes: rfNodes, id }
  const newId = nextNodeId(rfNodes)
  const pos = { x: src.position.x + 40, y: src.position.y + 40 }
  const clonedNode: WorkflowNode = {
    ...src.data.node,
    id: newId,
    dependsOn: [],
    position: pos,
  }
  const newRf: RFNode = {
    id: newId,
    type: "studio",
    position: pos,
    data: { node: clonedNode },
  }
  return { nodes: [...rfNodes, newRf], id: newId }
}

// 在边 A->B 上插入一个新节点 N（纯函数）：移除 A->B，新增 A->N 与 N->B。
// 新节点经 nextNodeId 分配 id，落在 midPos；promptId 取该 type org 默认。
// 边带 type:"studio"（与所有连边构造点一致）。
export function insertNodeOnEdge(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
  edgeId: string,
  type: string,
  midPos: { x: number; y: number },
  prompts?: Prompt[],
): { nodes: RFNode[]; edges: RFEdge[]; newId: string } {
  const edge = rfEdges.find((e) => e.id === edgeId)
  if (!edge) return { nodes: rfNodes, edges: rfEdges, newId: "" }
  const newId = nextNodeId(rfNodes)
  const node: WorkflowNode = {
    id: newId,
    type,
    promptId: defaultPromptIdFor(prompts, type),
    promptText: "",
    dependsOn: [],
    position: midPos,
  }
  const nodes: RFNode[] = [
    ...rfNodes,
    { id: newId, type: "studio", position: midPos, data: { node } },
  ]
  const edges: RFEdge[] = [
    ...rfEdges.filter((e) => e.id !== edgeId),
    {
      id: `${edge.source}->${newId}`,
      source: edge.source,
      target: newId,
      type: "studio",
    },
    {
      id: `${newId}->${edge.target}`,
      source: newId,
      target: edge.target,
      type: "studio",
    },
  ]
  return { nodes, edges, newId }
}
