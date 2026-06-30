import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { RunMatrix } from "./RunMatrix"
import type { GroupCell, RunGroup } from "./runFanout"

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

const CELLS: GroupCell[] = [
  { todoId: "a0", assetId: "img0", status: "done", kind: "image", pageOrdinal: 1 },
  { todoId: "a1", assetId: "aud0", status: "done", kind: "audio", pageOrdinal: 2 },
  { todoId: "a2", status: "running", kind: "image", pageOrdinal: 3 },
]

const GROUP: RunGroup = {
  canvasNodeId: "storyboard-1",
  cells: CELLS,
  counts: { done: 2, running: 1, failed: 0, pending: 0, total: 3 },
}

function renderMatrix(over: Partial<Parameters<typeof RunMatrix>[0]> = {}) {
  return render(
    <RunMatrix
      group={GROUP}
      onSelectCell={vi.fn()}
      org="acme"
      isAdmin
      {...over}
    />,
  )
}

describe("RunMatrix", () => {
  it("空组：渲染占位，无矩阵", () => {
    render(<RunMatrix group={undefined} onSelectCell={vi.fn()} org="acme" isAdmin />)
    expect(screen.getByText("该分镜暂无逐页产物")).toBeInTheDocument()
    expect(document.querySelector('[data-slot="run-matrix"]')).toBeNull()
  })

  it("渲染汇总 + 图例(4 色) + 每页一格矩阵", () => {
    renderMatrix()
    expect(screen.getByText(/2\/3 完成/)).toBeInTheDocument()
    const legend = document.querySelector('[data-slot="run-matrix-legend"]')!
    expect(legend.querySelectorAll("span[style]").length).toBe(4)
    const cells = document.querySelectorAll('[data-slot="run-matrix-cell"]')
    expect(cells.length).toBe(3)
    expect(cells[0].getAttribute("data-status")).toBe("done")
    expect(cells[2].getAttribute("data-status")).toBe("running")
  })

  it("点矩阵格 → onSelectCell(cell)", () => {
    const onSelectCell = vi.fn()
    renderMatrix({ onSelectCell })
    fireEvent.click(document.querySelectorAll('[data-slot="run-matrix-cell"]')[1])
    expect(onSelectCell).toHaveBeenCalledWith(CELLS[1])
  })

  it("选中 image 页 → SelectedAssetPanel；选中 audio 页 → AssetAudio 播放器", () => {
    const { rerender } = renderMatrix({ selectedTodoId: "a0" })
    expect(screen.getByTestId("selected-asset").getAttribute("data-asset-id")).toBe("img0")
    expect(screen.queryByTestId("asset-audio")).toBeNull()

    rerender(
      <RunMatrix group={GROUP} selectedTodoId="a1" onSelectCell={vi.fn()} org="acme" isAdmin />,
    )
    expect(screen.getByTestId("asset-audio").getAttribute("data-asset-id")).toBe("aud0")
  })

  it("选中 running 页（无 assetId）→ 暂无产物文案，不渲播放器", () => {
    renderMatrix({ selectedTodoId: "a2" })
    expect(screen.getByText(/暂无产物/)).toBeInTheDocument()
    expect(screen.queryByTestId("selected-asset")).toBeNull()
    expect(screen.queryByTestId("asset-audio")).toBeNull()
  })
})
