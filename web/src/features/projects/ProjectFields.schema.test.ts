import { describe, it, expect } from "vitest"
import {
  projectFormSchema,
  createProjectFormSchema,
  defaultsFor,
} from "./ProjectFields.schema"

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
  }

  it("name 必填", () => {
    const r = projectFormSchema.safeParse({ ...base, name: "" })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入项目名称")).toBe(true)
    }
  })

  it("name 纯空白 → 必填错误", () => {
    const r = projectFormSchema.safeParse({ ...base, name: "   " })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message === "请输入项目名称")).toBe(true)
    }
  })

  it("name 超长（>200）→ 长度错误", () => {
    const r = projectFormSchema.safeParse({ ...base, name: "a".repeat(201) })
    expect(r.success).toBe(false)
    if (!r.success) {
      expect(r.error.issues.some((i) => i.message.includes("200"))).toBe(true)
    }
  })

  it("name 前后空白被 trim", () => {
    const r = projectFormSchema.safeParse({ ...base, name: "  我的项目  " })
    expect(r.success).toBe(true)
    if (r.success) {
      expect(r.data.name).toBe("我的项目")
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
      }),
    )
    expect(r.success).toBe(true)
    if (r.success) {
      expect(r.data.brief).toBe("")
    }
  })

  // 内容类型/风格解耦：style/contentType/targetPlatform 均可空（留空=由工作流决定）。
  it("style/contentType/targetPlatform 可空（内容类型/风格解耦）", () => {
    const r = projectFormSchema.safeParse({
      ...base,
      style: "",
      contentType: "",
      targetPlatform: "",
    })
    expect(r.success).toBe(true)
  })

  it("合法表单通过校验", () => {
    const r = projectFormSchema.safeParse(base)
    expect(r.success).toBe(true)
  })
})

describe("defaultsFor", () => {
  it("无 initial 时给空表单默认（含 style/contentType/targetPlatform 皆空）", () => {
    const d = defaultsFor()
    expect(d.name).toBe("")
    expect(d.brief).toBe("")
    // 不再预填生成默认——留空由工作流决定。
    expect(d.style).toBe("")
    expect(d.contentType).toBe("")
    expect(d.targetPlatform).toBe("")
  })

  it("有 initial 时回填基本字段", () => {
    const d = defaultsFor({
      name: "旧名",
      description: "旧需求",
      contentType: "广告片",
      targetPlatform: "B 站",
      style: "动画",
    })
    expect(d.name).toBe("旧名")
    expect(d.description).toBe("旧需求")
    expect(d.contentType).toBe("广告片")
    expect(d.style).toBe("动画")
  })
})
