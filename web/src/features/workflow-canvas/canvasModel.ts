import type { Node, Edge } from "@xyflow/react"
import { autoLayout } from "@/lib/autoLayout"
import type { Prompt, WorkflowNode } from "@/lib/types"
import type { RunNodeStatus } from "./runOverlay"
import { isCustomType, nodeDisplay } from "./nodeColor"
import type { NodeTiming } from "@/features/workflow/useNodeTiming"

// 纯模型适配层（无 React）：把 studio 工作流 DAG（WorkflowNode[]）转成 ReactFlow
// 的 nodes/edges。节点缺省 position 时用 autoLayout 分层种子坐标兜底。

// 自定义 studio 节点的 data 形状：透传原始 WorkflowNode 供节点组件渲染。
// run 可选：仅运行模式注入（见 runOverlay.overlayRunStatus）；存在时节点渲染运行态指示器。
export interface StudioNodeData {
  node: WorkflowNode
  run?: RunNodeStatus
  // 运行态拓扑增强：实时耗时（仅实时观测到的节点有值）+ 失败高亮开关。
  timing?: NodeTiming
  highlightFailed?: boolean
  [key: string]: unknown
}

export type RFNode = Node<StudioNodeData, "studio">
export type RFEdge = Edge

// 种子布局：节点无显式 position 时的兜底坐标（TB，等价历史实现，委托 autoLayout）。
export function seedPositions(
  nodes: WorkflowNode[],
): Map<string, { x: number; y: number }> {
  return autoLayout(nodes, "TB")
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

// 某节点的「直接上游」id 集合：以它为 target 的边的 source 集合（不做传递闭包）。
// EDGES 是 dependsOn 的唯一真源——这条规则被 toStudioNodes（反向转换）和属性面板的
// 变量绑定上游下拉（WorkflowCanvas.upstreamNodes）共用，保证两处口径完全一致。
// 直接上游 ONLY：worker 表达式引擎只对节点的直接 depends_on 解析 $node，提供传递上游
// 会绑定到 worker 运行时拒绝的来源，故这里不能做图遍历。
export function directUpstreamIds(rfEdges: RFEdge[], nodeId: string): string[] {
  return rfEdges.filter((e) => e.target === nodeId).map((e) => e.source)
}

// 反向转换：RF 状态 → studio 工作流模型（保存载荷）。
// 单一真源约定：EDGES 是 dependsOn 的唯一真源——每个节点的 dependsOn 仅由
// 「以它为 target 的边的 source 集合」推导（见 directUpstreamIds），不读 data.node.dependsOn。
// 这样连线/断线/重命名级联只需维护边，不必双写 dependsOn。
// 其余可编辑字段（id/type/promptId/promptText）取自 rfNode.data.node；
// position 取自 live 的 rfNode.position（四舍五入为整数）。
export function toStudioNodes(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
): WorkflowNode[] {
  return rfNodes.map((rf) => {
    const n = rf.data.node
    const dependsOn = directUpstreamIds(rfEdges, rf.id)
    // preserve-unknown: spread the whole source node first so未识别字段（含未来
    // Property，如 parameters/typeVersion）随 disk JSON 往返存活（B-A1）。再显式
    // 覆盖三条不变量：id 取 RF（重命名级联权威）、dependsOn 由边推导（单一真源）、
    // position 取 live 坐标。
    const out: WorkflowNode = {
      ...n,
      id: rf.id,
      dependsOn,
      position: {
        x: Math.round(rf.position.x),
        y: Math.round(rf.position.y),
      },
    }
    // promptText 为空则省略（与既有保存载荷一致）；非空照旧透传。
    if (!out.promptText) delete out.promptText
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
// display 可选：自定义节点的 label/color/typeId 透传到 WorkflowNode。
// typeId 非空时写入节点（typed 可运行节点）；缺失则为 annotation（Phase 1 草图节点）。
export function addNodeAt(
  rfNodes: RFNode[],
  type: string,
  pos: { x: number; y: number },
  prompts?: Prompt[],
  id?: string,
  display?: { label?: string; color?: string; typeId?: string },
): RFNode[] {
  const nodeId = id ?? nextNodeId(rfNodes)
  const node: WorkflowNode = {
    id: nodeId,
    type,
    promptId: defaultPromptIdFor(prompts, type),
    promptText: "",
    dependsOn: [],
    position: pos,
    ...(display?.label ? { label: display.label } : {}),
    ...(display?.color ? { color: display.color } : {}),
    ...(display?.typeId ? { typeId: display.typeId } : {}),
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

// 复制选区（纯函数，C2）：把 selectedIds 圈选的节点/内部边整体克隆出一份。
// id 分配：先一次性建好 oldId→newId 映射，逐个对照「现有 id（existing） + 本批已分配 id」
// 取下一个空位，保证与画布现有节点 AND 同批其它克隆都不冲突；再据此重键边。
// - 节点：新 id，data.node 深拷贝 + data.node.id 同步，position 偏移 offset，selected:true。
// - 边：仅保留 source 与 target 都在 selectedIds 内的内部边，重键 source/target/id；type:"studio"。
//       触及非选中节点的边（如外部入边 X->A）一律丢弃。
// 返回克隆出的 nodes/edges（不与原图合并，由调用方合并）。existing 为分配 id 时需避让的
// 现有画布节点（粘贴时传当前 getNodes()，避免与画布既有 id 撞）。
export function cloneSelection(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
  selectedIds: Set<string>,
  offset: { x: number; y: number },
  _prompts?: Prompt[],
  existing?: RFNode[],
): { nodes: RFNode[]; edges: RFEdge[] } {
  const selected = rfNodes.filter((n) => selectedIds.has(n.id))
  // 先建 oldId→newId 映射：working 集合从「现有画布 id + 选区原 id」起步，
  // 每分配一个新 id 就并入 working，使后续分配既避让现有也避让本批已分配。
  const working: RFNode[] = [...(existing ?? []), ...selected]
  const idMap = new Map<string, string>()
  for (const n of selected) {
    const newId = nextNodeId(working)
    idMap.set(n.id, newId)
    // 把刚分配的 id 占位进 working，下一轮 nextNodeId 不会重复给出。
    working.push({
      id: newId,
      type: "studio",
      position: { x: 0, y: 0 },
      data: { node: { id: newId, type: n.data.node.type, promptId: "", dependsOn: [] } },
    })
  }
  const nodes: RFNode[] = selected.map((n) => {
    const newId = idMap.get(n.id)!
    return {
      id: newId,
      type: "studio",
      position: { x: n.position.x + offset.x, y: n.position.y + offset.y },
      selected: true,
      data: { node: { ...n.data.node, id: newId } },
    }
  })
  const edges: RFEdge[] = rfEdges
    .filter((e) => selectedIds.has(e.source) && selectedIds.has(e.target))
    .map((e) => {
      const source = idMap.get(e.source)!
      const target = idMap.get(e.target)!
      return { id: `${source}->${target}`, source, target, type: "studio" }
    })
  return { nodes, edges }
}

// 对齐辅助线（纯函数，C3）：把被拖节点的 left/center/right(x)、top/center/bottom(y)
// 与其它节点对应边比较；落在阈值内则返回引导线坐标（flow 坐标）+ 吸附后位置。
// 几何参考 ReactFlow「helper lines」示例：节点宽高取 width/height（measured 兜底），
// 缺省回退到 180×64。snapX/snapY 是为了让被拖节点边/中心与对齐目标精确重合所需的左上角坐标。
const DEFAULT_NODE_W = 180
const DEFAULT_NODE_H = 64

function nodeSize(n: RFNode): { w: number; h: number } {
  const anyN = n as RFNode & {
    width?: number
    height?: number
    measured?: { width?: number; height?: number }
  }
  return {
    w: anyN.width ?? anyN.measured?.width ?? DEFAULT_NODE_W,
    h: anyN.height ?? anyN.measured?.height ?? DEFAULT_NODE_H,
  }
}

export function getHelperLines(
  dragged: RFNode,
  nodes: RFNode[],
  threshold = 5,
): { horizontal?: number; vertical?: number; snapX?: number; snapY?: number } {
  const { w: dw, h: dh } = nodeSize(dragged)
  const dx = dragged.position.x
  const dy = dragged.position.y
  // 被拖节点在 x/y 轴上的三条参考线：左/中/右、上/中/下。
  const dragX = [
    { line: dx, snap: dx }, // left
    { line: dx + dw / 2, snap: dx }, // center
    { line: dx + dw, snap: dx }, // right
  ]
  const dragY = [
    { line: dy, snap: dy },
    { line: dy + dh / 2, snap: dy },
    { line: dy + dh, snap: dy },
  ]
  let vertical: number | undefined
  let snapX: number | undefined
  let horizontal: number | undefined
  let snapY: number | undefined
  let bestDx = threshold
  let bestDy = threshold
  for (const other of nodes) {
    if (other.id === dragged.id) continue
    const { w: ow, h: oh } = nodeSize(other)
    const ox = other.position.x
    const oy = other.position.y
    const otherX = [ox, ox + ow / 2, ox + ow]
    const otherY = [oy, oy + oh / 2, oy + oh]
    for (const d of dragX) {
      for (const t of otherX) {
        const dist = Math.abs(d.line - t)
        if (dist < bestDx) {
          bestDx = dist
          vertical = t
          // 吸附：使被拖参考线（line）与目标线 t 重合，左上角随之平移。
          snapX = d.snap + (t - d.line)
        }
      }
    }
    for (const d of dragY) {
      for (const t of otherY) {
        const dist = Math.abs(d.line - t)
        if (dist < bestDy) {
          bestDy = dist
          horizontal = t
          snapY = d.snap + (t - d.line)
        }
      }
    }
  }
  return { horizontal, vertical, snapX, snapY }
}

// 在边 A->B 上插入一个新节点 N（纯函数）：移除 A->B，新增 A->N 与 N->B。
// 新节点经 nextNodeId 分配 id，落在 midPos；promptId 取该 type org 默认。
// 边带 type:"studio"（与所有连边构造点一致）。
// display 可选：自定义节点的 label/color/typeId 透传到 WorkflowNode。
export function insertNodeOnEdge(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
  edgeId: string,
  type: string,
  midPos: { x: number; y: number },
  prompts?: Prompt[],
  display?: { label?: string; color?: string; typeId?: string },
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
    ...(display?.label ? { label: display.label } : {}),
    ...(display?.color ? { color: display.color } : {}),
    ...(display?.typeId ? { typeId: display.typeId } : {}),
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

// 建节点（Phase D，泛化 onConnectEnd/边插入的「建点」语义）：
// 有 source → 同时连 source→新节点；无 source → 仅建点。复用 addNodeAt + nextNodeId。
// display 可选：自定义节点的 label/color/typeId 透传到 WorkflowNode。
export function createNode(
  rfNodes: RFNode[],
  rfEdges: RFEdge[],
  type: string,
  pos: { x: number; y: number },
  prompts?: Prompt[],
  source?: string,
  display?: { label?: string; color?: string; typeId?: string },
): { nodes: RFNode[]; edges: RFEdge[]; newId: string } {
  const newId = nextNodeId(rfNodes)
  const nodes = addNodeAt(rfNodes, type, pos, prompts, newId, display)
  const edges = source
    ? [
        ...rfEdges,
        { id: `${source}->${newId}`, source, target: newId, type: "studio" },
      ]
    : rfEdges
  return { nodes, edges, newId }
}

// 本工作流的自定义类型登记表：从画布上的 custom: 节点按 type 去重（label/color 取 nodeDisplay）。
export function collectCustomTypes(
  rfNodes: RFNode[],
): { type: string; label: string; color: string }[] {
  const seen = new Map<string, { type: string; label: string; color: string }>()
  for (const n of rfNodes) {
    const t = n.data.node.type
    if (isCustomType(t) && !seen.has(t)) {
      const d = nodeDisplay(n.data.node)
      seen.set(t, { type: t, label: d.label, color: d.color })
    }
  }
  return [...seen.values()]
}

// 改名/改色级联：把同 type 的所有节点的 label/color 批量更新（纯函数）。
export function applyTypeDisplay(
  rfNodes: RFNode[],
  type: string,
  label: string,
  color: string,
): RFNode[] {
  return rfNodes.map((n) =>
    n.data.node.type === type
      ? { ...n, data: { ...n.data, node: { ...n.data.node, label, color } } }
      : n,
  )
}

// 画布是否含未绑定 (annotation) 自定义节点（用于禁运行；typed 节点放行）。
export function hasUnboundCustomNode(rfNodes: RFNode[]): boolean {
  return rfNodes.some((n) => isCustomType(n.data.node.type) && !n.data.node.typeId)
}

// 连线重连（Phase D）：移除旧边、按新 source/target 追加重键后的边，其余边不动。
// 纯函数：环检测由调用方用 toStudioNodes(...)+findGraphError 在提交前做。
// 同时去掉与新边 id 重复的既有边（重连到已连节点时避免重复 id / 重复 dependsOn）。
export function reconnectEdge(
  rfEdges: RFEdge[],
  oldEdgeId: string,
  conn: { source: string; target: string },
): RFEdge[] {
  const newId = `${conn.source}->${conn.target}`
  return [
    ...rfEdges.filter((e) => e.id !== oldEdgeId && e.id !== newId),
    {
      id: newId,
      source: conn.source,
      target: conn.target,
      type: "studio",
    },
  ]
}
