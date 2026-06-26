import { describe, expect, it } from "vitest"
import { toReactFlow, toStudioNodes } from "./canvasModel"
import type { WorkflowNode } from "@/lib/types"

describe("toStudioNodes parameters round-trip (B-A1)", () => {
  it("preserves parameters + typeVersion through load→save→reload", () => {
    const nodes: (WorkflowNode & Record<string, unknown>)[] = [
      {
        id: "n1",
        type: "custom:my-llm",
        promptId: "",
        typeId: "abc",
        dependsOn: [],
        typeVersion: 1,
        parameters: { temperature: 0.2, outputFormat: "json" },
        // an UNKNOWN property a future client wrote that this bundle doesn't model:
        futureField: { nested: true },
      },
    ]
    const { nodes: rf, edges } = toReactFlow(nodes as WorkflowNode[])
    const out = toStudioNodes(rf, edges) as (WorkflowNode & Record<string, unknown>)[]
    expect(out).toHaveLength(1)
    expect(out[0].parameters).toEqual({ temperature: 0.2, outputFormat: "json" })
    expect(out[0].typeVersion).toBe(1)
    // preserve-unknown: the unmodeled key survives the round-trip.
    expect(out[0].futureField).toEqual({ nested: true })
  })

  it("still derives dependsOn from edges and id from RF (existing invariants)", () => {
    const nodes: WorkflowNode[] = [
      { id: "a", type: "script", promptId: "", dependsOn: [] },
      { id: "b", type: "storyboard", promptId: "", dependsOn: ["a"] },
    ]
    const { nodes: rf, edges } = toReactFlow(nodes)
    const out = toStudioNodes(rf, edges)
    expect(out.find((n) => n.id === "b")?.dependsOn).toEqual(["a"])
  })
})
