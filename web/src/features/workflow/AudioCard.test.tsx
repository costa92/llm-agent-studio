import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { AudioCard } from "./AudioCard"
import { AssetCard } from "@/components/studio/AssetCard"

// useResolvedAssetUrl 走 authed fetch → blob object URL；jsdom 无网络。
// 默认 stub 为可解析 URL，让 armed 后 <audio> 真正渲染。
const { useResolvedAssetUrlMock } = vi.hoisted(() => ({
  useResolvedAssetUrlMock: vi.fn(() => ({
    url: "blob:fake-audio" as string | null,
    loading: false,
  })),
}))
vi.mock("@/features/workflow/assetThumb", () => ({
  resolveAssetUrl: vi.fn().mockResolvedValue(null),
  useResolvedAssetUrl: () => useResolvedAssetUrlMock(),
}))

afterEach(() => {
  vi.restoreAllMocks()
  useResolvedAssetUrlMock.mockReturnValue({ url: "blob:fake-audio", loading: false })
})

describe("AudioCard", () => {
  it("is lazy: renders a 试听 control and no <audio> before play", () => {
    render(<AudioCard assetId="a1" />)
    expect(screen.getByRole("button", { name: "试听" })).toBeInTheDocument()
    expect(document.querySelector("audio")).toBeNull()
  })

  it("mounts an <audio> player with the resolved blob URL after clicking 试听", async () => {
    const user = userEvent.setup()
    render(<AudioCard assetId="a1" />)
    await user.click(screen.getByRole("button", { name: "试听" }))
    const audio = document.querySelector("audio")
    expect(audio).not.toBeNull()
    expect(audio?.getAttribute("src")).toBe("blob:fake-audio")
  })

  // 卡内「试听」阻止冒泡：点它不应触发卡片 onSelect（打开抽屉）。
  it("clicking 试听 does not open the card (stopPropagation)", async () => {
    const onSelect = vi.fn()
    const user = userEvent.setup()
    render(<AssetCard assetId="a1" type="audio" onSelect={onSelect} />)
    await user.click(screen.getByRole("button", { name: "试听" }))
    expect(onSelect).not.toHaveBeenCalled()
  })
})
