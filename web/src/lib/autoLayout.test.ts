import { describe, expect, it } from "vitest"
import { autoLayout } from "./autoLayout"
import type { WorkflowNode } from "@/lib/types"

const chain: WorkflowNode[] = [
  { id: "a", type: "script", promptId: "", dependsOn: [] },
  { id: "b", type: "storyboard", promptId: "", dependsOn: ["a"] },
  { id: "c", type: "asset", promptId: "", dependsOn: ["b"] },
]

describe("autoLayout", () => {
  it("TB：层 index→y，层内 index→x（与旧 seedPositions 同口径）", () => {
    const pos = autoLayout(chain, "TB")
    expect(pos.get("a")).toEqual({ x: 0, y: 0 })
    expect(pos.get("b")).toEqual({ x: 0, y: 140 })
    expect(pos.get("c")).toEqual({ x: 0, y: 280 })
  })

  it("LR：交换主/交叉轴", () => {
    const pos = autoLayout(chain, "LR")
    expect(pos.get("a")).toEqual({ x: 0, y: 0 })
    expect(pos.get("b")).toEqual({ x: 240, y: 0 })
    expect(pos.get("c")).toEqual({ x: 480, y: 0 })
  })

  it("同层多节点：层内 index 沿交叉轴展开", () => {
    const fanout: WorkflowNode[] = [
      { id: "root", type: "script", promptId: "", dependsOn: [] },
      { id: "x", type: "asset", promptId: "", dependsOn: ["root"] },
      { id: "y", type: "asset", promptId: "", dependsOn: ["root"] },
    ]
    const tb = autoLayout(fanout, "TB")
    expect(tb.get("root")).toEqual({ x: 0, y: 0 })
    expect(tb.get("x")).toEqual({ x: 0, y: 140 })
    expect(tb.get("y")).toEqual({ x: 240, y: 140 })
  })

  it("空图返回空 map", () => {
    expect(autoLayout([], "TB").size).toBe(0)
  })
})
