import { describe, expect, it } from "vitest"
import { buildRunGroups, type AssetKind, type ParentAnchor } from "./runFanout"
import type { ProjectState, GraphNode, GraphEdge } from "@/lib/projectState"

// 最小 ProjectState 夹具：只给 buildRunGroups 用到的 nodes/edges，其余补默认。
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

const BOARD: ParentAnchor = { todoId: "board-0", canvasNodeId: "storyboard-1" }

describe("buildRunGroups", () => {
  it("空态：无 asset 节点 → 无分组", () => {
    const state = stateWith(
      [{ id: "board-0", label: "分镜", type: "storyboard", status: "done" }],
      [],
    )
    const groups = buildRunGroups(state, [BOARD], new Map())
    expect(groups.size).toBe(0)
  })

  it("单父多 asset：cell 顺序 + pageOrdinal 递增 + 计数", () => {
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
    const groups = buildRunGroups(state, [BOARD], kindById)
    expect(groups.size).toBe(1)
    const g = groups.get("storyboard-1")!
    expect(g.cells).toHaveLength(2)
    // pageOrdinal 按 state.nodes 序：a0=1, a1=2。
    expect(g.cells[0].pageOrdinal).toBe(1)
    expect(g.cells[1].pageOrdinal).toBe(2)
    expect(g.cells[0].todoId).toBe("a0")
    expect(g.cells[1].todoId).toBe("a1")
    // 计数：1 done + 1 running。
    expect(g.counts).toEqual({ done: 1, running: 1, failed: 0, pending: 0, total: 2 })
  })

  it("kind 映射：有 assetId 走 map，无 assetId → unknown 仍出 cell", () => {
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
    const g = buildRunGroups(state, [BOARD], kindById).get("storyboard-1")!
    expect(g.cells.map((c) => c.kind)).toEqual(["image", "audio", "unknown"])
    // 无 assetId 仍出 cell（generating 阶段）。
    expect(g.cells[2].assetId).toBeUndefined()
    expect(g.cells[2].status).toBe("running")
  })

  it("多父分组：各父独立 cell 列表，pageOrdinal 各自从 1 起", () => {
    const board2: ParentAnchor = { todoId: "board-1", canvasNodeId: "storyboard-2" }
    const state = stateWith(
      [
        { id: "board-0", label: "分镜A", type: "storyboard", status: "done" },
        { id: "board-1", label: "分镜B", type: "storyboard", status: "done" },
        { id: "a0", label: "A图", type: "asset", status: "done", assetId: "i0" },
        { id: "b0", label: "B图", type: "asset", status: "done", assetId: "i1" },
        { id: "b1", label: "B图2", type: "asset", status: "failed" },
      ],
      [
        { from: "a0", to: "board-0" },
        { from: "b0", to: "board-1" },
        { from: "b1", to: "board-1" },
      ],
    )
    const groups = buildRunGroups(state, [BOARD, board2], new Map())
    expect(groups.size).toBe(2)
    const gA = groups.get("storyboard-1")!
    const gB = groups.get("storyboard-2")!
    // board-0 下单个 asset → pageOrdinal 1，total 1。
    expect(gA.cells.map((c) => c.pageOrdinal)).toEqual([1])
    expect(gA.counts.total).toBe(1)
    // board-1 下两个 asset → pageOrdinal 1/2，计数 1 done + 1 failed。
    expect(gB.cells.map((c) => c.pageOrdinal)).toEqual([1, 2])
    expect(gB.counts).toEqual({ done: 1, running: 0, failed: 1, pending: 0, total: 2 })
  })

  it("孤儿 skip 不抛：边缺失 或 父锚点不匹配的 asset 不出分组", () => {
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
    const groups = buildRunGroups(state, [BOARD], new Map())
    expect(groups.size).toBe(0)
  })

  it("边方向断言：用 e.from===assetId 取父（而非 e.to）", () => {
    // 若误写成 e.to===assetId，下面这条边（from=a0,to=board-0）找不到父 → 无分组。
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a0", label: "图", type: "asset", status: "done", assetId: "img0" },
      ],
      [{ from: "a0", to: "board-0" }],
    )
    const g = buildRunGroups(state, [BOARD], new Map()).get("storyboard-1")
    expect(g).toBeDefined()
    expect(g!.cells).toHaveLength(1)
    expect(g!.cells[0].todoId).toBe("a0")
  })

  it("pending/blocked 都计入 pending 计数", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "running" },
        { id: "a0", label: "待", type: "asset", status: "pending" },
        { id: "a1", label: "阻", type: "asset", status: "blocked" },
      ],
      [
        { from: "a0", to: "board-0" },
        { from: "a1", to: "board-0" },
      ],
    )
    const g = buildRunGroups(state, [BOARD], new Map()).get("storyboard-1")!
    expect(g.counts.pending).toBe(2)
    expect(g.counts.total).toBe(2)
  })
})
