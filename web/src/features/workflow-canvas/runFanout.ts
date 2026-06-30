import type { Node } from "@xyflow/react"
import type { ProjectState, GraphNodeStatus } from "@/lib/projectState"
import type { RFEdge } from "./canvasModel"

// 运行态画布扇出（纯函数，无 React）：把 storyboard 扇出的逐页 asset todo
// 展开成父分镜节点下方的独立子节点（图/音），算出 RFNode[] + 运行态可视化边 + 坐标。
//
// ⚠️ 关键事实（已实测，见 internal/projectstate/state.go:464 GraphEdge{From:t.ID, To:dep}）：
//   ProjectState.edges 的 GraphEdge{from,to} = 「from 依赖 to」。扇出的 asset todo 是 from，
//   父 storyboard 是 to。故找父：state.edges.find(e => e.from === assetNode.id)?.to。
// 仅运行态可视化：不回写 dependsOn、不进保存载荷。

export type AssetKind = "image" | "audio" | "video" | "unknown"

export interface AssetRunNodeData {
  todoId: string
  assetId?: string
  status: GraphNodeStatus
  kind: AssetKind
  pageOrdinal: number
  [k: string]: unknown
}

export type AssetRunRFNode = Node<AssetRunNodeData, "assetRun">

// 父分镜锚点：run 节点 todoId（用于 edge.to 匹配）+ 画布节点 id + 坐标（子节点居中布局基准）。
export interface ParentAnchor {
  todoId: string
  canvasNodeId: string
  x: number
  y: number
}

export interface FanoutLayoutOpts {
  childW?: number
  gapX?: number
  gapY?: number
  // 每行最多子节点数：超过则换行成网格，避免一条长横排溢出视口。
  maxPerRow?: number
  // 网格行间距（子节点高度 + 行距）。
  rowGapY?: number
}

const DEFAULT_CHILD_W = 120
const DEFAULT_GAP_X = 24
const DEFAULT_GAP_Y = 140
const DEFAULT_MAX_PER_ROW = 6
const DEFAULT_ROW_GAP_Y = 132

// 主映射：ProjectState（含 asset run 节点 + edges）+ 父分镜锚点 + 资产 kind map
//   → asset 子节点 RFNode[] + 运行态可视化边 RFEdge[] + 坐标。
// 逻辑：
//   ① parentByTodo: 父锚点按 todoId 索引；
//   ② assetNodes: state.nodes 里 type==="asset"（保持 state.nodes 原序 = 后端拓扑序）；
//   ③ 每 asset：parentTodoId = edges.find(e => e.from === n.id)?.to，parent = parentByTodo.get(parentTodoId)；
//      无父 → skip（孤儿，不抛）；
//   ④ 按 parent.canvasNodeId 分组，组内序 = state.nodes 序 → pageOrdinal = idx+1；
//   ⑤ 居中布局：组内第 i / 共 c，x = parent.x + (i - (c-1)/2) * (childW + gapX)，y = parent.y + gapY；
//   ⑥ 子节点 id = `asset-run:${todoId}`，kind = assetKindById.get(assetId) ?? "unknown"；
//   ⑦ 边 {id:`${parent.canvasNodeId}->asset-run:${todoId}`, source:parent.canvasNodeId, target:childId, type:"studio"}。
export function buildAssetFanout(
  state: ProjectState,
  parents: ParentAnchor[],
  assetKindById: Map<string, AssetKind>,
  opts?: FanoutLayoutOpts,
): { nodes: AssetRunRFNode[]; edges: RFEdge[] } {
  const childW = opts?.childW ?? DEFAULT_CHILD_W
  const gapX = opts?.gapX ?? DEFAULT_GAP_X
  const gapY = opts?.gapY ?? DEFAULT_GAP_Y
  const maxPerRow = Math.max(1, opts?.maxPerRow ?? DEFAULT_MAX_PER_ROW)
  const rowGapY = opts?.rowGapY ?? DEFAULT_ROW_GAP_Y

  const parentByTodo = new Map<string, ParentAnchor>()
  for (const p of parents) parentByTodo.set(p.todoId, p)

  const assetNodes = state.nodes.filter((n) => n.type === "asset")

  // 先按父画布节点分组（保持 state.nodes 原序），记录每个 asset 的父锚点。
  const groups = new Map<string, { parent: ParentAnchor; assets: typeof assetNodes }>()
  for (const n of assetNodes) {
    const parentTodoId = state.edges.find((e) => e.from === n.id)?.to
    if (parentTodoId == null) continue
    const parent = parentByTodo.get(parentTodoId)
    if (!parent) continue
    const g = groups.get(parent.canvasNodeId) ?? { parent, assets: [] }
    g.assets.push(n)
    groups.set(parent.canvasNodeId, g)
  }

  const nodes: AssetRunRFNode[] = []
  const edges: RFEdge[] = []
  for (const { parent, assets } of groups.values()) {
    const count = assets.length
    // 网格布局：每行最多 maxPerRow 个，超出换行。行内以父节点为中心水平居中铺开，
    // 逐行向下堆叠 —— 避免子节点多时挤成一条溢出视口的长横排。
    const perRow = Math.min(count, maxPerRow)
    assets.forEach((n, i) => {
      const childId = `asset-run:${n.id}`
      const kind = (n.assetId ? assetKindById.get(n.assetId) : undefined) ?? "unknown"
      const row = Math.floor(i / perRow)
      const col = i % perRow
      // 末行可能不满，按该行实际节点数居中。
      const rowCount = Math.min(perRow, count - row * perRow)
      const x = parent.x + (col - (rowCount - 1) / 2) * (childW + gapX)
      const y = parent.y + gapY + row * rowGapY
      nodes.push({
        id: childId,
        type: "assetRun",
        position: { x, y },
        data: {
          todoId: n.id,
          assetId: n.assetId,
          status: n.status,
          kind,
          pageOrdinal: i + 1,
        },
      })
      edges.push({
        id: `${parent.canvasNodeId}->${childId}`,
        source: parent.canvasNodeId,
        target: childId,
        type: "studio",
      })
    })
  }
  return { nodes, edges }
}
