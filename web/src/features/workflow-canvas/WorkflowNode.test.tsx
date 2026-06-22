import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { ReactFlowProvider, Position, type NodeProps } from "@xyflow/react"
import { WorkflowNode, type StudioRFNode } from "./WorkflowNode"

// 最小 NodeProps 夹具：WorkflowNode 只读 data.node，其余字段给默认值满足类型。
function makeProps(node: StudioRFNode["data"]["node"]): NodeProps<StudioRFNode> {
  return {
    id: node.id,
    data: { node },
    type: "studio",
    selected: false,
    isConnectable: true,
    zIndex: 0,
    positionAbsoluteX: 0,
    positionAbsoluteY: 0,
    dragging: false,
    draggable: true,
    selectable: true,
    deletable: true,
    width: 140,
    height: 48,
    sourcePosition: Position.Bottom,
    targetPosition: Position.Top,
    parentId: undefined,
  } as unknown as NodeProps<StudioRFNode>
}

describe("WorkflowNode", () => {
  it("renders the id label and the Chinese type label", () => {
    render(
      <ReactFlowProvider>
        <WorkflowNode
          {...makeProps({
            id: "script-1",
            type: "script",
            promptId: "",
            dependsOn: [],
          })}
        />
      </ReactFlowProvider>,
    )
    expect(screen.getByText("script-1")).toBeInTheDocument()
    expect(screen.getByText("剧本")).toBeInTheDocument()
  })

  it("applies the agent color var for the node type", () => {
    const { container } = render(
      <ReactFlowProvider>
        <WorkflowNode
          {...makeProps({
            id: "script-1",
            type: "script",
            promptId: "",
            dependsOn: [],
          })}
        />
      </ReactFlowProvider>,
    )
    const bar = container.querySelector('[data-slot="canvas-node-bar"]')
    expect(bar).not.toBeNull()
    expect((bar as HTMLElement).style.backgroundColor).toBe("var(--script)")
  })
})
