import { describe, expect, it } from "vitest"
import {
  toReactFlow,
  toStudioNodes,
  addNodeAt,
  nextNodeId,
  duplicateNode,
  insertNodeOnEdge,
  cloneSelection,
  getHelperLines,
  defaultPromptIdFor,
  seedPositions,
  standardPipeline,
  reconnectEdge,
  createNode,
  collectCustomTypes,
  applyTypeDisplay,
  hasUnboundCustomNode,
  type RFNode,
  type RFEdge,
} from "./canvasModel"
import { findGraphError } from "@/features/projects/WorkflowDialog.schema"
import type { Prompt, WorkflowNode } from "@/lib/types"

// 3 节点链：script-1 → storyboard-1 → asset-1。
const chain: WorkflowNode[] = [
  { id: "script-1", type: "script", promptId: "", dependsOn: [] },
  { id: "storyboard-1", type: "storyboard", promptId: "", dependsOn: ["script-1"] },
  { id: "asset-1", type: "asset", promptId: "", dependsOn: ["storyboard-1"] },
]

describe("toReactFlow", () => {
  it("builds edges with correct source/target (source = upstream dep)", () => {
    const { edges } = toReactFlow(chain)
    expect(edges).toHaveLength(2)
    const byId = new Map(edges.map((e) => [e.id, e]))
    const e1 = byId.get("script-1->storyboard-1")
    const e2 = byId.get("storyboard-1->asset-1")
    expect(e1).toMatchObject({ source: "script-1", target: "storyboard-1" })
    expect(e2).toMatchObject({ source: "storyboard-1", target: "asset-1" })
  })

  it("seeds layered coords for nodes without a position (top-down)", () => {
    const { nodes } = toReactFlow(chain)
    const pos = new Map(nodes.map((n) => [n.id, n.position]))
    expect(pos.get("script-1")).toEqual({ x: 0, y: 0 })
    expect(pos.get("storyboard-1")).toEqual({ x: 0, y: 140 })
    expect(pos.get("asset-1")).toEqual({ x: 0, y: 280 })
  })

  it("keeps an explicit position when the node has one", () => {
    const withPos = [
      { ...chain[0], position: { x: 999, y: 777 } } as WorkflowNode,
      chain[1],
      chain[2],
    ]
    const { nodes } = toReactFlow(withPos)
    const script = nodes.find((n) => n.id === "script-1")!
    expect(script.position).toEqual({ x: 999, y: 777 })
  })
})

describe("seedPositions", () => {
  it("places each node at {x: indexWithinLayer*240, y: layerIndex*140}", () => {
    const seeded = seedPositions(chain)
    expect(seeded.get("script-1")).toEqual({ x: 0, y: 0 })
    expect(seeded.get("storyboard-1")).toEqual({ x: 0, y: 140 })
    expect(seeded.get("asset-1")).toEqual({ x: 0, y: 280 })
  })

  it("spreads sibling nodes in the same layer horizontally", () => {
    const fanout: WorkflowNode[] = [
      { id: "a", type: "script", promptId: "", dependsOn: [] },
      { id: "b", type: "script", promptId: "", dependsOn: [] },
    ]
    const seeded = seedPositions(fanout)
    expect(seeded.get("a")).toEqual({ x: 0, y: 0 })
    expect(seeded.get("b")).toEqual({ x: 240, y: 0 })
  })
})

// 小工具：把 studio nodes 转成 RF 状态供反向转换测试用。
function rf(nodes: WorkflowNode[]): { nodes: RFNode[]; edges: RFEdge[] } {
  return toReactFlow(nodes)
}

describe("toStudioNodes", () => {
  it("round-trips dependsOn both ways (edges are the source of truth)", () => {
    const { nodes, edges } = rf(chain)
    const out = toStudioNodes(nodes, edges)
    const byId = new Map(out.map((n) => [n.id, n]))
    expect(byId.get("script-1")!.dependsOn).toEqual([])
    expect(byId.get("storyboard-1")!.dependsOn).toEqual(["script-1"])
    expect(byId.get("asset-1")!.dependsOn).toEqual(["storyboard-1"])
  })

  it("includes integer position for every node", () => {
    const { edges } = rf(chain)
    // 模拟自由拖动后的小数坐标。
    const nodes: RFNode[] = [
      {
        id: "script-1",
        type: "studio",
        position: { x: 10.4, y: 20.6 },
        data: { node: chain[0] },
      },
      {
        id: "storyboard-1",
        type: "studio",
        position: { x: 0, y: 140 },
        data: { node: chain[1] },
      },
      {
        id: "asset-1",
        type: "studio",
        position: { x: 0, y: 280 },
        data: { node: chain[2] },
      },
    ]
    const out = toStudioNodes(nodes, edges)
    for (const n of out) {
      expect(n.position).toBeDefined()
      expect(Number.isInteger(n.position!.x)).toBe(true)
      expect(Number.isInteger(n.position!.y)).toBe(true)
    }
    const moved = out.find((n) => n.id === "script-1")!
    // 10.4 → 10, 20.6 → 21（四舍五入）。
    expect(moved.position).toEqual({ x: 10, y: 21 })
  })

  it("omits empty promptText, keeps non-empty", () => {
    const nodes: RFNode[] = [
      {
        id: "a",
        type: "studio",
        position: { x: 0, y: 0 },
        data: {
          node: { id: "a", type: "script", promptId: "", promptText: "", dependsOn: [] },
        },
      },
      {
        id: "b",
        type: "studio",
        position: { x: 0, y: 0 },
        data: {
          node: { id: "b", type: "script", promptId: "", promptText: "hi", dependsOn: [] },
        },
      },
    ]
    const out = toStudioNodes(nodes, [])
    expect(out.find((n) => n.id === "a")!.promptText).toBeUndefined()
    expect(out.find((n) => n.id === "b")!.promptText).toBe("hi")
  })
})

