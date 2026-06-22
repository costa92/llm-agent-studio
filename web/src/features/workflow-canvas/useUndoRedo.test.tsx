import { describe, expect, it } from "vitest"
import { act, render } from "@testing-library/react"
import { ReactFlow, ReactFlowProvider, useReactFlow } from "@xyflow/react"
import { useUndoRedo } from "./useUndoRedo"
import type { RFNode, RFEdge } from "./canvasModel"

function rfNode(id: string, x: number): RFNode {
  return {
    id,
    type: "studio",
    position: { x, y: 0 },
    data: { node: { id, type: "script", promptId: "", dependsOn: [] } },
  }
}

// 测试桥：把 hook + ReactFlow store 句柄暴露给测试。所有状态变更包在 act() 内。
// 注意：必须挂载真实 <ReactFlow>（defaultNodes 初始化内部 store），否则 jsdom 下
// 仅用裸 setNodes 种子的节点在后续 setNodes 重排时会被 store 丢弃。
type Harness = {
  hook: ReturnType<typeof useUndoRedo>
  getX: () => number[]
  move: (x: number) => void
}

function mountHarness(opts?: { maxHistory?: number }) {
  const ref: { current: Harness | null } = { current: null }
  function Probe() {
    const hook = useUndoRedo(opts)
    const { getNodes, setNodes } = useReactFlow<RFNode, RFEdge>()
    ref.current = {
      hook,
      getX: () => getNodes().map((n) => n.position.x),
      // 用函数式 updater 移动现有节点（保留 store 内部字段），模拟真实变更。
      move: (x) => setNodes((nds) => nds.map((n) => ({ ...n, position: { ...n.position, x } }))),
    }
    return null
  }
  render(
    <ReactFlowProvider>
      <div style={{ width: 800, height: 600 }}>
        <ReactFlow defaultNodes={[rfNode("a", 0)]} defaultEdges={[]}>
          <Probe />
        </ReactFlow>
      </div>
    </ReactFlowProvider>,
  )
  return ref as { current: Harness }
}

describe("useUndoRedo", () => {
  it("undo restores the snapshot taken before a mutation", () => {
    const h = mountHarness()
    expect(h.current.getX()).toEqual([0])

    act(() => h.current.hook.takeSnapshot())
    act(() => h.current.move(999))
    expect(h.current.getX()).toEqual([999])
    expect(h.current.hook.canUndo).toBe(true)

    act(() => h.current.hook.undo())
    expect(h.current.getX()).toEqual([0])
  })

  it("redo re-applies an undone mutation", () => {
    const h = mountHarness()
    act(() => h.current.hook.takeSnapshot())
    act(() => h.current.move(999))
    act(() => h.current.hook.undo())
    expect(h.current.getX()).toEqual([0])
    expect(h.current.hook.canRedo).toBe(true)

    act(() => h.current.hook.redo())
    expect(h.current.getX()).toEqual([999])
    expect(h.current.hook.canRedo).toBe(false)
  })

  it("takeSnapshot after an undo clears the redo future", () => {
    const h = mountHarness()
    act(() => h.current.hook.takeSnapshot())
    act(() => h.current.move(999))
    act(() => h.current.hook.undo())
    expect(h.current.hook.canRedo).toBe(true)

    // 撤销后再做一次新变更 → future 被清空。
    act(() => h.current.hook.takeSnapshot())
    act(() => h.current.move(555))
    expect(h.current.hook.canRedo).toBe(false)
    act(() => h.current.hook.redo())
    // redo 无效（future 空），状态保持 555。
    expect(h.current.getX()).toEqual([555])
  })

  it("caps history at maxHistory, dropping the oldest", () => {
    const h = mountHarness({ maxHistory: 2 })
    // 三次 snapshot（x=0,1,2 状态），past 上限 2 → 最旧（x=0 那次）被丢。
    act(() => h.current.hook.takeSnapshot()) // snapshot of x=0
    act(() => h.current.move(1))
    act(() => h.current.hook.takeSnapshot()) // snapshot of x=1
    act(() => h.current.move(2))
    act(() => h.current.hook.takeSnapshot()) // snapshot of x=2 — drops x=0
    act(() => h.current.move(3))

    // 撤销：x=2, x=1，然后 past 空（x=0 已被丢），canUndo=false。
    act(() => h.current.hook.undo())
    expect(h.current.getX()).toEqual([2])
    act(() => h.current.hook.undo())
    expect(h.current.getX()).toEqual([1])
    expect(h.current.hook.canUndo).toBe(false)
  })
})
