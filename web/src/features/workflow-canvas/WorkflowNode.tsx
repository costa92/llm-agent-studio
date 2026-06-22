import {
  Handle,
  NodeToolbar,
  Position,
  type NodeProps,
  type Node,
} from "@xyflow/react"
import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"
import type { StudioNodeData } from "./canvasModel"
import { useCanvasActions } from "./CanvasActionsContext"

// ReactFlow 自定义节点（编辑视图）：圆角卡片 + 左侧 agent 语义色条。
// 只展示节点 id（label）与中文类型标签，无运行态样式（运行态属 GraphView 的职责）。
// target/source 句柄分别在上/下，对齐自顶向下的种子布局方向。
export type StudioRFNode = Node<StudioNodeData, "studio">

export function WorkflowNode({ id, data, selected }: NodeProps<StudioRFNode>) {
  const node = data.node
  const color = NODE_COLOR[node.type] ?? "var(--line)"
  const typeLabel = TYPE_LABEL[node.type] ?? node.type
  const { onDuplicateNode, onDeleteNode } = useCanvasActions()

  return (
    <div
      data-slot="canvas-node"
      className="flex items-stretch gap-2.5 rounded-lg border border-line bg-bg-surface px-3 py-2 shadow-sm min-w-[140px]"
    >
      {/* 选中时浮出工具条：复制 / 删除。NodeToolbar portal 到 flow viewport，
          必须在 <ReactFlow> 内（CanvasInner 满足）。 */}
      <NodeToolbar isVisible={selected} position={Position.Top}>
        <div className="flex items-center gap-1 rounded border border-line bg-bg-raised p-1 shadow-lg">
          <button
            type="button"
            onClick={() => onDuplicateNode(id)}
            className="rounded px-2 py-0.5 text-[11px] text-text-1 hover:bg-bg-surface"
          >
            复制
          </button>
          <button
            type="button"
            onClick={() => onDeleteNode(id)}
            className="rounded px-2 py-0.5 text-[11px] text-danger hover:bg-bg-surface"
          >
            删除
          </button>
        </div>
      </NodeToolbar>
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
