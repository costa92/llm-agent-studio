import { describe, expect, it } from "vitest"
import { isCustomType, nodeDisplay, slugify, CUSTOM_PALETTE, descTypeFor } from "./nodeColor"

// 回归：上游节点 OutputSchema 解析的命名空间桥接（field-level varBindings DOA 修复）。
// 画布存储的内置节点是 BARE 名（script/...），但 OutputSchema 在 nodedesc 的 studio.* 条目上。
// 这组测试走真实解析路径（descTypeFor + find），正是之前单测注入 outputSchema 绕过的那层。
describe("descTypeFor (builtin bare → studio.* OutputSchema bridge)", () => {
  it("maps the 4 generation builtins to their studio.* desc type", () => {
    expect(descTypeFor("script")).toBe("studio.script")
    expect(descTypeFor("storyboard")).toBe("studio.storyboard")
    expect(descTypeFor("asset")).toBe("studio.asset")
    expect(descTypeFor("prescreen")).toBe("studio.prescreen")
  })
  it("passes custom:* and non-generation types through unchanged", () => {
    expect(descTypeFor("custom:translate")).toBe("custom:translate")
    expect(descTypeFor("llm")).toBe("llm")
    expect(descTypeFor("http")).toBe("http")
  })
  it("resolves a bare script upstream to studio.script OutputSchema, NOT the schema-less Starlark script (the DOA bug)", () => {
    // 真实 /node-types 形状：同时含 Starlark 裸 script(无 schema) 与 studio.script(4 字段)。
    const descs = [
      { type: "script", outputSchema: [] as { name: string }[] }, // Starlark transform (陷阱)
      { type: "studio.script", outputSchema: [{ name: "title" }, { name: "logline" }, { name: "characterSheet" }, { name: "scenes" }] },
      { type: "studio.storyboard", outputSchema: [{ name: "shotNo" }, { name: "description" }, { name: "narration" }] },
      { type: "custom:translate", outputSchema: [{ name: "text" }] },
    ]
    const resolve = (t: string) => descs.find((d) => d.type === descTypeFor(t))?.outputSchema ?? []
    expect(resolve("script").map((f) => f.name)).toEqual(["title", "logline", "characterSheet", "scenes"])
    expect(resolve("storyboard").length).toBeGreaterThan(0)
    expect(resolve("custom:translate").map((f) => f.name)).toEqual(["text"])
  })
})

describe("isCustomType", () => {
  it("true only for custom: with non-empty slug", () => {
    expect(isCustomType("custom:translate")).toBe(true)
    expect(isCustomType("custom:")).toBe(false)
    expect(isCustomType("script")).toBe(false)
  })
})

describe("nodeDisplay", () => {
  it("builtin → table label/color", () => {
    expect(nodeDisplay({ type: "script" })).toEqual({ label: "剧本", color: "var(--script)" })
  })
  it("custom → own label/color, with fallbacks", () => {
    expect(nodeDisplay({ type: "custom:x", label: "翻译", color: "#7c93ff" })).toEqual({ label: "翻译", color: "#7c93ff" })
    const fb = nodeDisplay({ type: "custom:x" })
    expect(fb.label).toBe("自定义")
    expect(fb.color).toMatch(/^#/)
  })
})

describe("slugify", () => {
  it("normalizes to a non-empty slug", () => {
    expect(slugify("My Step")).toBe("my-step")
    expect(slugify("翻译")).toBe("翻译")
    expect(slugify("   ")).toBe("type")
  })
})

describe("CUSTOM_PALETTE", () => {
  it("is a non-empty list of hex colors", () => {
    expect(CUSTOM_PALETTE.length).toBeGreaterThan(0)
    for (const c of CUSTOM_PALETTE) expect(c).toMatch(/^#[0-9a-f]{6}$/i)
  })
})
