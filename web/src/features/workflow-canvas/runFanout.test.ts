import { describe, expect, it } from "vitest"
import {
  buildRunGroups,
  pageCells,
  pageStatus,
  type AssetMeta,
  type ParentAnchor,
  type RunPage,
} from "./runFanout"
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

  it("图音配对：同 shotId 的 image + audio 归一页（图槽/音槽），pageOrdinal 从 1，计数按资产格", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a-img0", label: "图0", type: "asset", status: "done", assetId: "img0" },
        { id: "a-aud0", label: "音0", type: "asset", status: "running", assetId: "aud0" },
      ],
      [
        // from 依赖 to：asset todo (from) → 父 storyboard (to)。
        { from: "a-img0", to: "board-0" },
        { from: "a-aud0", to: "board-0" },
      ],
    )
    const meta = new Map<string, AssetMeta>([
      ["img0", { kind: "image", shotId: "s1" }],
      ["aud0", { kind: "audio", shotId: "s1" }],
    ])
    const groups = buildRunGroups(state, [BOARD], meta)
    expect(groups.size).toBe(1)
    const g = groups.get("storyboard-1")!
    // 图+音同 shot → 一页。
    expect(g.pages).toHaveLength(1)
    const p = g.pages[0]
    expect(p.pageOrdinal).toBe(1)
    expect(p.key).toBe("s1")
    expect(p.image?.todoId).toBe("a-img0")
    expect(p.audio?.todoId).toBe("a-aud0")
    // 计数逐资产格：1 done（图）+ 1 running（音）。
    expect(g.counts).toEqual({ done: 1, running: 1, failed: 0, pending: 0, total: 2 })
  })

  it("多页配对：两 shot 各图+音 → 两页，pageOrdinal 按 shot 首次出现序", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "img1", label: "图1", type: "asset", status: "done", assetId: "i1" },
        { id: "aud1", label: "音1", type: "asset", status: "done", assetId: "u1" },
        { id: "img2", label: "图2", type: "asset", status: "running", assetId: "i2" },
        { id: "aud2", label: "音2", type: "asset", status: "pending", assetId: "u2" },
      ],
      [
        { from: "img1", to: "board-0" },
        { from: "aud1", to: "board-0" },
        { from: "img2", to: "board-0" },
        { from: "aud2", to: "board-0" },
      ],
    )
    const meta = new Map<string, AssetMeta>([
      ["i1", { kind: "image", shotId: "s1" }],
      ["u1", { kind: "audio", shotId: "s1" }],
      ["i2", { kind: "image", shotId: "s2" }],
      ["u2", { kind: "audio", shotId: "s2" }],
    ])
    const g = buildRunGroups(state, [BOARD], meta).get("storyboard-1")!
    expect(g.pages.map((p) => p.pageOrdinal)).toEqual([1, 2])
    expect(g.pages.map((p) => p.key)).toEqual(["s1", "s2"])
    expect(g.counts.total).toBe(4)
  })

  it("无 shotId（资产未生成 / assetId 缺失）→ 回落 todoId 自成一页", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a-img", label: "图", type: "asset", status: "done", assetId: "img0" },
        // 生成中无 assetId → 无 meta → 回落 todoId 自成一页。
        { id: "a-gen", label: "生成中", type: "asset", status: "running" },
      ],
      [
        { from: "a-img", to: "board-0" },
        { from: "a-gen", to: "board-0" },
      ],
    )
    const meta = new Map<string, AssetMeta>([["img0", { kind: "image", shotId: "s1" }]])
    const g = buildRunGroups(state, [BOARD], meta).get("storyboard-1")!
    // 两页：s1（图）+ a-gen（fallback，unknown 进 others）。
    expect(g.pages).toHaveLength(2)
    expect(g.pages[0].key).toBe("s1")
    expect(g.pages[1].key).toBe("a-gen")
    expect(g.pages[1].image).toBeUndefined()
    expect(g.pages[1].others[0].kind).toBe("unknown")
  })

  it("image-only 分镜（无音节点）→ 每 shot 一页仅图槽", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "img1", label: "图1", type: "asset", status: "done", assetId: "i1" },
        { id: "img2", label: "图2", type: "asset", status: "done", assetId: "i2" },
      ],
      [
        { from: "img1", to: "board-0" },
        { from: "img2", to: "board-0" },
      ],
    )
    const meta = new Map<string, AssetMeta>([
      ["i1", { kind: "image", shotId: "s1" }],
      ["i2", { kind: "image", shotId: "s2" }],
    ])
    const g = buildRunGroups(state, [BOARD], meta).get("storyboard-1")!
    expect(g.pages).toHaveLength(2)
    expect(g.pages.every((p) => p.image && !p.audio)).toBe(true)
  })

  it("多父分组：各父独立页列表，pageOrdinal 各自从 1 起", () => {
    const board2: ParentAnchor = { todoId: "board-1", canvasNodeId: "storyboard-2" }
    const state = stateWith(
      [
        { id: "board-0", label: "分镜A", type: "storyboard", status: "done" },
        { id: "board-1", label: "分镜B", type: "storyboard", status: "done" },
        { id: "a0", label: "A图", type: "asset", status: "done", assetId: "i0" },
        { id: "b0", label: "B图", type: "asset", status: "done", assetId: "i1" },
        { id: "b1", label: "B音", type: "asset", status: "failed", assetId: "u1" },
      ],
      [
        { from: "a0", to: "board-0" },
        { from: "b0", to: "board-1" },
        { from: "b1", to: "board-1" },
      ],
    )
    const meta = new Map<string, AssetMeta>([
      ["i0", { kind: "image", shotId: "sa" }],
      ["i1", { kind: "image", shotId: "sb" }],
      ["u1", { kind: "audio", shotId: "sb" }],
    ])
    const groups = buildRunGroups(state, [BOARD, board2], meta)
    expect(groups.size).toBe(2)
    const gA = groups.get("storyboard-1")!
    const gB = groups.get("storyboard-2")!
    expect(gA.pages.map((p) => p.pageOrdinal)).toEqual([1])
    expect(gA.counts.total).toBe(1)
    // board-1 下图+音同 shot sb → 一页；计数 1 done + 1 failed。
    expect(gB.pages).toHaveLength(1)
    expect(gB.counts).toEqual({ done: 1, running: 0, failed: 1, pending: 0, total: 2 })
  })

  it("孤儿 skip 不抛：边缺失 或 父锚点不匹配的 asset 不出分组", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a-orphan", label: "无边", type: "asset", status: "done", assetId: "x" },
        { id: "a-badparent", label: "父不匹配", type: "asset", status: "done", assetId: "y" },
      ],
      [{ from: "a-badparent", to: "board-unknown" }],
    )
    const groups = buildRunGroups(state, [BOARD], new Map())
    expect(groups.size).toBe(0)
  })

  it("边方向断言：用 e.from===assetId 取父（而非 e.to）", () => {
    const state = stateWith(
      [
        { id: "board-0", label: "分镜", type: "storyboard", status: "done" },
        { id: "a0", label: "图", type: "asset", status: "done", assetId: "img0" },
      ],
      [{ from: "a0", to: "board-0" }],
    )
    const g = buildRunGroups(state, [BOARD], new Map()).get("storyboard-1")
    expect(g).toBeDefined()
    expect(g!.pages).toHaveLength(1)
    // 无 meta → unknown → 进 others，页 key = todoId。
    expect(g!.pages[0].key).toBe("a0")
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

describe("pageCells / pageStatus", () => {
  const mk = (over: Partial<RunPage>): RunPage => ({ key: "s", pageOrdinal: 1, others: [], ...over })

  it("pageCells：图 → 音 → others 顺序", () => {
    const p = mk({
      image: { todoId: "i", status: "done", kind: "image" },
      audio: { todoId: "a", status: "running", kind: "audio" },
      others: [{ todoId: "o", status: "done", kind: "video" }],
    })
    expect(pageCells(p).map((c) => c.todoId)).toEqual(["i", "a", "o"])
  })

  it("pageStatus：failed > running > pending > done", () => {
    expect(
      pageStatus(mk({ image: { todoId: "i", status: "done", kind: "image" }, audio: { todoId: "a", status: "failed", kind: "audio" } })),
    ).toBe("failed")
    expect(
      pageStatus(mk({ image: { todoId: "i", status: "done", kind: "image" }, audio: { todoId: "a", status: "running", kind: "audio" } })),
    ).toBe("running")
    expect(
      pageStatus(mk({ image: { todoId: "i", status: "done", kind: "image" }, audio: { todoId: "a", status: "pending", kind: "audio" } })),
    ).toBe("pending")
    expect(
      pageStatus(mk({ image: { todoId: "i", status: "done", kind: "image" }, audio: { todoId: "a", status: "done", kind: "audio" } })),
    ).toBe("done")
    // blocked 计入 pending 档。
    expect(pageStatus(mk({ image: { todoId: "i", status: "blocked", kind: "image" } }))).toBe("pending")
  })
})
