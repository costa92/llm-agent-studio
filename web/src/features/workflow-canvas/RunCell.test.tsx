import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { RunCell } from "./RunCell"
import type { GroupCell } from "./runFanout"

// AssetThumb 走真实网络（blob 下载），单测 mock 成可断言的 <img>。
vi.mock("@/features/workflow/AssetThumb", () => ({
  AssetThumb: ({ assetId, className }: { assetId: string; className?: string }) => (
    <img data-testid="asset-thumb" data-asset-id={assetId} className={className} alt="" />
  ),
}))

function cell(over: Partial<GroupCell>): GroupCell {
  return { todoId: "a0", status: "done", kind: "image", pageOrdinal: 1, ...over }
}

describe("RunCell", () => {
  it("image+done：渲染 AssetThumb，页序标签", () => {
    render(<RunCell cell={cell({ assetId: "img0", kind: "image", pageOrdinal: 3 })} />)
    const img = screen.getByTestId("asset-thumb")
    expect(img.getAttribute("data-asset-id")).toBe("img0")
    expect(screen.getByText(/第3页·配图/)).toBeInTheDocument()
  })

  it("audio+done：渲染波形占位（run-cell-audio），不下载缩略图", () => {
    render(<RunCell cell={cell({ assetId: "aud0", kind: "audio" })} />)
    expect(document.querySelector('[data-slot="run-cell-audio"]')).toBeInTheDocument()
    expect(screen.queryByTestId("asset-thumb")).toBeNull()
    expect(screen.getByText(/配音/)).toBeInTheDocument()
  })

  it("running：渲染生成中占位（图/音文案区分）", () => {
    render(<RunCell cell={cell({ status: "running", kind: "audio", assetId: undefined })} />)
    expect(screen.getByText("配音中…")).toBeInTheDocument()
  })

  it("failed：渲染失败占位", () => {
    render(<RunCell cell={cell({ status: "failed", kind: "image" })} />)
    expect(screen.getByText("生成失败")).toBeInTheDocument()
  })

  it("点击 → onSelect 调用，且 stopPropagation 不冒泡到父", () => {
    const onSelect = vi.fn()
    const parentClick = vi.fn()
    render(
      <div onClick={parentClick}>
        <RunCell cell={cell({})} onSelect={onSelect} />
      </div>,
    )
    fireEvent.click(screen.getByRole("button"))
    expect(onSelect).toHaveBeenCalledOnce()
    expect(parentClick).not.toHaveBeenCalled()
  })

  it("selected：高亮边框（border-amber）", () => {
    render(<RunCell cell={cell({})} selected />)
    expect(screen.getByRole("button").className).toContain("border-amber")
  })
})
