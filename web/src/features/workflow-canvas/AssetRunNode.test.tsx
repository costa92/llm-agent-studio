import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import { ReactFlowProvider, Position, type NodeProps } from "@xyflow/react"
import { AssetRunNode, type AssetRunRFNode } from "./AssetRunNode"
import type { AssetRunNodeData } from "./runFanout"

// AssetThumb 走真实网络（blob 下载），单测里 mock 成可断言的 <img>。
vi.mock("@/features/workflow/AssetThumb", () => ({
  AssetThumb: ({ assetId, className }: { assetId: string; className?: string }) => (
    <img data-testid="asset-thumb" data-asset-id={assetId} className={className} alt="" />
  ),
}))

function makeProps(data: AssetRunNodeData): NodeProps<AssetRunRFNode> {
  return {
    id: `asset-run:${data.todoId}`,
    data,
    type: "assetRun",
    selected: false,
    isConnectable: false,
    zIndex: 0,
    positionAbsoluteX: 0,
    positionAbsoluteY: 0,
    dragging: false,
    draggable: false,
    selectable: true,
    deletable: false,
    width: 120,
    height: 90,
    sourcePosition: Position.Bottom,
    targetPosition: Position.Top,
    parentId: undefined,
  } as unknown as NodeProps<AssetRunRFNode>
}

function renderNode(data: AssetRunNodeData) {
  return render(
    <ReactFlowProvider>
      <AssetRunNode {...makeProps(data)} />
    </ReactFlowProvider>,
  )
}

describe("AssetRunNode", () => {
  it("done image：渲染 AssetThumb <img> + 页序标签", () => {
    renderNode({
      todoId: "a0",
      assetId: "img0",
      status: "done",
      kind: "image",
      pageOrdinal: 3,
    })
    const img = screen.getByTestId("asset-thumb")
    expect(img).toBeInTheDocument()
    expect(img.getAttribute("data-asset-id")).toBe("img0")
    expect(screen.getByText(/第3页·配图/)).toBeInTheDocument()
  })

  it("audio：渲染音频占位，不下载缩略图", () => {
    renderNode({
      todoId: "a1",
      assetId: "aud0",
      status: "done",
      kind: "audio",
      pageOrdinal: 1,
    })
    expect(screen.queryByTestId("asset-thumb")).toBeNull()
    expect(screen.getByText(/第1页·配音/)).toBeInTheDocument()
  })

  it("generating（running 无 assetId）：渲染「生成中…」", () => {
    renderNode({
      todoId: "a2",
      status: "running",
      kind: "unknown",
      pageOrdinal: 2,
    })
    expect(screen.getByText("生成中…")).toBeInTheDocument()
    expect(screen.queryByTestId("asset-thumb")).toBeNull()
    expect(screen.getByText(/第2页·素材/)).toBeInTheDocument()
  })

  it("failed：渲染 danger 边框 + 「生成失败」", () => {
    const { container } = renderNode({
      todoId: "a3",
      assetId: "img3",
      status: "failed",
      kind: "image",
      pageOrdinal: 1,
    })
    expect(screen.getByText("生成失败")).toBeInTheDocument()
    const card = container.querySelector('[data-slot="asset-run-node"]')
    expect(card?.className).toContain("border-danger")
    expect(card?.getAttribute("data-status")).toBe("failed")
  })

  it("done 状态点带 ✓", () => {
    const { container } = renderNode({
      todoId: "a4",
      assetId: "img4",
      status: "done",
      kind: "image",
      pageOrdinal: 1,
    })
    const dot = container.querySelector('[data-slot="asset-run-status"]')
    expect(dot).not.toBeNull()
    expect(dot?.textContent).toContain("✓")
  })
})
