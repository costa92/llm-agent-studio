import type { ProjectState, GraphNodeStatus } from "@/lib/projectState"

// 运行态扇出分组（纯函数，无 React）：把 storyboard 扇出的逐页 asset todo
// 归并成「父分镜 → 逐页(RunPage) + 状态计数」。运行态画布把它渲成可折叠的大功能
// 容器（折叠=状态条 + [N 项]；展开=逐页卡片），右栏渲成 Run Matrix。
//
// 图音双渲染（PR-4）：一个绘本跨页会扇出 image + audio 两个 asset todo，二者共享
//   同一 shotId（见 worker.go 逐 shot 建 image/audio 两条 todo，输入 JSON 同 shotId）。
//   GraphNode 本身不带 shotId，只有 assetId → 经 assetMetaById 解析出 {kind, shotId}，
//   按 shotId 把图/音配成一页（image 槽 + audio 槽）。无法解析 shotId（如资产未生成、
//   assetId 缺失）→ 回落 todoId 自成一页（图/音就绪后会自动并入同一 shotId 页）。
//
// ⚠️ 关键事实（已实测，见 internal/projectstate/state.go GraphEdge{From:t.ID, To:dep}）：
//   ProjectState.edges 的 GraphEdge{from,to} = 「from 依赖 to」。扇出的 asset todo 是 from，
//   父 storyboard 是 to。故找父：state.edges.find(e => e.from === assetNode.id)?.to。
// 仅运行态可视化：不回写 dependsOn、不进保存载荷、不产可视化边（子卡是容器内 HTML 网格）。

export type AssetKind = "image" | "audio" | "video" | "unknown"

// asset 元信息（按 assetId 索引，来源 ProjectAsset）：kind 决定图/音槽，shotId 决定配对。
export interface AssetMeta {
  kind: AssetKind
  shotId?: string
}

// 一页内的单个资产格（一个 asset todo）。
export interface PageCell {
  todoId: string
  assetId?: string
  status: GraphNodeStatus
  kind: AssetKind
}

// 一页（一个 shot）：图 + 音配对渲染。others = 无法归入图/音槽的（video / 同类重复 / unknown）。
export interface RunPage {
  // 配对键：优先 shotId；无法解析 → todoId（自成一页 fallback）。
  key: string
  // 该页在父分镜下的序号（从 1 起，按页首次出现序 = state.nodes 拓扑序）。
  pageOrdinal: number
  image?: PageCell
  audio?: PageCell
  others: PageCell[]
}

export interface GroupCounts {
  done: number
  running: number
  failed: number
  // pending + blocked 合并计入 pending（状态条/计数里都当「待运行」）。
  pending: number
  // 逐个资产格计（非页数）——summary「X/Y 完成」与徽标「N 项」都指生成任务数。
  total: number
}

// 一个大功能容器：父画布节点 id + 逐页 + 状态计数。
export interface RunGroup {
  canvasNodeId: string
  pages: RunPage[]
  counts: GroupCounts
}

// 父分镜锚点：run 节点 todoId（用于 edge.to 匹配）+ 画布节点 id。
export interface ParentAnchor {
  todoId: string
  canvasNodeId: string
}

// 一页的所有资产格（图 → 音 → others 顺序）。供状态条/计数展平。
export function pageCells(p: RunPage): PageCell[] {
  const cs: PageCell[] = []
  if (p.image) cs.push(p.image)
  if (p.audio) cs.push(p.audio)
  cs.push(...p.others)
  return cs
}

// 一页的聚合状态（供矩阵格/状态条着色）：failed > running > pending > done。
// 语义：任一格失败 → 页失败；任一在跑 → 页运行中；任一待 → 页待运行；全 done → 页完成。
export function pageStatus(p: RunPage): GraphNodeStatus {
  const cs = pageCells(p)
  if (cs.some((c) => c.status === "failed")) return "failed"
  if (cs.some((c) => c.status === "running")) return "running"
  if (cs.some((c) => c.status === "pending" || c.status === "blocked")) return "pending"
  return "done"
}

// 主映射：ProjectState（含 asset run 节点 + edges）+ 父分镜锚点 + asset 元信息 map
//   → Map<父画布节点 id, RunGroup>。
// 逻辑：
//   ① parentByTodo: 父锚点按 todoId 索引；
//   ② assetNodes: state.nodes 里 type==="asset"（保持 state.nodes 原序 = 后端拓扑序）；
//   ③ 每 asset：parentTodoId = edges.find(e => e.from === n.id)?.to，parent = parentByTodo.get(parentTodoId)；
//      无父 → skip（孤儿，不抛）；
//   ④ 页键 = meta.shotId ?? n.id；同键归一页，image/audio 各占一槽（已占 → others）；
//      pageOrdinal = 该组内页首次出现序；
//   ⑤ counts 按 cell 状态累计（pending/blocked 都计入 pending，total = 资产格数）。
export function buildRunGroups(
  state: ProjectState,
  parents: ParentAnchor[],
  assetMetaById: Map<string, AssetMeta>,
): Map<string, RunGroup> {
  const parentByTodo = new Map<string, ParentAnchor>()
  for (const p of parents) parentByTodo.set(p.todoId, p)

  const assetNodes = state.nodes.filter((n) => n.type === "asset")

  const groups = new Map<string, RunGroup>()
  // 每组按页键索引 RunPage，累积同 shot 的图/音（Map 保持首次出现序 → pageOrdinal）。
  const pageMapByGroup = new Map<string, Map<string, RunPage>>()

  for (const n of assetNodes) {
    const parentTodoId = state.edges.find((e) => e.from === n.id)?.to
    if (parentTodoId == null) continue
    const parent = parentByTodo.get(parentTodoId)
    if (!parent) continue

    let g = groups.get(parent.canvasNodeId)
    let pageMap = pageMapByGroup.get(parent.canvasNodeId)
    if (!g || !pageMap) {
      g = {
        canvasNodeId: parent.canvasNodeId,
        pages: [],
        counts: { done: 0, running: 0, failed: 0, pending: 0, total: 0 },
      }
      groups.set(parent.canvasNodeId, g)
      pageMap = new Map()
      pageMapByGroup.set(parent.canvasNodeId, pageMap)
    }

    const meta = n.assetId ? assetMetaById.get(n.assetId) : undefined
    const kind = meta?.kind ?? "unknown"
    const cell: PageCell = { todoId: n.id, assetId: n.assetId, status: n.status, kind }

    const pageKey = meta?.shotId ?? n.id
    let page = pageMap.get(pageKey)
    if (!page) {
      page = { key: pageKey, pageOrdinal: pageMap.size + 1, others: [] }
      pageMap.set(pageKey, page)
      g.pages.push(page)
    }
    if (kind === "image" && !page.image) page.image = cell
    else if (kind === "audio" && !page.audio) page.audio = cell
    else page.others.push(cell)

    g.counts.total += 1
    if (n.status === "done") g.counts.done += 1
    else if (n.status === "running") g.counts.running += 1
    else if (n.status === "failed") g.counts.failed += 1
    else g.counts.pending += 1
  }
  return groups
}
