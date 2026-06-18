import { describe, it, expect } from "vitest"
import {
  projectFormSchema,
  createProjectFormSchema,
  defaultsFor,
  serializePbConfig,
  parsePbConfig,
} from "./ProjectFields.schema"
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

  it("brief 必填（仅 Create schema）", () => {
    const r = createProjectFormSchema.safeParse({ ...base, brief: "" })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入创意需求")).toBe(true)
    }
  })

  // 回归测试：base schema 必须接受空 brief。Edit 走 base + defaultsFor(project)，
  // 而 Project 类型无 brief→defaultsFor 永远产出 brief:""；zodResolver v5 校验整份
  // form.getValues()，若 base 里 brief.min(1) 则 Edit resolver 必在 brief 上失败、
  // handleSubmit 静默不触发（Edit 错误还被藏）→ Edit 变哑弹。此测试锁住该修复。
  it("base schema 接受空 brief（Edit 场景：defaultsFor(project) 永远 brief=''）", () => {
    const r = projectFormSchema.safeParse(
      defaultsFor({
        name: "旧名",
        contentType: "短视频",
        targetPlatform: "抖音",
        style: "写实",
        kind: "standard",
      }),
    )
    expect(r.success).toBe(true)
    if (r.success) {
      expect(r.data.brief).toBe("")
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

describe("serializePbConfig", () => {
  it("round-trip：JSON.parse(serializePbConfig(parsePbConfig(s))) 等于解析结果", () => {
    const raw = JSON.stringify({
      ...emptyPictureBookConfig,
      ageBand: "3-6",
      bookType: "narrative",
      pageCount: 16,
      themes: ["friendship", "courage"],
    })
    const parsed = parsePbConfig(raw)
    expect(JSON.parse(serializePbConfig(parsed))).toEqual(parsed)
  })

  it("与现状 JSON.stringify(pbConfig) 等价", () => {
    expect(serializePbConfig(emptyPictureBookConfig)).toBe(
      JSON.stringify(emptyPictureBookConfig),
    )
  })
})
