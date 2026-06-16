import { describe, it, expect, vi } from "vitest"
import { render, screen, fireEvent } from "@testing-library/react"
import { GraphView } from "./GraphView"
import type { GraphNode, GraphEdge } from "@/lib/projectState"

const nodes: GraphNode[] = [
  { id: "a", label: "剧本生成 #1", type: "script", status: "done" },
  { id: "b", label: "分镜拆解 #1", type: "storyboard", status: "running" },
  { id: "c", label: "素材生成 #1", type: "asset", status: "done", assetId: "as1" },
  { id: "d", label: "规划器 #1", type: "planner", status: "done" },
]
const edges: GraphEdge[] = [
  { from: "b", to: "a" },
  { from: "c", to: "b" },
]

describe("GraphView", () => {
  it("空 nodes 显占位", () => {
    render(<GraphView nodes={[]} edges={[]} />)
    expect(screen.getByText(/等待规划/)).toBeInTheDocument()
  })

  it("渲染每个节点的 label 与 data-status", () => {
    render(<GraphView nodes={nodes} edges={edges} />)
    expect(screen.getByText("剧本生成 #1")).toBeInTheDocument()
    expect(screen.getByText("分镜拆解 #1")).toBeInTheDocument()
    const cards = document.querySelectorAll('[data-slot="graph-node"]')
    expect(cards).toHaveLength(4)
    const runningCard = Array.from(cards).find(
      (c) => c.getAttribute("data-status") === "running",
    )
    expect(runningCard).toBeTruthy()
  })

  it("点击带 assetId 的节点触发 onSelectNode", () => {
    const onSelect = vi.fn()
    render(<GraphView nodes={nodes} edges={edges} onSelectNode={onSelect} />)
    fireEvent.click(screen.getByText("素材生成 #1"))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: "c", assetId: "as1" }))
  })

  it("点击 script 节点触发 onSelectNode（含 script 节点本身）", () => {
    const onSelect = vi.fn()
    render(<GraphView nodes={nodes} edges={edges} onSelectNode={onSelect} />)
    fireEvent.click(screen.getByText("剧本生成 #1"))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: "a", type: "script" }))
  })

  it("点击 storyboard 节点触发 onSelectNode", () => {
    const onSelect = vi.fn()
    render(<GraphView nodes={nodes} edges={edges} onSelectNode={onSelect} />)
    fireEvent.click(screen.getByText("分镜拆解 #1"))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: "b", type: "storyboard" }))
  })

  it("无 assetId 且非 script/storyboard 的节点点击不触发", () => {
    const onSelect = vi.fn()
    render(<GraphView nodes={nodes} edges={edges} onSelectNode={onSelect} />)
    fireEvent.click(screen.getByText("规划器 #1"))
    expect(onSelect).not.toHaveBeenCalled()
  })

  it("asset 节点无 assetId 时点击不触发", () => {
    const onSelect = vi.fn()
    const noAssetNodes: GraphNode[] = [
      { id: "x", label: "素材生成 #1", type: "asset", status: "pending" },
    ]
    render(<GraphView nodes={noAssetNodes} edges={[]} onSelectNode={onSelect} />)
    fireEvent.click(screen.getByText("素材生成 #1"))
    expect(onSelect).not.toHaveBeenCalled()
  })
})
