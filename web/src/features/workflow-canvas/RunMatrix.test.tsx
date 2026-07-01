import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { RunMatrix } from "./RunMatrix"
import type { RunPage, RunGroup } from "./runFanout"

// 选中产物区复用的重组件 mock 成可断言占位（各自有 react-query / blob 下载依赖）。
vi.mock("@/features/workflow/SelectedAssetPanel", () => ({
  SelectedAssetPanel: ({ assetId }: { assetId: string }) => (
    <div data-testid="selected-asset" data-asset-id={assetId} />
  ),
}))
vi.mock("@/features/workflow/AssetAudio", () => ({
  AssetAudio: ({ assetId }: { assetId: string }) => (
    <div data-testid="asset-audio" data-asset-id={assetId} />
  ),
}))

// 三页：p1 图+音全 done；p2 仅图 done（image-only）；p3 图 running（无 assetId）。
const PAGES: RunPage[] = [
  {
    key: "s1",
    pageOrdinal: 1,
    image: { todoId: "img1", assetId: "img0", status: "done", kind: "image" },
    audio: { todoId: "aud1", assetId: "aud0", status: "done", kind: "audio" },
    others: [],
  },
  {
    key: "s2",
    pageOrdinal: 2,
    image: { todoId: "img2", assetId: "img2", status: "done", kind: "image" },
    others: [],
  },
  {
    key: "s3",
    pageOrdinal: 3,
    image: { todoId: "img3", status: "running", kind: "image" },
    others: [],
  },
]

const GROUP: RunGroup = {
  canvasNodeId: "storyboard-1",
  pages: PAGES,
  counts: { done: 3, running: 1, failed: 0, pending: 0, total: 4 },
}

function renderMatrix(over: Partial<Parameters<typeof RunMatrix>[0]> = {}) {
  return render(
    <RunMatrix
      group={GROUP}
      onSelectPage={vi.fn()}
      org="acme"
      isAdmin
      {...over}
    />,
  )
}

describe("RunMatrix", () => {
  it("空组：渲染占位，无矩阵", () => {
    render(<RunMatrix group={undefined} onSelectPage={vi.fn()} org="acme" isAdmin />)
    expect(screen.getByText("该分镜暂无逐页产物")).toBeInTheDocument()
    expect(document.querySelector('[data-slot="run-matrix"]')).toBeNull()
  })

  it("渲染汇总（逐资产格）+ 图例(4 色) + 每页一格矩阵（页聚合状态）", () => {
    renderMatrix()
    expect(screen.getByText(/3\/4 完成/)).toBeInTheDocument()
    const legend = document.querySelector('[data-slot="run-matrix-legend"]')!
    expect(legend.querySelectorAll("span[style]").length).toBe(4)
    const cells = document.querySelectorAll('[data-slot="run-matrix-cell"]')
    // 三页 → 三格。
    expect(cells.length).toBe(3)
    expect(cells[0].getAttribute("data-status")).toBe("done")
    expect(cells[2].getAttribute("data-status")).toBe("running")
  })

  it("点矩阵格 → onSelectPage(page)", () => {
    const onSelectPage = vi.fn()
    renderMatrix({ onSelectPage })
    fireEvent.click(document.querySelectorAll('[data-slot="run-matrix-cell"]')[1])
    expect(onSelectPage).toHaveBeenCalledWith(PAGES[1])
  })

  it("图音双渲染：选中含图+音的页 → 同时渲 SelectedAssetPanel + AssetAudio", () => {
    renderMatrix({ selectedPageKey: "s1" })
    expect(screen.getByTestId("selected-asset").getAttribute("data-asset-id")).toBe("img0")
    expect(screen.getByTestId("asset-audio").getAttribute("data-asset-id")).toBe("aud0")
  })

  it("image-only 页 → 只渲图，不渲音播放器", () => {
    renderMatrix({ selectedPageKey: "s2" })
    expect(screen.getByTestId("selected-asset").getAttribute("data-asset-id")).toBe("img2")
    expect(screen.queryByTestId("asset-audio")).toBeNull()
  })

  it("选中 running 页（图无 assetId）→ 配图暂无产物文案，不渲图面板", () => {
    renderMatrix({ selectedPageKey: "s3" })
    expect(screen.getByText(/暂无产物/)).toBeInTheDocument()
    expect(screen.queryByTestId("selected-asset")).toBeNull()
  })
})
