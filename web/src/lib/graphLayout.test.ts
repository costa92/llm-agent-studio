import { describe, it, expect } from "vitest"
import { layerize } from "./graphLayout"
import type { GraphNode, GraphEdge } from "./projectState"

function n(id: string): GraphNode {
  return { id, label: id, type: "script", status: "done" }
}

describe("layerize", () => {
  it("空图返回空数组", () => {
    expect(layerize([], [])).toEqual([])
  })

  it("线性链分成逐层", () => {
    const nodes = [n("a"), n("b"), n("c")]
    const edges: GraphEdge[] = [
      { from: "b", to: "a" },
      { from: "c", to: "b" },
    ]
    const layers = layerize(nodes, edges)
    expect(layers.map((l) => l.map((x) => x.id))).toEqual([["a"], ["b"], ["c"]])
  })

  it("一父多子:子节点同层并排", () => {
    const nodes = [n("a"), n("b"), n("c")]
    const edges: GraphEdge[] = [
      { from: "b", to: "a" },
      { from: "c", to: "a" },
    ]
    const layers = layerize(nodes, edges)
    expect(layers[0].map((x) => x.id)).toEqual(["a"])
    expect(layers[1].map((x) => x.id).sort()).toEqual(["b", "c"])
  })

  it("多父汇聚:汇聚点落在最深父之后", () => {
    const nodes = [n("a"), n("b"), n("c"), n("d")]
    const edges: GraphEdge[] = [
      { from: "b", to: "a" },
      { from: "d", to: "a" },
      { from: "d", to: "b" },
    ]
    const layers = layerize(nodes, edges)
    const layerOf = (id: string) => layers.findIndex((l) => l.some((x) => x.id === id))
    expect(layerOf("d")).toBeGreaterThan(layerOf("b"))
    expect(layerOf("b")).toBeGreaterThan(layerOf("a"))
  })

  it("残留环不死循环(兜底返回有限层)", () => {
    const nodes = [n("a"), n("b")]
    const edges: GraphEdge[] = [
      { from: "a", to: "b" },
      { from: "b", to: "a" },
    ]
    const layers = layerize(nodes, edges)
    const ids = layers.flat().map((x) => x.id).sort()
    expect(ids).toEqual(["a", "b"])
  })
})
