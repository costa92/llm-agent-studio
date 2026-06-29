import { describe, expect, it, vi } from "vitest"
import { render } from "@testing-library/react"
import { ReactFlowProvider, type EdgeProps } from "@xyflow/react"
import { StudioEdge } from "./StudioEdge"
import { CanvasActionsProvider } from "./CanvasActionsContext"

const noop = () => {}
const ctxValue = {
  onDuplicateNode: noop,
  onDeleteNode: noop,
  onDeleteEdge: noop,
  onInsertOnEdge: noop,
  onQuickAddFrom: noop,
}

vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} })

// 直接渲染 StudioEdge：ReactFlow 在 jsdom 里不画边（无布局引擎，0 个 <path>），
// 故绕过 ReactFlow 渲染边本体。ReactFlowProvider 供组件内 useReactFlow() 使用。
function renderEdge(active: boolean) {
  const props = {
    id: "a->b",
    source: "a",
    target: "b",
    type: "studio",
    animated: false,
    selected: false,
    selectable: true,
    deletable: true,
    sourceX: 0,
    sourceY: 0,
    targetX: 0,
    targetY: 120,
    // Position 枚举值即字符串字面量
    sourcePosition: "bottom" as EdgeProps["sourcePosition"],
    targetPosition: "top" as EdgeProps["targetPosition"],
    markerEnd: undefined,
    style: undefined,
    data: { active },
  } as unknown as EdgeProps
  return render(
    <CanvasActionsProvider value={ctxValue}>
      <ReactFlowProvider>
        <svg>
          <StudioEdge {...props} />
        </svg>
      </ReactFlowProvider>
    </CanvasActionsProvider>,
  )
}

describe("StudioEdge data.active", () => {
  it("active 时边路径带 data-active=true", () => {
    const { container } = renderEdge(true)
    expect(
      container.querySelector('[data-slot="studio-edge-path"][data-active="true"]'),
    ).toBeTruthy()
  })
  it("非 active 时 data-active=false", () => {
    const { container } = renderEdge(false)
    expect(
      container.querySelector('[data-slot="studio-edge-path"][data-active="false"]'),
    ).toBeTruthy()
  })
})
