import { describe, it, expect } from "vitest"
import { pictureBookRunSchema } from "./pbRunSchema"
import type { PictureBookConfig } from "@/lib/types"

// 该派生 schema 必须与后端 internal/runinputs.PictureBookSchema 对称：
// 字段顺序 voice/themes/ageBand/bookType/illustrationStyle/narrationStyle/pageCount，
// 全 target=pbConfig，字符串字段 select、themes multiselect、pageCount number，
// default 取当前 cfg 值（JSON 字面量）。不对称会导致提交被后端 400。

const CFG: PictureBookConfig = {
  ageBand: "3-6",
  bookType: "narrative",
  illustrationStyle: "watercolor",
  narrationStyle: "plain",
  themes: ["friendship", "courage"],
  pageCount: 16,
  voice: "warm",
}

describe("pictureBookRunSchema (与后端 PictureBookSchema 对称)", () => {
  it("派生 7 字段，顺序/name/type/target 与后端一致", () => {
    const s = pictureBookRunSchema(CFG)
    expect(s.map((f) => f.name)).toEqual([
      "voice",
      "themes",
      "ageBand",
      "bookType",
      "illustrationStyle",
      "narrationStyle",
      "pageCount",
    ])
    // 全部 target=pbConfig。
    expect(s.every((f) => f.target === "pbConfig")).toBe(true)
    // 字符串字段 select；themes multiselect；pageCount number。
    expect(s.find((f) => f.name === "voice")?.type).toBe("select")
    expect(s.find((f) => f.name === "ageBand")?.type).toBe("select")
    expect(s.find((f) => f.name === "bookType")?.type).toBe("select")
    expect(s.find((f) => f.name === "illustrationStyle")?.type).toBe("select")
    expect(s.find((f) => f.name === "narrationStyle")?.type).toBe("select")
    expect(s.find((f) => f.name === "themes")?.type).toBe("multiselect")
    expect(s.find((f) => f.name === "pageCount")?.type).toBe("number")
  })

  it("无任何 text 字段（注入边界：字符串字段永不为 text）", () => {
    const s = pictureBookRunSchema(CFG)
    expect(s.some((f) => f.type === "text" || f.type === "textarea")).toBe(false)
  })

  it("select/multiselect 带非空 options（themes=16，ageBand=3，bookType=10 等）", () => {
    const s = pictureBookRunSchema(CFG)
    expect(s.find((f) => f.name === "themes")?.options).toHaveLength(16)
    expect(s.find((f) => f.name === "ageBand")?.options).toHaveLength(3)
    expect(s.find((f) => f.name === "bookType")?.options).toHaveLength(10)
    expect(s.find((f) => f.name === "illustrationStyle")?.options).toHaveLength(8)
    expect(s.find((f) => f.name === "narrationStyle")?.options).toHaveLength(4)
  })

  it("default 取当前 cfg 值（JSON 字面量，与后端 jsonString/Marshal 对齐）", () => {
    const s = pictureBookRunSchema(CFG)
    const byName = (n: string) => s.find((f) => f.name === n)
    expect(byName("voice")?.default).toBe(JSON.stringify("warm"))
    expect(byName("themes")?.default).toBe(JSON.stringify(["friendship", "courage"]))
    expect(byName("ageBand")?.default).toBe(JSON.stringify("3-6"))
    expect(byName("pageCount")?.default).toBe(JSON.stringify(16))
  })

  it("voice 空值时 options 退化为单个空值选项（与后端空值兜底一致）", () => {
    const s = pictureBookRunSchema({ ...CFG, voice: "" })
    expect(s.find((f) => f.name === "voice")?.options).toEqual([{ value: "" }])
    expect(s.find((f) => f.name === "voice")?.default).toBe(JSON.stringify(""))
  })

  it("voice 非空时 options 为当前值单元素（org 音色列表未接入）", () => {
    const s = pictureBookRunSchema(CFG)
    expect(s.find((f) => f.name === "voice")?.options).toEqual([
      { value: "warm", label: "warm" },
    ])
  })
})
