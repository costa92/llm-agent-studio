import { describe, it, expect } from "vitest"
import { projectFormSchema, defaultsFor } from "./ProjectFields.schema"
import { emptyPictureBookConfig } from "./pbConfig"

describe("projectFormSchema", () => {
  const base = {
    name: "项目",
    brief: "一句创意",
    description: "",
    contentType: "短视频",
    targetPlatform: "抖音",
    style: "写实",
    plannerProvider: "",
    plannerModel: "",
    imageProvider: "",
    imageModel: "",
    storageConfigId: "",
    kind: "standard" as const,
    pbConfig: emptyPictureBookConfig,
  }

  it("name 必填", () => {
    const r = projectFormSchema.safeParse({ ...base, name: "" })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入项目名称")).toBe(true)
    }
  })

  it("brief 必填", () => {
    const r = projectFormSchema.safeParse({ ...base, brief: "" })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入创意需求")).toBe(true)
    }
  })

  it("style 必填", () => {
    const r = projectFormSchema.safeParse({ ...base, style: "" })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请选择风格")).toBe(true)
    }
  })

  it("standard 模式合法", () => {
    const r = projectFormSchema.safeParse(base)
    expect(r.success).toBe(true)
  })

  it("picturebook 模式不要求选年龄段——空年龄段仍通过（复刻现状零前端校验）", () => {
    const r = projectFormSchema.safeParse({
      ...base,
      kind: "picturebook",
      pbConfig: { ...emptyPictureBookConfig, ageBand: "" },
    })
    expect(r.success).toBe(true)
  })
})

describe("defaultsFor", () => {
  it("无 initial 时给空表单默认（standard + 空 pbConfig）", () => {
    const d = defaultsFor()
    expect(d.kind).toBe("standard")
    expect(d.name).toBe("")
    expect(d.brief).toBe("")
    expect(d.pbConfig).toEqual(emptyPictureBookConfig)
  })

  it("有 initial 时回填（含 picturebook + 解析 pictureBookConfig 字符串）", () => {
    const d = defaultsFor({
      name: "旧名",
      description: "旧需求",
      contentType: "广告片",
      targetPlatform: "B 站",
      style: "动画",
      kind: "picturebook",
      pictureBookConfig: JSON.stringify({
        ...emptyPictureBookConfig,
        ageBand: "0-3",
        pageCount: 8,
      }),
    })
    expect(d.name).toBe("旧名")
    expect(d.description).toBe("旧需求")
    expect(d.kind).toBe("picturebook")
    expect(d.pbConfig.ageBand).toBe("0-3")
    expect(d.pbConfig.pageCount).toBe(8)
  })

  it("pictureBookConfig 非法 JSON 回退空配置", () => {
    const d = defaultsFor({ pictureBookConfig: "{bad" })
    expect(d.pbConfig).toEqual(emptyPictureBookConfig)
  })
})
