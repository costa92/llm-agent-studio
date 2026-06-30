import { describe, expect, it } from "vitest"
import { buildAssetFanout, type AssetKind, type ParentAnchor } from "./runFanout"
import type { ProjectState, GraphNode, GraphEdge } from "@/lib/projectState"

// 最小 ProjectState 夹具：只给 buildAssetFanout 用到的 nodes/edges，其余补默认。
function stateWith(nodes: GraphNode[], edges: GraphEdge[]): ProjectState {
  return {
    projectId: "p1",
    version: 1,
    status: "running",
    runStatus: "running",
    stages: [],
    pips: [],
    assets: { total: 0, done: 0, pending: 0 },
    nodes,
    edges,
    isCustom: true,
  }
}

const BOARD: ParentAnchor = { todoId: "board-0", canvasNodeId: "storyboard-1", x: 100, y: 0 }

describe("buildAssetFanout", () => {
  it("空态：无 asset 节点 → 无子节点、无边", () => {
    const state = stateWith(
      [{ id: "board-0", label: "分镜", type: "storyboard", status: "done" }],
      [],
    )
    const { nodes, edges } = buildAssetFanout(state, [BOARD], new Map())
    expect(nodes).toEqual([])
    expect(edges).toEqual([])
  })

  it("单父多 asset：pageOrdinal 递增、x 居中、y = parent.y + gapY", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a0", label: "图0", type: "asset", status: "done", assetId: "img0" },
        { id: "a1", label: "图1", type: "asset", status: "running" },
      ],
      [
        // from 依赖 to：asset todo (from) → 父 storyboard (to)。
        { from: "a0", to: "board-0" },
        { from: "a1", to: "board-0" },
      ],
    )
    const kindById = new Map<string, AssetKind>([["img0", "image"]])
    const { nodes, edges } = buildAssetFanout(state, [BOARD], kindById)
    expect(nodes).toHaveLength(2)
    // pageOrdinal 按 state.nodes 序：a0=1, a1=2。
    expect(nodes[0].data.pageOrdinal).toBe(1)
    expect(nodes[1].data.pageOrdinal).toBe(2)
    // 居中：count=2，i=0 → x=100+(-0.5)*144=28；i=1 → x=100+0.5*144=172。
    expect(nodes[0].position.x).toBe(28)
    expect(nodes[1].position.x).toBe(172)
    // y = parent.y(0) + gapY(140)。
    expect(nodes[0].position.y).toBe(140)
    expect(nodes[1].position.y).toBe(140)
    // id 形状 + 边方向（source=父画布节点, target=子节点）。
    expect(nodes[0].id).toBe("asset-run:a0")
    expect(edges[0]).toEqual({
      id: "storyboard-1->asset-run:a0",
      source: "storyboard-1",
      target: "asset-run:a0",
      type: "studio",
    })
  })

  it("kind 映射：有 assetId 走 map，无 assetId → unknown 仍出节点", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a0", label: "图", type: "asset", status: "done", assetId: "img0" },
        { id: "a1", label: "音", type: "asset", status: "done", assetId: "aud0" },
        { id: "a2", label: "生成中", type: "asset", status: "running" },
      ],
      [
        { from: "a0", to: "board-0" },
        { from: "a1", to: "board-0" },
        { from: "a2", to: "board-0" },
      ],
    )
    const kindById = new Map<string, AssetKind>([
      ["img0", "image"],
      ["aud0", "audio"],
    ])
    const { nodes } = buildAssetFanout(state, [BOARD], kindById)
    expect(nodes.map((n) => n.data.kind)).toEqual(["image", "audio", "unknown"])
    // 无 assetId 仍出节点（generating 阶段）。
    expect(nodes[2].data.assetId).toBeUndefined()
    expect(nodes[2].data.status).toBe("running")
  })

  it("多父分组：各父下独立居中布局，pageOrdinal 各自从 1 起", () => {
    const board2: ParentAnchor = { todoId: "board-1", canvasNodeId: "storyboard-2", x: 500, y: 0 }
    const state = stateWith(
      [
        { id: "board-0", label: "分镜A", type: "storyboard", status: "done" },
        { id: "board-1", label: "分镜B", type: "storyboard", status: "done" },
        { id: "a0", label: "A图", type: "asset", status: "done", assetId: "i0" },
        { id: "b0", label: "B图", type: "asset", status: "done", assetId: "i1" },
        { id: "b1", label: "B图2", type: "asset", status: "done", assetId: "i2" },
      ],
      [
        { from: "a0", to: "board-0" },
        { from: "b0", to: "board-1" },
        { from: "b1", to: "board-1" },
      ],
    )
    const { nodes } = buildAssetFanout(state, [BOARD, board2], new Map())
    const byId = new Map(nodes.map((n) => [n.id, n]))
    // board-0 下单个 asset → 居中（x=parent.x=100）。
    expect(byId.get("asset-run:a0")!.position.x).toBe(100)
    expect(byId.get("asset-run:a0")!.data.pageOrdinal).toBe(1)
    // board-1 下两个 asset → pageOrdinal 1/2，围绕 x=500 居中。
    expect(byId.get("asset-run:b0")!.data.pageOrdinal).toBe(1)
    expect(byId.get("asset-run:b1")!.data.pageOrdinal).toBe(2)
    expect(byId.get("asset-run:b0")!.position.x).toBe(500 - 72)
    expect(byId.get("asset-run:b1")!.position.x).toBe(500 + 72)
  })

  it("孤儿 skip 不抛：边缺失 或 父锚点不匹配的 asset 不出节点", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a-orphan", label: "无边", type: "asset", status: "done", assetId: "x" },
        { id: "a-badparent", label: "父不匹配", type: "asset", status: "done", assetId: "y" },
      ],
      [
        // a-orphan 无边；a-badparent 指向一个没有锚点的 todo。
        { from: "a-badparent", to: "board-unknown" },
      ],
    )
    const { nodes, edges } = buildAssetFanout(state, [BOARD], new Map())
    expect(nodes).toEqual([])
    expect(edges).toEqual([])
  })

  it("边方向断言：用 e.from===assetId 取父（而非 e.to）", () => {
    // 若误写成 e.to===assetId，下面这条边（from=a0,to=board-0）找不到父 → 无节点。
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a0", label: "图", type: "asset", status: "done", assetId: "img0" },
      ],
      [{ from: "a0", to: "board-0" }],
    )
    const { nodes } = buildAssetFanout(state, [BOARD], new Map())
    expect(nodes).toHaveLength(1)
    expect(nodes[0].id).toBe("asset-run:a0")
  })

  it("网格换行：超过 maxPerRow 的子节点换到下一行（y 下移、行内重新居中）", () => {
    const boardNodes: GraphNode[] = [
      { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
    ]
    const edges: GraphEdge[] = []
    // 8 个 asset，maxPerRow=6 → 第一行 6 个、第二行 2 个。
    for (let i = 0; i < 8; i++) {
      boardNodes.push({ id: `a${i}`, label: `图${i}`, type: "asset", status: "done", assetId: `i${i}` })
      edges.push({ from: `a${i}`, to: "board-0" })
    }
    const state = stateWith(boardNodes, edges)
    const { nodes } = buildAssetFanout(state, [BOARD], new Map(), { maxPerRow: 6 })
    const byId = new Map(nodes.map((n) => [n.id, n]))
    // 第一行 6 个（i=0..5）y 相同 = parent.y + gapY(140)。
    expect(byId.get("asset-run:a0")!.position.y).toBe(140)
    expect(byId.get("asset-run:a5")!.position.y).toBe(140)
    // 第二行（i=6,7）y = 140 + rowGapY(132) = 272。
    expect(byId.get("asset-run:a6")!.position.y).toBe(272)
    expect(byId.get("asset-run:a7")!.position.y).toBe(272)
    // 第二行只有 2 个 → 围绕 parent.x(100) 居中：a6=100-72, a7=100+72。
    expect(byId.get("asset-run:a6")!.position.x).toBe(100 - 72)
    expect(byId.get("asset-run:a7")!.position.x).toBe(100 + 72)
    // pageOrdinal 仍按整体序 1..8（跨行连续）。
    expect(byId.get("asset-run:a6")!.data.pageOrdinal).toBe(7)
  })

  it("自定义布局参数：childW/gapX/gapY 透传", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a0", label: "图0", type: "asset", status: "done", assetId: "i0" },
        { id: "a1", label: "图1", type: "asset", status: "done", assetId: "i1" },
      ],
      [
        { from: "a0", to: "board-0" },
        { from: "a1", to: "board-0" },
      ],
    )
    const { nodes } = buildAssetFanout(state, [BOARD], new Map(), {
      childW: 100,
      gapX: 0,
      gapY: 200,
    })
    // step = childW+gapX = 100；i=0 → 100+(-0.5)*100=50；y=0+200。
    expect(nodes[0].position.x).toBe(50)
    expect(nodes[0].position.y).toBe(200)
  })
})
