import type { ProjectState, GraphNodeStatus } from "@/lib/projectState"

// 运行态扇出分组（纯函数，无 React）：把 storyboard 扇出的逐页 asset todo
// 归并成「父分镜 → 逐页 cell 列表 + 状态计数」。运行态画布把它渲成可折叠的大功能
// 容器（折叠=状态条 + [N 项]；展开=逐页子卡网格），右栏渲成 Run Matrix。
//
// ⚠️ 关键事实（已实测，见 internal/projectstate/state.go GraphEdge{From:t.ID, To:dep}）：
//   ProjectState.edges 的 GraphEdge{from,to} = 「from 依赖 to」。扇出的 asset todo 是 from，
//   父 storyboard 是 to。故找父：state.edges.find(e => e.from === assetNode.id)?.to。
// 仅运行态可视化：不回写 dependsOn、不进保存载荷、不产可视化边（子卡是容器内 HTML 网格）。

export type AssetKind = "image" | "audio" | "video" | "unknown"

// 容器内一页（一个 asset todo）的渲染数据。
export interface GroupCell {
  todoId: string
  assetId?: string
  status: GraphNodeStatus
  kind: AssetKind
  // 该页在父分镜下的序号（从 1 起，按 state.nodes 后端拓扑序）。
  pageOrdinal: number
}

export interface GroupCounts {
  done: number
  running: number
  failed: number
  // pending + blocked 合并计入 pending（状态条/计数里都当「待运行」）。
  pending: number
  total: number
}

// 一个大功能容器：父画布节点 id + 逐页 cell + 状态计数。
export interface RunGroup {
  canvasNodeId: string
  cells: GroupCell[]
  counts: GroupCounts
}

// 父分镜锚点：run 节点 todoId（用于 edge.to 匹配）+ 画布节点 id。
// （不再需要 x/y —— 子卡是容器内 HTML 网格，不是独立坐标的 RF 节点。）
export interface ParentAnchor {
  todoId: string
  canvasNodeId: string
}

// 主映射：ProjectState（含 asset run 节点 + edges）+ 父分镜锚点 + 资产 kind map
//   → Map<父画布节点 id, RunGroup>。
// 逻辑：
//   ① parentByTodo: 父锚点按 todoId 索引；
//   ② assetNodes: state.nodes 里 type==="asset"（保持 state.nodes 原序 = 后端拓扑序）；
//   ③ 每 asset：parentTodoId = edges.find(e => e.from === n.id)?.to，parent = parentByTodo.get(parentTodoId)；
//      无父 → skip（孤儿，不抛）；
//   ④ 按 parent.canvasNodeId 分组，组内序 = state.nodes 序 → pageOrdinal = 组内当前长度 + 1；
//   ⑤ kind = assetKindById.get(assetId) ?? "unknown"；
//   ⑥ counts 按 cell 状态累计（pending/blocked 都计入 pending）。
export function buildRunGroups(
  state: ProjectState,
  parents: ParentAnchor[],
  assetKindById: Map<string, AssetKind>,
): Map<string, RunGroup> {
  const parentByTodo = new Map<string, ParentAnchor>()
  for (const p of parents) parentByTodo.set(p.todoId, p)

  const assetNodes = state.nodes.filter((n) => n.type === "asset")

  const groups = new Map<string, RunGroup>()
  for (const n of assetNodes) {
    const parentTodoId = state.edges.find((e) => e.from === n.id)?.to
    if (parentTodoId == null) continue
    const parent = parentByTodo.get(parentTodoId)
    if (!parent) continue
    let g = groups.get(parent.canvasNodeId)
    if (!g) {
      g = {
        canvasNodeId: parent.canvasNodeId,
        cells: [],
        counts: { done: 0, running: 0, failed: 0, pending: 0, total: 0 },
      }
      groups.set(parent.canvasNodeId, g)
    }
    const kind = (n.assetId ? assetKindById.get(n.assetId) : undefined) ?? "unknown"
    g.cells.push({
      todoId: n.id,
      assetId: n.assetId,
      status: n.status,
      kind,
      pageOrdinal: g.cells.length + 1,
    })
    g.counts.total += 1
    if (n.status === "done") g.counts.done += 1
    else if (n.status === "running") g.counts.running += 1
    else if (n.status === "failed") g.counts.failed += 1
    else g.counts.pending += 1
  }
  return groups
}
