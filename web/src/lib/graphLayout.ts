import type { GraphNode, GraphEdge } from "./projectState"

// layerize 把 DAG 拓扑分层:无依赖的节点在第 0 层,其余按「最长依赖路径」下沉。
// 同层节点保持输入顺序(后端已按 created_at,id 稳定排序)。
// 残留环防御:迭代次数上限 = 节点数 + 1,环内节点停在已达到的层,不死循环。
export function layerize(nodes: GraphNode[], edges: GraphEdge[]): GraphNode[][] {
  if (nodes.length === 0) return []
  const has = new Set(nodes.map((x) => x.id))
  // prereqs: 节点 → 它依赖的节点(edge.from 依赖 edge.to)。
  const prereqs = new Map<string, string[]>()
  for (const x of nodes) prereqs.set(x.id, [])
  for (const e of edges) {
    if (has.has(e.from) && has.has(e.to)) prereqs.get(e.from)!.push(e.to)
  }
  const layer = new Map<string, number>()
  for (const x of nodes) layer.set(x.id, 0)

  let changed = true
  let guard = nodes.length + 1
  while (changed && guard-- > 0) {
    changed = false
    for (const x of nodes) {
      let want = 0
      for (const d of prereqs.get(x.id)!) {
        want = Math.max(want, (layer.get(d) ?? 0) + 1)
      }
      if (want !== layer.get(x.id)) {
        layer.set(x.id, want)
        changed = true
      }
    }
  }

  const maxLayer = Math.max(0, ...nodes.map((x) => layer.get(x.id) ?? 0))
  const out: GraphNode[][] = Array.from({ length: maxLayer + 1 }, () => [])
  for (const x of nodes) out[layer.get(x.id) ?? 0].push(x)
  return out.filter((l) => l.length > 0)
}
