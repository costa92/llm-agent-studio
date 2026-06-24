import { describe, expect, it } from "vitest"
import { overlayRunStatus } from "./runOverlay"
import type { ProjectState, GraphNode } from "@/lib/projectState"
import type { WorkflowNode } from "@/lib/types"

// 最小 ProjectState 夹具：只给 overlayRunStatus 用到的 nodes，其余补默认。
function stateWith(nodes: GraphNode[]): ProjectState {
  return {
    projectId: "p1",
    version: 1,
    status: "running",
    runStatus: "running",
    stages: [],
    pips: [],
    assets: { total: 0, done: 0, pending: 0 },
    nodes,
    edges: [],
    isCustom: true,
  }
}

describe("overlayRunStatus", () => {
  it("(a) maps a standard 2-node pipeline by (type, ordinal)", () => {
    const canvas: WorkflowNode[] = [
      { id: "script-1", type: "script", promptId: "", dependsOn: [] },
      {
        id: "storyboard-1",
        type: "storyboard",
        promptId: "",
        dependsOn: ["script-1"],
      },
    ]
    const state = stateWith([
      { id: "uuidA", label: "脚本", type: "script", status: "done" },
      { id: "uuidB", label: "分镜", type: "storyboard", status: "running" },
    ])
    const map = overlayRunStatus(canvas, state)
    expect(map.get("script-1")).toEqual({ status: "done", todoId: "uuidA", assetId: undefined })
    expect(map.get("storyboard-1")).toEqual({
      status: "running",
      todoId: "uuidB",
      assetId: undefined,
    })
  })

  it("(b) maps two asset nodes to asset#0 / asset#1 by order", () => {
    const canvas: WorkflowNode[] = [
      { id: "script-1", type: "script", promptId: "", dependsOn: [] },
      { id: "asset-a", type: "asset", promptId: "", dependsOn: ["script-1"] },
      { id: "asset-b", type: "asset", promptId: "", dependsOn: ["script-1"] },
    ]
    const state = stateWith([
      { id: "uS", label: "脚本", type: "script", status: "done" },
      { id: "u0", label: "图0", type: "asset", status: "done", assetId: "asset0" },
      { id: "u1", label: "图1", type: "asset", status: "running" },
    ])
    const map = overlayRunStatus(canvas, state)
    // 画布拓扑序内 asset 序号：asset-a=0, asset-b=1。
    expect(map.get("asset-a")).toEqual({ status: "done", todoId: "u0", assetId: "asset0" })
    expect(map.get("asset-b")).toEqual({ status: "running", todoId: "u1", assetId: undefined })
  })

  it("(c) returns a partial map when state has fewer nodes than canvas (no throw)", () => {
    const canvas: WorkflowNode[] = [
      { id: "script-1", type: "script", promptId: "", dependsOn: [] },
      {
        id: "storyboard-1",
        type: "storyboard",
        promptId: "",
        dependsOn: ["script-1"],
      },
    ]
    const state = stateWith([
      { id: "uuidA", label: "脚本", type: "script", status: "done" },
    ])
    const map = overlayRunStatus(canvas, state)
    expect(map.get("script-1")).toMatchObject({ status: "done", todoId: "uuidA" })
    expect(map.has("storyboard-1")).toBe(false)
  })

  it("(d) returns an empty map for empty state.nodes", () => {
    const canvas: WorkflowNode[] = [
      { id: "script-1", type: "script", promptId: "", dependsOn: [] },
    ]
    const map = overlayRunStatus(canvas, stateWith([]))
    expect(map.size).toBe(0)
  })

  it("(e) threads output + outputFormat from state node into overlay entry", () => {
    const canvas: WorkflowNode[] = [
      { id: "custom-1", type: "custom:translate", promptId: "", dependsOn: [] },
    ]
    const state = stateWith([
      {
        id: "uuidC",
        label: "翻译",
        type: "custom:translate",
        status: "done",
        output: "Hello, world!",
        outputFormat: "text",
      },
    ])
    const map = overlayRunStatus(canvas, state)
    expect(map.get("custom-1")).toEqual({
      status: "done",
      todoId: "uuidC",
      assetId: undefined,
      output: "Hello, world!",
      outputFormat: "text",
    })
  })

  it("(f) output is absent when state node has no output (standard node)", () => {
    const canvas: WorkflowNode[] = [
      { id: "script-1", type: "script", promptId: "", dependsOn: [] },
    ]
    const state = stateWith([
      { id: "uuidA", label: "脚本", type: "script", status: "done" },
    ])
    const map = overlayRunStatus(canvas, state)
    expect(map.get("script-1")?.output).toBeUndefined()
  })
})
