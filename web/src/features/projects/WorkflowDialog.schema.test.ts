import { describe, it, expect } from "vitest"
import { workflowFormSchema, findGraphError } from "./WorkflowDialog.schema"

const node = (id: string, dependsOn: string[] = []) => ({
  id,
  type: "script",
  promptId: "",
  dependsOn,
})

describe("workflowFormSchema superRefine", () => {
  it("name 空 → 请输入工作流名称", () => {
    const r = workflowFormSchema.safeParse({ name: "  ", nodes: [node("a")] })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入工作流名称")).toBe(true)
    }
  })

  it("0 节点 → 工作流必须包含至少一个节点", () => {
    const r = workflowFormSchema.safeParse({ name: "wf", nodes: [] })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(
        r.error.issues.some((i) => i.message === "工作流必须包含至少一个节点"),
      ).toBe(true)
    }
  })

  it("空 ID 节点 → 所有节点 ID 不能为空", () => {
    const r = workflowFormSchema.safeParse({ name: "wf", nodes: [node("")] })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(
        r.error.issues.some((i) => i.message === "所有节点 ID 不能为空"),
      ).toBe(true)
    }
  })

  it("重复 ID → 存在重复的节点 ID: a", () => {
    const r = workflowFormSchema.safeParse({
      name: "wf",
      nodes: [node("a"), node("a")],
    })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(
        r.error.issues.some((i) => i.message === "存在重复的节点 ID: a"),
      ).toBe(true)
    }
  })

  it("循环依赖 → findGraphError 文案", () => {
    const r = workflowFormSchema.safeParse({
      name: "wf",
      nodes: [node("A", ["B"]), node("B", ["A"])],
    })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => /循环依赖/.test(i.message))).toBe(true)
    }
  })

  it("合法线性图通过", () => {
    const r = workflowFormSchema.safeParse({
      name: "wf",
      nodes: [node("a"), node("b", ["a"])],
    })
    expect(r.success).toBe(true)
  })
})

// findGraphError 行为单测（Phase 3 从已删除的 WorkflowDialog.test.tsx 迁来）。
describe("findGraphError", () => {
  it("returns null for a valid linear graph", () => {
    const nodes = [
      { id: "a", dependsOn: [] },
      { id: "b", dependsOn: ["a"] },
      { id: "c", dependsOn: ["b"] },
    ]
    expect(findGraphError(nodes)).toBeNull()
  })

  it("returns null for an empty graph", () => {
    expect(findGraphError([])).toBeNull()
  })

  it("returns a message for a direct cycle (A↔B)", () => {
    const nodes = [
      { id: "A", dependsOn: ["B"] },
      { id: "B", dependsOn: ["A"] },
    ]
    const result = findGraphError(nodes)
    expect(result).not.toBeNull()
    expect(result).toMatch(/循环依赖/)
  })

  it("returns a message for a self-loop (A→A)", () => {
    const nodes = [{ id: "A", dependsOn: ["A"] }]
    const result = findGraphError(nodes)
    expect(result).not.toBeNull()
    expect(result).toMatch(/循环依赖/)
  })

  it("returns a message for a longer cycle (A→B→C→A)", () => {
    const nodes = [
      { id: "A", dependsOn: ["C"] },
      { id: "B", dependsOn: ["A"] },
      { id: "C", dependsOn: ["B"] },
    ]
    const result = findGraphError(nodes)
    expect(result).not.toBeNull()
    expect(result).toMatch(/循环依赖/)
  })

  it("returns a message for a dependency on an unknown node", () => {
    const nodes = [
      { id: "a", dependsOn: [] },
      { id: "b", dependsOn: ["unknown-node"] },
    ]
    const result = findGraphError(nodes)
    expect(result).not.toBeNull()
    expect(result).toContain("unknown-node")
  })
})
