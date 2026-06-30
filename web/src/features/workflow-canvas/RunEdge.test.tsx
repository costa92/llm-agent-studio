import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { render } from "@testing-library/react"
import { ReactFlowProvider, Position, type EdgeProps } from "@xyflow/react"
import { RunEdge } from "./RunEdge"

function stubMatchMedia(reduce: boolean) {
  window.matchMedia = ((q: string) => ({
    matches: reduce && q.includes("reduce"),
    media: q,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as typeof window.matchMedia
}

function makeProps(active: boolean): EdgeProps {
  return {
    id: "e1",
    source: "a",
    target: "b",
    sourceX: 0,
    sourceY: 0,
    targetX: 100,
    targetY: 100,
    sourcePosition: Position.Bottom,
    targetPosition: Position.Top,
    data: { active },
    selected: false,
    animated: false,
    markerEnd: undefined,
    style: {},
    interactionWidth: 20,
  } as unknown as EdgeProps
}

// RunEdge 渲 <path>/<circle> 等 SVG，须挂在 <svg> 内；ReactFlowProvider 提供边渲染上下文。
function renderEdge(active: boolean) {
  return render(
    <ReactFlowProvider>
      <svg>
        <RunEdge {...makeProps(active)} />
      </svg>
    </ReactFlowProvider>,
  )
}

describe("RunEdge", () => {
  // 钉死非 reduced-motion（默认），避免跨文件 matchMedia 污染（setup.ts 仅在 undefined 时设）。
  let origMatchMedia: typeof window.matchMedia
  beforeEach(() => {
    origMatchMedia = window.matchMedia
    stubMatchMedia(false)
  })
  afterEach(() => {
    window.matchMedia = origMatchMedia
  })

  it("活动边：琥珀描边 + 3 颗 SMIL 流动粒子（非 reduced-motion）", () => {
    const { container } = renderEdge(true)
    const particles = container.querySelector('[data-slot="run-edge-particles"]')
    expect(particles).toBeInTheDocument()
    expect(particles!.querySelectorAll("animateMotion").length).toBe(3)
    // BaseEdge path 描边为琥珀。
    const path = container.querySelector("path.react-flow__edge-path")
    expect(path?.getAttribute("style") ?? "").toContain("--amber")
  })

  it("空闲边：无粒子、无琥珀描边", () => {
    const { container } = renderEdge(false)
    expect(container.querySelector('[data-slot="run-edge-particles"]')).toBeNull()
    expect(container.querySelectorAll("animateMotion").length).toBe(0)
  })

  it("reduced-motion：活动边只渲静态琥珀描边，不挂 animateMotion", () => {
    stubMatchMedia(true)
    const { container } = renderEdge(true)
    expect(container.querySelectorAll("animateMotion").length).toBe(0)
    // 仍有琥珀描边（active 但不动）。
    const path = container.querySelector("path.react-flow__edge-path")
    expect(path?.getAttribute("style") ?? "").toContain("--amber")
  })
})
