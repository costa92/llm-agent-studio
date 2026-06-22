import { Handle, Position, type NodeProps, type Node } from "@xyflow/react"
import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"
import type { StudioNodeData } from "./canvasModel"

// ReactFlow 自定义节点（编辑视图）：圆角卡片 + 左侧 agent 语义色条。
// 只展示节点 id（label）与中文类型标签，无运行态样式（运行态属 GraphView 的职责）。
// target/source 句柄分别在上/下，对齐自顶向下的种子布局方向。
export type StudioRFNode = Node<StudioNodeData, "studio">

export function WorkflowNode({ data }: NodeProps<StudioRFNode>) {
  const node = data.node
  const color = NODE_COLOR[node.type] ?? "var(--line)"
  const typeLabel = TYPE_LABEL[node.type] ?? node.type

  return (
    <div
      data-slot="canvas-node"
      className="flex items-stretch gap-2.5 rounded-lg border border-line bg-bg-surface px-3 py-2 shadow-sm min-w-[140px]"
    >
      <Handle type="target" position={Position.Top} />
      <span
        aria-hidden
        data-slot="canvas-node-bar"
        className="w-1 shrink-0 rounded-full"
        style={{ backgroundColor: color }}
      />
      <div className="flex flex-col gap-0.5">
        <span className="text-[12.5px] font-semibold text-text-1">{node.id}</span>
        <span className="text-[11px] text-text-2">{typeLabel}</span>
      </div>
      <Handle type="source" position={Position.Bottom} />
    </div>
  )
}
