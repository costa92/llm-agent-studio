import { describe, it, expect, vi, beforeEach } from "vitest"
import { render, screen } from "@testing-library/react"

// 音频解析 hook 桩：受测组件只关心拿到 url 后喂给 <audio>。
const resolved = vi.hoisted(() => ({ url: null as string | null, loading: false }))
vi.mock("./assetThumb", () => ({
  useResolvedAssetUrl: () => resolved,
}))

import { AssetAudio } from "./AssetAudio"

describe("AssetAudio", () => {
  beforeEach(() => {
    resolved.url = null
    resolved.loading = false
  })

  it("解析出 url → 渲染 <audio> 且 src 正确", () => {
    resolved.url = "blob:audio-123"
    render(<AssetAudio assetId="a1" />)
    const audio = document.querySelector("audio")
    expect(audio).not.toBeNull()
    expect(audio).toHaveAttribute("src", "blob:audio-123")
  })

  it("加载中 → 降级文案", () => {
    resolved.url = null
    resolved.loading = true
    render(<AssetAudio assetId="a1" />)
    expect(screen.getByText("音频加载中…")).toBeInTheDocument()
    expect(document.querySelector("audio")).toBeNull()
  })

  it("解析失败 → 不可用文案", () => {
    resolved.url = null
    resolved.loading = false
    render(<AssetAudio assetId="a1" />)
    expect(screen.getByText("音频不可用")).toBeInTheDocument()
  })
})
