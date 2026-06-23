import { describe, expect, it } from "vitest"
import { isCustomType, nodeDisplay, slugify, CUSTOM_PALETTE } from "./nodeColor"

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
