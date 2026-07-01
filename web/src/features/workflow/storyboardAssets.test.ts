import { describe, it, expect } from "vitest"
import { imageAssetIdByShotId } from "./storyboardAssets"
import type { ProjectAsset } from "./api"

const asset = (over: Partial<ProjectAsset>): ProjectAsset => ({
  id: "x",
  shotId: "",
  type: "image",
  status: "accepted",
  ...over,
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
