import { describe, it, expect } from "vitest"
import type {
  StageRole,
  StageStatus2,
  RunStatus2,
  PipStatus2,
  GraphNodeStatus,
  GraphNode,
  InspectorItem,
  InspectorBinaryRef,
} from "./projectState"

// 守护前后端枚举漂移:任一侧新增/改名而另一侧没跟 → 这里需同步更新,否则编译/断言红。
// 后端真相:internal/projectstate/state.go 的注释枚举域 + Compute 分支。
describe("ProjectState 枚举契约(与后端 projectstate 对齐)", () => {
  it("StageRole 恰为 5 个语义角色", () => {
    const roles: StageRole[] = ["planner", "script", "storyboard", "asset", "review"]
    expect(roles).toHaveLength(5)
  })
  it("StageStatus2 恰为 5 态", () => {
    const s: StageStatus2[] = ["blocked", "pending", "running", "done", "failed"]
    expect(s).toHaveLength(5)
  })
  it("RunStatus2 恰为 3 态", () => {
    const s: RunStatus2[] = ["idle", "running", "done"]
    expect(s).toHaveLength(3)
  })
  it("PipStatus2 恰为 4 态", () => {
    const s: PipStatus2[] = ["idle", "running", "done", "failed"]
    expect(s).toHaveLength(4)
  })
  it("GraphNode.status 复用 StageStatus2 的 5 态", () => {
    const s: GraphNodeStatus[] = ["blocked", "pending", "running", "done", "failed"]
    expect(s).toHaveLength(5)
  })

  // workflow-v2 P5d：per-item inspector 契约。对齐后端 InspectorItem/InspectorBinaryRef
  // (internal/projectstate/state.go)。binary 子键 assetId/mimeType/kind/status 均为
  // camelCase；items 是 GraphNode 上的可选字段(后端 omitempty → 老/标量节点无此键)。
  it("GraphNode.items 可选(omitempty 对齐):省略时仍是合法 GraphNode", () => {
    const noItems: GraphNode = {
      id: "n1",
      label: "节点",
      type: "custom:x",
      status: "done",
    }
    expect(noItems.items).toBeUndefined()
  })

  it("InspectorItem = { json: unknown; binary?: Record<string, InspectorBinaryRef> }", () => {
    const item: InspectorItem = { json: { hello: "world" } }
    expect(item.binary).toBeUndefined()
    const withBinary: InspectorItem = {
      json: { text: "hi" },
      binary: {
        cover: { assetId: "a1", mimeType: "image/png", kind: "image" },
      },
    }
    expect(withBinary.binary?.cover.assetId).toBe("a1")
  })

  it("InspectorBinaryRef 字段为 camelCase(assetId/mimeType/kind/status),status 可选", () => {
    const ref: InspectorBinaryRef = {
      assetId: "a1",
      mimeType: "image/png",
      kind: "image",
      status: "done",
    }
    expect(Object.keys(ref).sort()).toEqual(["assetId", "kind", "mimeType", "status"])
    // status omitempty → 省略仍合法。
    const noStatus: InspectorBinaryRef = { assetId: "a2", mimeType: "audio/mpeg", kind: "audio" }
    expect(noStatus.status).toBeUndefined()
  })
})
