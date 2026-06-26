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

  // P5d：items 透传到选中态（OQ1=所有节点类型，含 script/storyboard/asset/custom）。
  it("threads items onto a script selection (storyboard fan-out headline lives on storyboard, but all types carry items)", () => {
    const items = [{ json: { line: "一" } }]
    expect(resolveSelection("script", { status: "done", todoId: "t1", items })).toEqual({
      kind: "script",
      todoId: "t1",
      items,
    })
  })

  it("threads items onto a storyboard selection (fan-out: multiple shot items)", () => {
    const items = [{ json: { shot: 1 } }, { json: { shot: 2 } }]
    expect(
      resolveSelection("storyboard", { status: "done", todoId: "t2", items }),
    ).toEqual({ kind: "storyboard", todoId: "t2", items })
  })

  it("threads items onto an asset selection", () => {
    const items = [{ json: { text: "ok" }, binary: { img: { assetId: "a1", mimeType: "image/png", kind: "image" } } }]
    expect(
      resolveSelection("asset", { status: "done", todoId: "t3", assetId: "a1", items }),
    ).toEqual({ kind: "asset", assetId: "a1", items })
  })

  it("threads items onto a custom selection", () => {
    const items = [{ json: { text: "hi" } }]
    expect(
      resolveSelection("custom:translate", { status: "done", todoId: "t4", output: "hi", outputFormat: "text", items }),
    ).toEqual({ kind: "custom", output: "hi", outputFormat: "text", items })
  })

  it("items absent → selection has no items key (back-compat with old backend)", () => {
    expect(resolveSelection("script", { status: "done", todoId: "t1" })).toEqual({
      kind: "script",
      todoId: "t1",
    })
  })
})
