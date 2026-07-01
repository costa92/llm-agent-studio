import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { RunPageCard } from "./RunPageCard"
import type { RunPage } from "./runFanout"

vi.mock("@/features/workflow/AssetThumb", () => ({
  AssetThumb: ({ assetId }: { assetId: string }) => (
    <img data-testid="asset-thumb" data-asset-id={assetId} alt="" />
  ),
}))

const mk = (over: Partial<RunPage>): RunPage => ({ key: "s", pageOrdinal: 1, others: [], ...over })

describe("RunPageCard", () => {
  it("图音双渲染：done 图 → 缩略图，done 音 → 波形 + 播放占位；页头显示页序", () => {
    render(
      <RunPageCard
        page={mk({
          pageOrdinal: 2,
          image: { todoId: "i", assetId: "img9", status: "done", kind: "image" },
          audio: { todoId: "a", assetId: "aud9", status: "done", kind: "audio" },
        })}
      />,
    )
    expect(screen.getByText("第2页")).toBeInTheDocument()
    expect(screen.getByTestId("asset-thumb").getAttribute("data-asset-id")).toBe("img9")
    expect(document.querySelector('[data-slot="run-cell-audio"]')).not.toBeNull()
    // 图 + 音 = 两个子格。
    expect(document.querySelectorAll('[data-slot="run-subcell"]').length).toBe(2)
  })

  it("image-only 页（无音槽）→ 只渲一个子格，不渲配音", () => {
    render(
      <RunPageCard
        page={mk({ image: { todoId: "i", assetId: "img1", status: "done", kind: "image" } })}
      />,
    )
    expect(document.querySelectorAll('[data-slot="run-subcell"]').length).toBe(1)
    expect(document.querySelector('[data-slot="run-cell-audio"]')).toBeNull()
  })

  it("running → 生成中/配音中；pending → 待配图/待配音；failed → 失败", () => {
    const { rerender } = render(
      <RunPageCard
        page={mk({
          image: { todoId: "i", status: "running", kind: "image" },
          audio: { todoId: "a", status: "pending", kind: "audio" },
        })}
      />,
    )
    expect(screen.getByText("生成中…")).toBeInTheDocument()
    expect(screen.getByText("待配音")).toBeInTheDocument()

    rerender(
      <RunPageCard
        page={mk({
          image: { todoId: "i", status: "failed", kind: "image" },
          audio: { todoId: "a", status: "failed", kind: "audio" },
        })}
      />,
    )
    expect(screen.getByText("生成失败")).toBeInTheDocument()
    expect(screen.getByText("配音失败")).toBeInTheDocument()
  })

  it("页聚合状态：任一失败 → 卡片 data-status=failed", () => {
    render(
      <RunPageCard
        page={mk({
          image: { todoId: "i", status: "done", kind: "image" },
          audio: { todoId: "a", status: "failed", kind: "audio" },
        })}
      />,
    )
    expect(
      document.querySelector('[data-slot="run-page-card"]')!.getAttribute("data-status"),
    ).toBe("failed")
  })

  it("点击 → onSelect，且 stopPropagation（不冒泡到父）", () => {
    const onSelect = vi.fn()
    const onParent = vi.fn()
    render(
      <div onClick={onParent}>
        <RunPageCard
          page={mk({ image: { todoId: "i", assetId: "x", status: "done", kind: "image" } })}
          onSelect={onSelect}
        />
      </div>,
    )
    fireEvent.click(document.querySelector('[data-slot="run-page-card"]')!)
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onParent).not.toHaveBeenCalled()
  })
})
