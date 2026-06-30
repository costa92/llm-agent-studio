import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { ReactFlowProvider, Position, type NodeProps } from "@xyflow/react"
import { WorkflowNode, type StudioRFNode } from "./WorkflowNode"
import type { StudioNodeData } from "./canvasModel"

// 最小 NodeProps 夹具：接受完整 StudioNodeData（含 timing/highlightFailed），
// 其余 NodeProps 字段给默认值满足类型。
function makeProps(data: StudioNodeData): NodeProps<StudioRFNode> {
  return {
    id: data.node.id,
    data,
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

// 直接渲染 WorkflowNode（包 ReactFlowProvider 满足 useReactFlow/NodeToolbar），
// 绕过 jsdom 下 ReactFlow 不渲染自定义节点的限制。
function renderNode(data: StudioNodeData) {
  return render(
    <ReactFlowProvider>
      <WorkflowNode {...makeProps(data)} />
    </ReactFlowProvider>,
  )
}

describe("WorkflowNode", () => {
  it("renders the id label and the Chinese type label", () => {
    render(
      <ReactFlowProvider>
        <WorkflowNode
          {...makeProps({
            node: { id: "script-1", type: "script", promptId: "", dependsOn: [] },
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
            node: { id: "script-1", type: "script", promptId: "", dependsOn: [] },
          })}
        />
      </ReactFlowProvider>,
    )
    const bar = container.querySelector('[data-slot="canvas-node-bar"]')
    expect(bar).not.toBeNull()
    expect((bar as HTMLElement).style.backgroundColor).toBe("var(--script)")
  })

  // 运行模式（data.run 存在）：渲染运行状态指示器、标 data-status、隐藏编辑工具条/色条。
  it.each(["done", "running", "failed"] as const)(
    "renders run-status indicator with data-status=%s in run mode",
    (status) => {
      const { container } = render(
        <ReactFlowProvider>
          <WorkflowNode
            {...makeProps({
              node: { id: "script-1", type: "script", promptId: "", dependsOn: [] },
              run: { status, todoId: "uuidA" },
            })}
          />
        </ReactFlowProvider>,
      )
      const node = container.querySelector('[data-slot="canvas-node"]')
      expect(node).not.toBeNull()
      expect((node as HTMLElement).getAttribute("data-status")).toBe(status)
      // 运行态指示器存在、编辑态色条不再渲染。
      expect(
        container.querySelector('[data-slot="canvas-node-status"]'),
      ).not.toBeNull()
      expect(
        container.querySelector('[data-slot="canvas-node-bar"]'),
      ).toBeNull()
    },
  )
})

const baseNode = { id: "n1", type: "asset", promptId: "", dependsOn: [] }

describe("WorkflowNode 耗时 chip / 失败红环", () => {
  it("有 timing 时显示格式化耗时", () => {
    renderNode({
      node: baseNode,
      run: { status: "done", todoId: "t1" },
      timing: { startedAt: 0, finishedAt: 3400, elapsedMs: 3400 },
    })
    expect(screen.getByText("3.4s")).toBeInTheDocument()
  })
  it("无 timing 时不显示耗时 chip", () => {
    const { container } = renderNode({ node: baseNode, run: { status: "done", todoId: "t1" } })
    expect(container.querySelector('[data-slot="canvas-node-timing"]')).toBeNull()
  })
  it("highlightFailed 且 failed 时加红环标记", () => {
    const { container } = renderNode({
      node: baseNode,
      run: { status: "failed", todoId: "t1" },
      highlightFailed: true,
    })
    expect(container.querySelector('[data-failed-highlight="true"]')).toBeInTheDocument()
  })
})
