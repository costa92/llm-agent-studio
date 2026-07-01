import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { ReactFlowProvider, Position, type NodeProps } from "@xyflow/react"
import { GroupNode, type GroupRunNodeData, type GroupRunRFNode } from "./GroupNode"
import type { RunPage } from "./runFanout"
import type { WorkflowNode } from "@/lib/types"

vi.mock("@/features/workflow/AssetThumb", () => ({
  AssetThumb: ({ assetId }: { assetId: string }) => (
    <img data-testid="asset-thumb" data-asset-id={assetId} alt="" />
  ),
}))

const BOARD_NODE: WorkflowNode = {
  id: "storyboard-1",
  type: "storyboard",
  promptId: "",
  dependsOn: [],
  position: { x: 0, y: 0 },
}

// 两页：第1页图(done)+音(done)；第2页图(running)+音(failed)。共 4 个资产格。
const PAGES: RunPage[] = [
  {
    key: "s1",
    pageOrdinal: 1,
    image: { todoId: "img1", assetId: "i1", status: "done", kind: "image" },
    audio: { todoId: "aud1", assetId: "u1", status: "done", kind: "audio" },
    others: [],
  },
  {
    key: "s2",
    pageOrdinal: 2,
    image: { todoId: "img2", status: "running", kind: "image" },
    audio: { todoId: "aud2", status: "failed", kind: "audio" },
    others: [],
  },
]

function makeProps(over: Partial<GroupRunNodeData> = {}): NodeProps<GroupRunRFNode> {
  const data: GroupRunNodeData = {
    node: BOARD_NODE,
    pages: PAGES,
    counts: { done: 2, running: 1, failed: 1, pending: 0, total: 4 },
    expanded: false,
    onToggle: vi.fn(),
    onSelectPage: vi.fn(),
    ...over,
  }
  return {
    id: "storyboard-1",
    data,
    type: "groupRun",
    selected: false,
    isConnectable: false,
    zIndex: 0,
    positionAbsoluteX: 0,
    positionAbsoluteY: 0,
    dragging: false,
    draggable: false,
    selectable: true,
    deletable: false,
    width: 280,
    height: 80,
    sourcePosition: Position.Bottom,
    targetPosition: Position.Top,
    parentId: undefined,
  } as unknown as NodeProps<GroupRunRFNode>
}

function renderNode(props: NodeProps<GroupRunRFNode>) {
  return render(
    <ReactFlowProvider>
      <GroupNode {...props} />
    </ReactFlowProvider>,
  )
}

describe("GroupNode", () => {
  it("折叠态：渲染 [N 项] 徽标（资产格计）+ 逐资产格状态条，不渲页卡网格", () => {
    renderNode(makeProps())
    // 徽标 = counts.total（资产格：4）。
    expect(screen.getByText("4 项")).toBeInTheDocument()
    const bar = document.querySelector('[data-slot="group-run-bar"]')!
    // 状态条逐资产格展平：img1,aud1,img2,aud2 = 4 段。
    expect(bar.querySelectorAll("span").length).toBe(4)
    const statuses = Array.from(bar.querySelectorAll("span")).map((s) =>
      s.getAttribute("data-status"),
    )
    expect(statuses).toEqual(["done", "done", "running", "failed"])
    // 折叠态无展开网格。
    expect(document.querySelector('[data-slot="group-run-grid"]')).toBeNull()
  })

  it("点 header → onToggle(id) 调用，aria-expanded 反映折叠态", () => {
    const onToggle = vi.fn()
    renderNode(makeProps({ onToggle }))
    const header = document.querySelector('[data-slot="group-run-header"]')!
    expect(header.getAttribute("aria-expanded")).toBe("false")
    fireEvent.click(header)
    expect(onToggle).toHaveBeenCalledWith("storyboard-1")
  })

  it("展开态：data-expanded=true + 每页一张页卡（图音双渲染）", () => {
    renderNode(makeProps({ expanded: true }))
    expect(
      document.querySelector('[data-slot="group-run-node"]')!.getAttribute("data-expanded"),
    ).toBe("true")
    const grid = document.querySelector('[data-slot="group-run-grid"]')!
    // 两页 → 两张页卡。
    expect(grid.querySelectorAll('[data-slot="run-page-card"]').length).toBe(2)
    // 第1页含 图 + 音 两个子格。
    const firstCard = grid.querySelectorAll('[data-slot="run-page-card"]')[0]
    expect(firstCard.querySelectorAll('[data-slot="run-subcell"]').length).toBe(2)
  })

  it("展开态：点页卡 → onSelectPage(id, page)", () => {
    const onSelectPage = vi.fn()
    renderNode(makeProps({ expanded: true, onSelectPage }))
    const firstCard = document.querySelectorAll('[data-slot="run-page-card"]')[0]
    fireEvent.click(firstCard)
    expect(onSelectPage).toHaveBeenCalledWith("storyboard-1", PAGES[0])
  })
})
