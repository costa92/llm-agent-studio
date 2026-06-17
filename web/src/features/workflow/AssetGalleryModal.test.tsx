import { describe, it, expect, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { AssetGalleryModal } from "./AssetGalleryModal"

// 子组件会 fetch / 用 clipboard，测试里桩掉，聚焦相册自身逻辑。
vi.mock("./AssetThumb", () => ({
  AssetThumb: ({ assetId }: { assetId: string }) => <div data-testid="thumb">{assetId}</div>,
}))
vi.mock("./AssetPreviewActions", () => ({
  AssetPreviewActions: ({ assetId }: { assetId: string }) => (
    <div data-testid="actions">{assetId}</div>
  ),
}))

const thumbs = () => Array.from(document.querySelectorAll('[data-slot="gallery-thumb"]'))

describe("AssetGalleryModal", () => {
  it("空素材显示占位", () => {
    render(<AssetGalleryModal assetIds={[]} open onOpenChange={() => {}} />)
    expect(screen.getByText("暂无已生成素材")).toBeInTheDocument()
  })

  it("网格态：渲染全部缩略图 + 计数", () => {
    render(<AssetGalleryModal assetIds={["a", "b", "c"]} open onOpenChange={() => {}} />)
    expect(screen.getByText("全部素材 · 3")).toBeInTheDocument()
    expect(thumbs()).toHaveLength(3)
  })

  it("点缩略图进灯箱：显示「i / N」+ 返回 + 操作", () => {
    render(<AssetGalleryModal assetIds={["a", "b", "c"]} open onOpenChange={() => {}} />)
    fireEvent.click(thumbs()[1])
    expect(screen.getByText("素材 2 / 3")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "下一张" })).toBeInTheDocument()
    expect(screen.getByTestId("actions")).toHaveTextContent("b")
    // 返回相册 → 回网格态。
    fireEvent.click(screen.getByText("← 返回相册"))
    expect(screen.getByText("全部素材 · 3")).toBeInTheDocument()
  })

  it("灯箱翻页环绕：下一张从末尾回到首张", () => {
    render(<AssetGalleryModal assetIds={["a", "b"]} open onOpenChange={() => {}} />)
    fireEvent.click(thumbs()[1]) // 看 b（index 1，末尾）
    expect(screen.getByText("素材 2 / 2")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: "下一张" }))
    expect(screen.getByText("素材 1 / 2")).toBeInTheDocument()
  })
})
