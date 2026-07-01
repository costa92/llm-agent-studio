import { describe, it, expect } from "vitest"
import { assemblePages, isBookReady, imageAssetIdByShotId } from "./pictureBookPages"
import type { Shot, ProjectAsset } from "./api"

const shots: Shot[] = [
  { id: "s0", action: "" }, // 封面
  { id: "s1", action: "小熊起床" },
  { id: "s2", action: "小熊吃饭" },
  { id: "s3", action: "" }, // 结尾
]

const asset = (over: Partial<ProjectAsset>): ProjectAsset => ({
  id: "x",
  shotId: "",
  type: "image",
  status: "accepted",
  ...over,
})

describe("assemblePages", () => {
  it("首尾判定为封面/结尾，中间为内容", () => {
    const pages = assemblePages({ shots, assets: [], title: "小熊" })
    expect(pages.map((p) => p.kind)).toEqual(["cover", "content", "content", "ending"])
    expect(pages[0].title).toBe("小熊")
    expect(pages[3].title).toBe("小熊")
    expect(pages[1].title).toBeUndefined()
    expect(pages[1].narration).toBe("小熊起床")
  })

  it("按 shotId 配对插图/音频，取该页 image 的 prompt/model", () => {
    const assets: ProjectAsset[] = [
      asset({ id: "img1", shotId: "s1", type: "image", prompt: "p1", provider: "openai", model: "m1" }),
      asset({ id: "aud1", shotId: "s1", type: "audio" }),
    ]
    const pages = assemblePages({ shots, assets, title: "t" })
    expect(pages[1].illustrationAssetId).toBe("img1")
    expect(pages[1].audioAssetId).toBe("aud1")
    expect(pages[1].prompt).toBe("p1")
    expect(pages[1].model).toBe("m1")
  })

  it("同页多版本取最新 version", () => {
    const assets: ProjectAsset[] = [
      asset({ id: "v1", shotId: "s1", type: "image", version: 1 }),
      asset({ id: "v2", shotId: "s1", type: "image", version: 2 }),
    ]
    const pages = assemblePages({ shots, assets, title: "t" })
    expect(pages[1].illustrationAssetId).toBe("v2")
  })

  it("非 accepted 资产被忽略", () => {
    const assets: ProjectAsset[] = [
      asset({ id: "pending", shotId: "s1", type: "image", status: "generating" }),
      asset({ id: "await", shotId: "s2", type: "image", status: "pending_acceptance" }),
    ]
    const pages = assemblePages({ shots, assets, title: "t" })
    expect(pages[1].illustrationAssetId).toBeUndefined()
  })

  it("空 shots → 空页", () => {
    expect(assemblePages({ shots: [], assets: [], title: "t" })).toEqual([])
  })
})

describe("imageAssetIdByShotId", () => {
  it("按 shotId 映射 accepted image 的 id，忽略 audio", () => {
    const assets: ProjectAsset[] = [
      asset({ id: "img1", shotId: "s1", type: "image" }),
      asset({ id: "aud1", shotId: "s1", type: "audio" }),
      asset({ id: "img2", shotId: "s2", type: "image" }),
    ]
    expect(imageAssetIdByShotId(assets)).toEqual({ s1: "img1", s2: "img2" })
  })

  it("同页多版本取最新 version", () => {
    const assets: ProjectAsset[] = [
      asset({ id: "v1", shotId: "s1", type: "image", version: 1 }),
      asset({ id: "v2", shotId: "s1", type: "image", version: 2 }),
    ]
    expect(imageAssetIdByShotId(assets)).toEqual({ s1: "v2" })
  })

  it("非 accepted image 被忽略", () => {
    const assets: ProjectAsset[] = [
      asset({ id: "pending", shotId: "s1", type: "image", status: "generating" }),
    ]
    expect(imageAssetIdByShotId(assets)).toEqual({})
  })
})

describe("isBookReady", () => {
  it("accepted image 数 ≥ 内容页一半 → 就绪", () => {
    // 内容页 = 4-2 = 2，需 ≥ 1 张 accepted image。
    const assets = [asset({ shotId: "s1", type: "image", status: "accepted" })]
    expect(isBookReady(shots, assets)).toBe(true)
  })

  it("无 accepted image → 未就绪", () => {
    expect(isBookReady(shots, [])).toBe(false)
  })

  it("仅 pending_acceptance image → 未就绪", () => {
    const assets = [asset({ shotId: "s1", type: "image", status: "pending_acceptance" })]
    expect(isBookReady(shots, assets)).toBe(false)
  })

  it("空 shots → 未就绪", () => {
    expect(isBookReady([], [])).toBe(false)
  })
})