describe("defaultPromptIdFor", () => {
  const prompts: Prompt[] = [
    {
      id: "p-script",
      orgId: "o",
      name: "脚本默认",
      content: "",
      style: "",
      kind: "script",
      isDefault: true,
      createdAt: "",
      updatedAt: "",
    },
  ]
  it("returns the org default prompt id for the kind, else empty", () => {
    expect(defaultPromptIdFor(prompts, "script")).toBe("p-script")
    expect(defaultPromptIdFor(prompts, "storyboard")).toBe("")
    expect(defaultPromptIdFor(undefined, "script")).toBe("")
  })
})

describe("addNodeAt", () => {
  it("appends a new node at pos with a unique id and default-ish prompt", () => {
    const { nodes } = rf(chain) // 3 nodes
    const prompts: Prompt[] = [
      {
        id: "p-script",
        orgId: "o",
        name: "x",
        content: "",
        style: "",
        kind: "script",
        isDefault: true,
        createdAt: "",
        updatedAt: "",
      },
    ]
    const next = addNodeAt(nodes, "script", { x: 50, y: 60 }, prompts)
    expect(next).toHaveLength(4)
    const added = next[next.length - 1]
    expect(added.position).toEqual({ x: 50, y: 60 })
    expect(added.data.node.type).toBe("script")
    expect(added.data.node.promptId).toBe("p-script")
    // id 唯一。
    const ids = next.map((n) => n.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it("collision-checks the generated id against existing ids", () => {
    const existing: RFNode[] = [
      {
        id: "node-1",
        type: "studio",
        position: { x: 0, y: 0 },
        data: { node: { id: "node-1", type: "script", promptId: "", dependsOn: [] } },
      },
    ]
    // length+1 = node-2，无冲突。
    const next = addNodeAt(existing, "asset", { x: 0, y: 0 })
    expect(next[next.length - 1].id).toBe("node-2")
  })
})

describe("standardPipeline", () => {
  const prompts: Prompt[] = [
    {
      id: "p-script",
      orgId: "o",
      name: "脚本默认",
      content: "",
      style: "",
      kind: "script",
      isDefault: true,
      createdAt: "",
      updatedAt: "",
    },
    {
      id: "p-story",
      orgId: "o",
      name: "分镜默认",
      content: "",
      style: "",
      kind: "storyboard",
      isDefault: true,
      createdAt: "",
      updatedAt: "",
    },
  ]

  it("returns script-1 → storyboard-1 with dependsOn=['script-1']", () => {
    const nodes = standardPipeline(prompts)
    expect(nodes.map((n) => n.id)).toEqual(["script-1", "storyboard-1"])
    expect(nodes[0].dependsOn).toEqual([])
    expect(nodes[1].dependsOn).toEqual(["script-1"])
  })

  it("resolves per-kind default promptId from prompts", () => {
    const nodes = standardPipeline(prompts)
    expect(nodes[0].promptId).toBe("p-script")
    expect(nodes[1].promptId).toBe("p-story")
  })

  it("seeds positions and round-trips through toReactFlow", () => {
    const nodes = standardPipeline(prompts)
    expect(nodes.every((n) => n.position != null)).toBe(true)
    const { nodes: rfNodes, edges } = toReactFlow(nodes)
    expect(rfNodes).toHaveLength(2)
    expect(edges).toHaveLength(1)
    expect(edges[0]).toMatchObject({ source: "script-1", target: "storyboard-1" })
  })

  it("empty prompts → empty promptId", () => {
    const nodes = standardPipeline(undefined)
    expect(nodes[0].promptId).toBe("")
    expect(nodes[1].promptId).toBe("")
  })
})

// 键盘删除（T3.3）：删一个节点后，画布层 onNodesDelete 把它关联的边过滤掉
//（边是 dependsOn 真源 → 依赖随之清理）。此处验证「过滤关联边 + 反向转换」纯逻辑。
describe("keyboard delete cascade (node delete removes incident edges)", () => {
  it("removes incident edges so dependsOn cleans up", () => {
    const three: WorkflowNode[] = [
      { id: "a", type: "script", promptId: "", dependsOn: [] },
      { id: "b", type: "storyboard", promptId: "", dependsOn: ["a"] },
      { id: "c", type: "asset", promptId: "", dependsOn: ["b"] },
    ]
    const { nodes, edges } = toReactFlow(three)
    // 删除节点 b：节点过滤 + 关联边（a->b、b->c）过滤。
    const deletedIds = new Set(["b"])
    const remainingNodes = nodes.filter((n) => !deletedIds.has(n.id))
    const remainingEdges = edges.filter(
      (e) => !deletedIds.has(e.source) && !deletedIds.has(e.target),
    )
    expect(remainingNodes.map((n) => n.id).sort()).toEqual(["a", "c"])
    expect(remainingEdges).toHaveLength(0)
    const out = toStudioNodes(remainingNodes, remainingEdges)
    // c 之前依赖 b，b 被删后依赖清空。
    expect(out.find((n) => n.id === "c")!.dependsOn).toEqual([])
    expect(out.find((n) => n.id === "a")!.dependsOn).toEqual([])
  })

  it("deleting a selected edge removes just that dependency", () => {
    const ab: WorkflowNode[] = [
      { id: "A", type: "script", promptId: "", dependsOn: [] },
      { id: "B", type: "storyboard", promptId: "", dependsOn: ["A"] },
    ]
    const { nodes, edges } = toReactFlow(ab)
    // 删边 A->B（ReactFlow onEdgesChange 内建移除）→ 反向转换 B 依赖清空。
    const remaining = edges.filter((e) => e.id !== "A->B")
    const out = toStudioNodes(nodes, remaining)
    expect(out.find((n) => n.id === "B")!.dependsOn).toEqual([])
  })
})

// 自动整理（A3）：onAutoTidy 用当前 RF 状态反推 studio 模型，再跑 seedPositions
// 仅覆盖坐标。此处验证「toStudioNodes → seedPositions」对扇出图给出分层坐标。
describe("auto-tidy layout (seedPositions over toStudioNodes)", () => {
  it("re-layers a fan-out graph into layered coords using live edges", () => {
    // root → {child-a, child-b}（root 为上游，两子节点同层并排）。
    const fan: WorkflowNode[] = [
      { id: "root", type: "script", promptId: "", dependsOn: [] },
      { id: "child-a", type: "storyboard", promptId: "", dependsOn: ["root"] },
      { id: "child-b", type: "storyboard", promptId: "", dependsOn: ["root"] },
    ]
    const { nodes, edges } = toReactFlow(fan)
    // 模拟用户把节点拖到杂乱坐标后点「自动整理」。
    const messy = nodes.map((n) => ({
      ...n,
      position: { x: 777, y: 999 },
    }))
    const seeded = seedPositions(toStudioNodes(messy, edges))
    // root 在第 0 层，两子节点在第 1 层（y=140）并排（x=0 与 x=240）。
    expect(seeded.get("root")).toEqual({ x: 0, y: 0 })
    const a = seeded.get("child-a")!
    const b = seeded.get("child-b")!
    expect(a.y).toBe(140)
    expect(b.y).toBe(140)
    expect(new Set([a.x, b.x])).toEqual(new Set([0, 240]))
  })
})

describe("connect / cycle guard (edges authoritative for dependsOn)", () => {
  it("connecting A->B makes toStudioNodes give B.dependsOn=[A]", () => {
    const two: WorkflowNode[] = [
      { id: "A", type: "script", promptId: "", dependsOn: [] },
      { id: "B", type: "storyboard", promptId: "", dependsOn: [] },
    ]
    const { nodes } = rf(two)
    const edges: RFEdge[] = [{ id: "A->B", source: "A", target: "B" }]
    const out = toStudioNodes(nodes, edges)
    expect(out.find((n) => n.id === "B")!.dependsOn).toEqual(["A"])
  })

  it("rejects a back-edge that would create a cycle", () => {
    const ab: WorkflowNode[] = [
      { id: "A", type: "script", promptId: "", dependsOn: [] },
      { id: "B", type: "storyboard", promptId: "", dependsOn: ["A"] },
    ]
    const { nodes } = rf(ab)
    // 现有 A->B；候选再加 B->A。
    const candidate = toStudioNodes(nodes, [
      { id: "A->B", source: "A", target: "B" },
      { id: "B->A", source: "B", target: "A" },
    ])
    expect(findGraphError(candidate)).not.toBeNull()
  })

  it("disconnecting an edge removes the dependency", () => {
    const ab: WorkflowNode[] = [
      { id: "A", type: "script", promptId: "", dependsOn: [] },
      { id: "B", type: "storyboard", promptId: "", dependsOn: ["A"] },
    ]
    const { nodes } = rf(ab)
    const out = toStudioNodes(nodes, []) // 边删空。
    expect(out.find((n) => n.id === "B")!.dependsOn).toEqual([])
  })
})

// 小工具：构造一个最小 RFNode（仅 id/position/data.node 关心）。
function mkNode(id: string, pos = { x: 0, y: 0 }, type = "script"): RFNode {
  return {
    id,
    type: "studio",
    position: pos,
    data: { node: { id, type, promptId: "", dependsOn: [] } },
  }
}

describe("nextNodeId", () => {
  it("returns node-1 for an empty graph", () => {
    expect(nextNodeId([])).toBe("node-1")
  })

  it("returns a unique id not colliding with existing ids", () => {
    const nodes = [mkNode("node-1"), mkNode("node-3")]
    const id = nextNodeId(nodes)
    // 第一个空位是 node-2。
    expect(id).toBe("node-2")
    expect(nodes.some((n) => n.id === id)).toBe(false)
  })

  it("skips occupied low ids", () => {
    const nodes = [mkNode("node-1"), mkNode("node-2")]
    expect(nextNodeId(nodes)).toBe("node-3")
  })
})

describe("addNodeAt id override", () => {
  it("uses the provided id instead of generating one", () => {
    const next = addNodeAt([mkNode("node-1")], "asset", { x: 5, y: 6 }, undefined, "custom-id")
    const added = next[next.length - 1]
    expect(added.id).toBe("custom-id")
    expect(added.data.node.id).toBe("custom-id")
    expect(added.position).toEqual({ x: 5, y: 6 })
  })

  it("falls back to nextNodeId when no id is given", () => {
    const next = addNodeAt([mkNode("node-1")], "asset", { x: 0, y: 0 })
    expect(next[next.length - 1].id).toBe("node-2")
  })
})

describe("duplicateNode", () => {
  it("clones with a fresh unique id, updated data.node.id, +40/+40 offset, no deps, no edges", () => {
    const nodes: RFNode[] = [
      {
        id: "node-1",
        type: "studio",
        position: { x: 100, y: 200 },
        data: {
          node: {
            id: "node-1",
            type: "storyboard",
            promptId: "p-1",
            promptText: "hi",
            dependsOn: ["x"],
          },
        },
      },
    ]
    const { nodes: out, id } = duplicateNode(nodes, "node-1")
    expect(out).toHaveLength(2)
    const clone = out[out.length - 1]
    expect(id).toBe("node-2")
    expect(clone.id).toBe("node-2")
    expect(clone.data.node.id).toBe("node-2")
    expect(clone.position).toEqual({ x: 140, y: 240 })
    // 副本未连接：dependsOn 清空。
    expect(clone.data.node.dependsOn).toEqual([])
    // 其余字段保留（type/prompt）。
    expect(clone.data.node.type).toBe("storyboard")
    expect(clone.data.node.promptId).toBe("p-1")
    // 原节点未被改动。
    expect(out[0].data.node.dependsOn).toEqual(["x"])
    expect(out[0].position).toEqual({ x: 100, y: 200 })
  })

  it("is a no-op when the id is not found", () => {
    const nodes = [mkNode("node-1")]
    const { nodes: out, id } = duplicateNode(nodes, "missing")
    expect(out).toBe(nodes)
    expect(id).toBe("missing")
  })
})

describe("insertNodeOnEdge", () => {
  it("splits A->B into A->N and N->B with correct ids/type and N at midPos", () => {
    const ab: WorkflowNode[] = [
      { id: "A", type: "script", promptId: "", dependsOn: [] },
      { id: "B", type: "storyboard", promptId: "", dependsOn: ["A"] },
    ]
    const { nodes, edges } = toReactFlow(ab)
    const out = insertNodeOnEdge(nodes, edges, "A->B", "asset", { x: 50, y: 70 })
    expect(out.newId).toBe("node-1")
    // 原 A->B 已删。
    expect(out.edges.find((e) => e.id === "A->B")).toBeUndefined()
    const an = out.edges.find((e) => e.id === "A->node-1")!
    const nb = out.edges.find((e) => e.id === "node-1->B")!
    expect(an).toMatchObject({ source: "A", target: "node-1", type: "studio" })
    expect(nb).toMatchObject({ source: "node-1", target: "B", type: "studio" })
    const n = out.nodes.find((x) => x.id === "node-1")!
    expect(n.position).toEqual({ x: 50, y: 70 })
    // 反向转换：N.dependsOn=[A]，B.dependsOn=[N]。
    const studio = toStudioNodes(out.nodes, out.edges)
    expect(studio.find((x) => x.id === "node-1")!.dependsOn).toEqual(["A"])
    expect(studio.find((x) => x.id === "B")!.dependsOn).toEqual(["node-1"])
  })

  it("is a no-op when the edge id is not found", () => {
    const { nodes, edges } = toReactFlow([
      { id: "A", type: "script", promptId: "", dependsOn: [] },
    ])
    const out = insertNodeOnEdge(nodes, edges, "missing", "asset", { x: 0, y: 0 })
    expect(out.nodes).toBe(nodes)
    expect(out.edges).toBe(edges)
    expect(out.newId).toBe("")
  })
})

describe("cloneSelection (C2 clipboard)", () => {
  it("clones {A,B} with internal edge A->B, drops incoming X->A, re-keys to fresh ids", () => {
    // 图：X->A->B。选区 {A,B}，内部边 A->B；外部入边 X->A 应被丢弃。
    const graph: WorkflowNode[] = [
      { id: "X", type: "script", promptId: "", dependsOn: [] },
      { id: "A", type: "storyboard", promptId: "", dependsOn: ["X"] },
      { id: "B", type: "asset", promptId: "", dependsOn: ["A"] },
    ]
    const { nodes, edges } = toReactFlow(graph)
    const sel = new Set(["A", "B"])
    const { nodes: cloned, edges: clonedEdges } = cloneSelection(
      nodes,
      edges,
      sel,
      { x: 50, y: 60 },
    )
    // 2 个克隆节点，id 全新且唯一，与原图不冲突。
    expect(cloned).toHaveLength(2)
    const newIds = cloned.map((n) => n.id)
    expect(new Set(newIds).size).toBe(2)
    for (const id of newIds) {
      expect(["X", "A", "B"]).not.toContain(id)
    }
    // 1 条克隆边（A->B 重键），type studio；X->A 被丢弃。
    expect(clonedEdges).toHaveLength(1)
    expect(clonedEdges[0].type).toBe("studio")
    // position 偏移；data.node.id 同步；selected。
    for (const n of cloned) {
      expect(n.selected).toBe(true)
      expect(n.data.node.id).toBe(n.id)
    }
    const aClone = cloned.find((n) => n.data.node.type === "storyboard")!
    const aSrc = nodes.find((n) => n.id === "A")!
    expect(aClone.position).toEqual({
      x: aSrc.position.x + 50,
      y: aSrc.position.y + 60,
    })
    // 反向转换：B'.dependsOn=[A']，A'.dependsOn=[]（外部依赖被切断）。
    const bClone = cloned.find((n) => n.data.node.type === "asset")!
    const studio = toStudioNodes(cloned, clonedEdges)
    expect(studio.find((n) => n.id === bClone.id)!.dependsOn).toEqual([
      aClone.id,
    ])
    expect(studio.find((n) => n.id === aClone.id)!.dependsOn).toEqual([])
  })

  it("allocates ids against existing canvas nodes (no collision)", () => {
    // 选区只有一个节点 node-1；现有画布已占 node-1/node-2。粘贴时须避让到 node-3。
    const sel: RFNode[] = [mkNode("node-1")]
    const existing: RFNode[] = [mkNode("node-1"), mkNode("node-2")]
    const { nodes: cloned } = cloneSelection(
      sel,
      [],
      new Set(["node-1"]),
      { x: 32, y: 32 },
      undefined,
      existing,
    )
    expect(cloned).toHaveLength(1)
    expect(cloned[0].id).toBe("node-3")
    expect(cloned[0].data.node.id).toBe("node-3")
  })

  it("assigns distinct ids within one batch (no intra-batch dup)", () => {
    const sel: RFNode[] = [mkNode("node-1"), mkNode("node-2")]
    const { nodes: cloned } = cloneSelection(
      sel,
      [],
      new Set(["node-1", "node-2"]),
      { x: 32, y: 32 },
    )
    const ids = cloned.map((n) => n.id)
    expect(new Set(ids).size).toBe(2)
    // 都不与原选区 id 冲突。
    for (const id of ids) expect(["node-1", "node-2"]).not.toContain(id)
  })
})

describe("getHelperLines (C3 alignment guides)", () => {
  // 默认节点尺寸 180x64（与 canvasModel 内常量一致）。
  it("returns a vertical guide + snapX when within threshold of another's left edge", () => {
    const dragged = mkNode("d", { x: 103, y: 500 })
    const other = mkNode("o", { x: 100, y: 0 })
    const l = getHelperLines(dragged, [other], 5)
    // 被拖左边 103 与目标左边 100 相距 3 < 5 → 竖线在 100，snapX=100。
    expect(l.vertical).toBe(100)
    expect(l.snapX).toBe(100)
  })

  it("aligns centers → guide at the shared center x (different widths)", () => {
    // other 宽 100（中心 = x+50），dragged 宽 180（中心 = x+90）。
    // other 左上角 200 → 中心 250；dragged 左上角 161 → 中心 251，与 250 相距 1（< 阈值）。
    // 左边 161 vs 200（差 39）、右边 341 vs 300（差 41）均 > 阈值 → 唯一命中中心对齐 → 竖线 250。
    const dragged = mkNode("d", { x: 161, y: 500 })
    const other = { ...mkNode("o", { x: 200, y: 0 }), width: 100 } as RFNode
    const l = getHelperLines(dragged, [other], 5)
    expect(l.vertical).toBe(250)
    // snapX 使被拖中心(251)对齐到 250 → 左上角 161 + (250-251) = 160。
    expect(l.snapX).toBe(160)
  })

  it("returns empty when outside threshold", () => {
    const dragged = mkNode("d", { x: 500, y: 500 })
    const other = mkNode("o", { x: 0, y: 0 })
    const l = getHelperLines(dragged, [other], 5)
    expect(l.vertical).toBeUndefined()
    expect(l.horizontal).toBeUndefined()
    expect(l.snapX).toBeUndefined()
    expect(l.snapY).toBeUndefined()
  })

  it("ignores the dragged node itself", () => {
    const dragged = mkNode("d", { x: 100, y: 100 })
    const l = getHelperLines(dragged, [dragged], 5)
    expect(l.vertical).toBeUndefined()
    expect(l.horizontal).toBeUndefined()
  })
})

describe("reconnectEdge", () => {
  it("removes the old edge and adds a re-keyed edge for the new connection", () => {
    const { edges } = toReactFlow(chain) // script-1->storyboard-1, storyboard-1->asset-1
    const next = reconnectEdge(edges as RFEdge[], "storyboard-1->asset-1", {
      source: "script-1",
      target: "asset-1",
    })
    const ids = next.map((e) => e.id).sort()
    expect(ids).toEqual(["script-1->asset-1", "script-1->storyboard-1"])
    const re = next.find((e) => e.id === "script-1->asset-1")
    expect(re).toMatchObject({ source: "script-1", target: "asset-1", type: "studio" })
  })

  it("produces a candidate graph findGraphError can reject when reconnect would cycle", () => {
    const { nodes, edges } = toReactFlow(chain)
    // 重连 script-1->storyboard-1 为 asset-1->storyboard-1：保留 storyboard-1->asset-1，
    // 新增反向边 → storyboard-1 ↔ asset-1 成环。
    const candidateEdges = reconnectEdge(edges as RFEdge[], "script-1->storyboard-1", {
      source: "asset-1",
      target: "storyboard-1",
    })
    const err = findGraphError(toStudioNodes(nodes as RFNode[], candidateEdges))
    expect(err).toBeTruthy()
  })

  it("leaves other edges untouched and is a no-op id-wise when old id is absent", () => {
    const { edges } = toReactFlow(chain)
    const next = reconnectEdge(edges as RFEdge[], "missing->edge", {
      source: "script-1",
      target: "asset-1",
    })
    expect(next).toHaveLength(3)
  })

  it("dedups: reconnecting onto an already-connected pair yields no duplicate edge id", () => {
    // 链 + 额外 script-1->asset-1；把 storyboard-1->asset-1 重连成 script-1->asset-1（已存在）。
    const { edges } = toReactFlow(chain)
    const withExtra = [
      ...(edges as RFEdge[]),
      { id: "script-1->asset-1", source: "script-1", target: "asset-1", type: "studio" },
    ]
    const next = reconnectEdge(withExtra, "storyboard-1->asset-1", {
      source: "script-1",
      target: "asset-1",
    })
    const ids = next.map((e) => e.id)
    expect(ids.filter((id) => id === "script-1->asset-1")).toHaveLength(1)
  })
})

describe("rename cascade (re-key edges)", () => {
  it("toStudioNodes follows the new id after edges are re-keyed", () => {
    // 重命名把节点 A → A2，并把以 A 为端点的边重键。
    const nodes: RFNode[] = [
      {
        id: "A2",
        type: "studio",
        position: { x: 0, y: 0 },
        data: { node: { id: "A2", type: "script", promptId: "", dependsOn: [] } },
      },
      {
        id: "B",
        type: "studio",
        position: { x: 0, y: 0 },
        data: { node: { id: "B", type: "storyboard", promptId: "", dependsOn: [] } },
      },
    ]
    const edges: RFEdge[] = [{ id: "A2->B", source: "A2", target: "B" }]
    const out = toStudioNodes(nodes, edges)
    expect(out.map((n) => n.id).sort()).toEqual(["A2", "B"])
    expect(out.find((n) => n.id === "B")!.dependsOn).toEqual(["A2"])
  })
})

describe("createNode", () => {
  it("with a source: adds a node and an edge source->newId", () => {
    const { nodes, edges } = toReactFlow(chain)
    const res = createNode(
      nodes as RFNode[],
      edges as RFEdge[],
      "asset",
      { x: 10, y: 20 },
      undefined,
      "asset-1",
    )
    expect(res.nodes).toHaveLength(4)
    const added = res.nodes.find((n) => n.id === res.newId)
    expect(added?.data.node.type).toBe("asset")
    expect(added?.position).toEqual({ x: 10, y: 20 })
    expect(res.edges.map((e) => e.id)).toContain(`asset-1->${res.newId}`)
  })

  it("without a source: adds only a node, no new edge", () => {
    const { nodes, edges } = toReactFlow(chain)
    const res = createNode(
      nodes as RFNode[],
      edges as RFEdge[],
      "script",
      { x: 0, y: 0 },
    )
    expect(res.nodes).toHaveLength(4)
    expect(res.edges).toHaveLength(edges.length) // unchanged
  })

  it("assigns a fresh non-colliding id", () => {
    const { nodes, edges } = toReactFlow(chain)
    const res = createNode(nodes as RFNode[], edges as RFEdge[], "script", { x: 0, y: 0 })
    expect(nodes.map((n) => n.id)).not.toContain(res.newId)
  })

  // B4.4：http 注册表类型走与 llm 完全相同的 typed-node 路径——
  // 传 display.typeId（= 注册表条目 id）即产出带 typeId 的可运行 typed 节点。
  it("an http registry type (display.typeId set) produces a typed node with its typeId", () => {
    const { nodes, edges } = toReactFlow(chain)
    const res = createNode(
      nodes as RFNode[],
      edges as RFEdge[],
      "custom:weather",
      { x: 5, y: 5 },
      undefined,
      undefined,
      { label: "天气查询", color: "#22b8a6", typeId: "reg-http-1" },
    )
    const added = res.nodes.find((n) => n.id === res.newId)
    expect(added?.data.node.typeId).toBe("reg-http-1")
    expect(added?.data.node.type).toBe("custom:weather")
    expect(added?.data.node.label).toBe("天气查询")
  })
})

describe("custom-type registry + cascade", () => {
  const mk = (id: string, type: string, label?: string, color?: string): RFNode => ({
    id, type: "studio", position: { x: 0, y: 0 },
    data: { node: { id, type, promptId: "", dependsOn: [], ...(label ? { label } : {}), ...(color ? { color } : {}) } },
  })

  it("collectCustomTypes dedupes custom nodes by type", () => {
    const nodes = [mk("a", "custom:t", "翻译", "#111111"), mk("b", "custom:t", "翻译", "#111111"), mk("s", "script")]
    const types = collectCustomTypes(nodes)
    expect(types).toHaveLength(1)
    expect(types[0]).toEqual({ type: "custom:t", label: "翻译", color: "#111111" })
  })

  it("applyTypeDisplay updates label/color on every same-type node", () => {
    const nodes = [mk("a", "custom:t", "old", "#111111"), mk("b", "custom:t", "old", "#111111"), mk("s", "script")]
    const next = applyTypeDisplay(nodes, "custom:t", "新名", "#222222")
    const changed = next.filter((n) => n.data.node.type === "custom:t")
    expect(changed.every((n) => n.data.node.label === "新名" && n.data.node.color === "#222222")).toBe(true)
    expect(next.find((n) => n.id === "s")!.data.node.label).toBeUndefined()
  })

  describe("hasUnboundCustomNode (run-gate predicate)", () => {
    // helper that sets typeId on the data node
    const mkTyped = (id: string, type: string, typeId: string): RFNode => ({
      id, type: "studio", position: { x: 0, y: 0 },
      data: { node: { id, type, promptId: "", dependsOn: [], typeId } },
    })

    it("returns false for no custom nodes", () => {
      expect(hasUnboundCustomNode([mk("s", "script")])).toBe(false)
    })

    it("returns false for a typed custom node (has typeId)", () => {
      expect(hasUnboundCustomNode([mkTyped("c", "custom:translate", "reg-abc")])).toBe(false)
    })

    it("returns true for an annotation custom node (no typeId)", () => {
      expect(hasUnboundCustomNode([mk("c", "custom:annot")])).toBe(true)
    })

    it("returns true for a mix of typed + annotation custom nodes", () => {
      expect(
        hasUnboundCustomNode([
          mkTyped("c1", "custom:translate", "reg-abc"),
          mk("c2", "custom:annot"),
        ]),
      ).toBe(true)
    })

    it("returns false for only typed custom nodes (workflow is runnable)", () => {
      expect(
        hasUnboundCustomNode([
          mk("s", "script"),
          mkTyped("c1", "custom:translate", "reg-abc"),
        ]),
      ).toBe(false)
    })
  })

  it("createNode threads display onto the new node", () => {
    const res = createNode([], [], "custom:t", { x: 0, y: 0 }, undefined, undefined, { label: "翻译", color: "#333333" })
    const n = res.nodes[0].data.node
    expect(n.label).toBe("翻译")
    expect(n.color).toBe("#333333")
  })

  it("createNode with display.typeId sets typeId on the created node (Task 13)", () => {
    const res = createNode([], [], "custom:translate", { x: 0, y: 0 }, undefined, undefined, {
      label: "翻译",
      color: "#7c93ff",
      typeId: "reg-abc",
    })
    const n = res.nodes[0].data.node
    expect(n.typeId).toBe("reg-abc")
    expect(n.label).toBe("翻译")
    // toStudioNodes preserves typeId (already covered by T1 test, verify here end-to-end)
    const { nodes: rfn, edges: rfe } = toReactFlow([n])
    const out = toStudioNodes(rfn as RFNode[], rfe as RFEdge[])
    expect(out[0].typeId).toBe("reg-abc")
  })

  it("addNodeAt with display.typeId sets typeId on the new node", () => {
    const nodes = addNodeAt([], "custom:t", { x: 10, y: 20 }, undefined, undefined, {
      label: "测试",
      color: "#aabbcc",
      typeId: "reg-xyz",
    })
    expect(nodes[0].data.node.typeId).toBe("reg-xyz")
  })
})

describe("typeId + varBindings round-trip (T1)", () => {
  it("preserves typeId + varBindings on a typed custom node round-trip (T1)", () => {
    const typed: WorkflowNode[] = [
      {
        id: "n1",
        type: "custom:translate",
        typeId: "reg-123",
        varBindings: [{ name: "draft", sourceNodeId: "script-1" }],
        promptId: "",
        dependsOn: ["script-1"],
        label: "翻译",
        color: "#7c93ff",
      },
    ]
    const { nodes, edges } = toReactFlow(typed)
    const out = toStudioNodes(nodes as RFNode[], edges as RFEdge[])
    expect(out[0].typeId).toBe("reg-123")
    expect(out[0].varBindings).toEqual([{ name: "draft", sourceNodeId: "script-1" }])
    expect(out[0].label).toBe("翻译")
  })
})

describe("typed node palette entry stability (Important 3)", () => {
  // Once a typed node of slug 'translate' is on the canvas, the merged
  // customTypes for that slug must still carry typeId (registry entry wins).
  // Previously collectCustomTypes was fed ALL nodes including typed ones, which
  // produced an annotation-shaped entry (no typeId) that then blocked the typed
  // registry entry via allAnnotationTypes.has(t.type).
  it("typed node on canvas does not shadow its registry entry (slug still carries typeId)", () => {
    // Simulate one typed node already placed on the canvas.
    const typedOnCanvas = mkNode("n1", { x: 0, y: 0 }, "custom:translate") as RFNode
    typedOnCanvas.data.node.typeId = "reg-abc"
    typedOnCanvas.data.node.label = "翻译"
    typedOnCanvas.data.node.color = "#7c93ff"

    // Only annotation nodes (no typeId) should feed collectCustomTypes.
    const annotationOnly = [typedOnCanvas].filter((n) => !n.data.node.typeId)
    const annotation = collectCustomTypes(annotationOnly)

    // The typed registry entry.
    const registryTyped = [{ type: "custom:translate", label: "翻译", color: "#7c93ff", typeId: "reg-abc" }]

    const allAnnotationTypes = new Set(annotation.map((a) => a.type))
    const mergedTyped = registryTyped.filter((t) => !allAnnotationTypes.has(t.type))
    const merged = [...annotation, ...mergedTyped]

    const entry = merged.find((e) => e.type === "custom:translate")
    expect(entry).toBeDefined()
    expect((entry as typeof registryTyped[0]).typeId).toBe("reg-abc")
  })
})

describe("insertNodeOnEdge threads typeId (Nit 4)", () => {
  it("inserting a typed node on an edge sets typeId on the new node", () => {
    const ab: WorkflowNode[] = [
      { id: "A", type: "script", promptId: "", dependsOn: [] },
      { id: "B", type: "storyboard", promptId: "", dependsOn: ["A"] },
    ]
    const { nodes, edges } = toReactFlow(ab)
    const display = { label: "翻译", color: "#7c93ff", typeId: "reg-xyz" }
    const out = insertNodeOnEdge(nodes, edges, "A->B", "custom:translate", { x: 50, y: 70 }, undefined, display)
    const inserted = out.nodes.find((n) => n.id === out.newId)!
    expect(inserted.data.node.typeId).toBe("reg-xyz")
  })
})

describe("custom node label/color round-trip", () => {
  it("toStudioNodes carries label+color for custom nodes, omits for builtin", () => {
    const nodes: RFNode[] = [
      {
        id: "c1", type: "studio", position: { x: 0, y: 0 },
        data: { node: { id: "c1", type: "custom:translate", promptId: "", dependsOn: [], label: "翻译", color: "#7c93ff" } },
      },
      {
        id: "s1", type: "studio", position: { x: 0, y: 0 },
        data: { node: { id: "s1", type: "script", promptId: "", dependsOn: [] } },
      },
    ]
    const out = toStudioNodes(nodes, [])
    const c = out.find((n) => n.id === "c1")!
    expect(c.label).toBe("翻译")
    expect(c.color).toBe("#7c93ff")
    const s = out.find((n) => n.id === "s1")!
    expect(s.label).toBeUndefined()
    expect(s.color).toBeUndefined()
  })

  it("toReactFlow preserves label/color into data.node", () => {
    const { nodes } = toReactFlow([
      { id: "c1", type: "custom:translate", promptId: "", dependsOn: [], label: "翻译", color: "#7c93ff" },
    ])
    expect(nodes[0].data.node.label).toBe("翻译")
    expect(nodes[0].data.node.color).toBe("#7c93ff")
  })
})
