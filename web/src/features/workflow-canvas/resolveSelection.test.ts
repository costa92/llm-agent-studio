import { describe, expect, it } from "vitest"
import { resolveSelection } from "./resolveSelection"

describe("resolveSelection", () => {
  it("script node → script selection with todoId", () => {
    expect(resolveSelection("script", { status: "done", todoId: "t1" })).toEqual({
      kind: "script",
      todoId: "t1",
    })
  })

  it("storyboard node → storyboard selection with todoId", () => {
    expect(resolveSelection("storyboard", { status: "running", todoId: "t2" })).toEqual({
      kind: "storyboard",
      todoId: "t2",
    })
  })

  it("asset node with assetId → asset selection", () => {
    expect(
      resolveSelection("asset", { status: "done", todoId: "t3", assetId: "a1" }),
    ).toEqual({ kind: "asset", assetId: "a1" })
  })

  it("asset node without assetId → null (nothing to view yet)", () => {
    expect(resolveSelection("asset", { status: "running", todoId: "t4" })).toBeNull()
  })

  it("undefined entry (unmatched/pending node) → null", () => {
    expect(resolveSelection("script", undefined)).toBeNull()
  })
})
