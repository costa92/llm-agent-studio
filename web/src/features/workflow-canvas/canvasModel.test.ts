import { describe, expect, it } from "vitest"
import { toReactFlow, seedPositions } from "./canvasModel"
import type { WorkflowNode } from "@/lib/types"

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
