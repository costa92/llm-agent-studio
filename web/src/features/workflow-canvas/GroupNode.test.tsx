import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@testing-library/react"
import { ReactFlowProvider, Position, type NodeProps } from "@xyflow/react"
import { GroupNode, type GroupRunNodeData, type GroupRunRFNode } from "./GroupNode"
import type { GroupCell } from "./runFanout"
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

const CELLS: GroupCell[] = [
  { todoId: "a0", assetId: "img0", status: "done", kind: "image", pageOrdinal: 1 },
  { todoId: "a1", status: "running", kind: "image", pageOrdinal: 2 },
  { todoId: "a2", status: "failed", kind: "audio", pageOrdinal: 3 },
]

function makeProps(over: Partial<GroupRunNodeData> = {}): NodeProps<GroupRunRFNode> {
  const data: GroupRunNodeData = {
    node: BOARD_NODE,
    cells: CELLS,
    counts: { done: 1, running: 1, failed: 1, pending: 0, total: 3 },
    expanded: false,
    onToggle: vi.fn(),
    onSelectCell: vi.fn(),
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
  it("折叠态：渲染 [N 项] 徽标 + 每页一格状态条，不渲子卡网格", () => {
    renderNode(makeProps())
    expect(screen.getByText("3 项")).toBeInTheDocument()
    const bar = document.querySelector('[data-slot="group-run-bar"]')!
    expect(bar.querySelectorAll("span").length).toBe(3)
    // 状态条每格按状态打 data-status（含一个 done/running/failed）。
    const statuses = Array.from(bar.querySelectorAll("span")).map((s) =>
      s.getAttribute("data-status"),
    )
    expect(statuses).toEqual(["done", "running", "failed"])
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

  it("展开态：data-expanded=true + 渲逐页子卡网格", () => {
    renderNode(makeProps({ expanded: true }))
    expect(
      document.querySelector('[data-slot="group-run-node"]')!.getAttribute("data-expanded"),
    ).toBe("true")
    const grid = document.querySelector('[data-slot="group-run-grid"]')!
    expect(grid.querySelectorAll('[data-slot="run-cell"]').length).toBe(3)
  })

  it("展开态：点子卡 → onSelectCell(id, cell)", () => {
    const onSelectCell = vi.fn()
    renderNode(makeProps({ expanded: true, onSelectCell }))
    const firstCell = document.querySelectorAll('[data-slot="run-cell"]')[0]
    fireEvent.click(firstCell)
    expect(onSelectCell).toHaveBeenCalledWith("storyboard-1", CELLS[0])
  })
})
