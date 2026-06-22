import {
  Handle,
  NodeToolbar,
  Position,
  type NodeProps,
  type Node,
} from "@xyflow/react"
import { cn } from "@/lib/utils"
import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"
import type { StudioNodeData } from "./canvasModel"
import type { RunNodeStatus } from "./runOverlay"
import { useCanvasActions } from "./CanvasActionsContext"

// ReactFlow 自定义节点。编辑视图：圆角卡片 + 左侧 agent 语义色条 + 选中工具条。
// 运行视图（data.run 存在）：叠加该 run 的执行状态指示器（复用 GraphView 视觉语言：
// done 填充✓ / running 琥珀虚线转环 / failed danger / pending 中性），并隐藏编辑工具条
// （运行模式只读）。
export type StudioRFNode = Node<StudioNodeData, "studio">

export function WorkflowNode({ id, data, selected }: NodeProps<StudioRFNode>) {
  const node = data.node
  const color = NODE_COLOR[node.type] ?? "var(--line)"
  const typeLabel = TYPE_LABEL[node.type] ?? node.type
  const { onDuplicateNode, onDeleteNode } = useCanvasActions()

  // 运行模式：data.run 注入了该节点对应 run 节点的状态（见 runOverlay.overlayRunStatus）。
  const run = data.run
  const isRunMode = run != null
  const runStatus = run?.status

  return (
    <div
      data-slot="canvas-node"
      data-status={isRunMode ? (runStatus ?? "pending") : undefined}
      className="flex items-center gap-2.5 rounded-lg border border-line bg-bg-surface px-3 py-2 shadow-sm min-w-[140px]"
    >
      {/* 选中时浮出工具条：复制 / 删除。仅编辑模式（运行模式只读，隐藏工具条）。
          NodeToolbar portal 到 flow viewport，必须在 <ReactFlow> 内（CanvasInner 满足）。 */}
      {!isRunMode && (
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
      )}
      <Handle type="target" position={Position.Top} />
      {isRunMode ? (
        // 运行状态指示器（复用 GraphView GraphNodeCard 的类形 + token）。
        <RunStatusDot status={runStatus} color={color} />
      ) : (
        <span
          aria-hidden
          data-slot="canvas-node-bar"
          className="w-1 shrink-0 self-stretch rounded-full"
          style={{ backgroundColor: color }}
        />
      )}
      <div className="flex flex-col gap-0.5">
        <span className="text-[12.5px] font-semibold text-text-1">{node.id}</span>
        <span className="text-[11px] text-text-2">{typeLabel}</span>
      </div>
      <Handle type="source" position={Position.Bottom} />
    </div>
  )
}

// 运行状态圆点（复用 GraphView 视觉：done 填 var(--cur) + 白✓ / running 琥珀 + 虚线转环 /
// failed border-danger bg-danger/15 / pending 中性 border-line）。amber token only。
function RunStatusDot({
  status,
  color,
}: {
  status?: RunNodeStatus["status"]
  color: string
}) {
  const isDone = status === "done"
  const isRunning = status === "running"
  const isFailed = status === "failed"
  return (
    <span
      aria-hidden
      data-slot="canvas-node-status"
      className={cn(
        "relative grid h-7 w-7 shrink-0 place-items-center rounded-full border-2 bg-bg-base",
        isDone && "border-[var(--cur)] bg-[var(--cur)]",
        isRunning && "border-amber",
        isFailed && "border-danger bg-danger/15",
        !isDone && !isRunning && !isFailed && "border-line",
      )}
      style={{ ["--cur" as string]: color }}
    >
      {isRunning && (
        <span
          aria-hidden
          className="absolute -inset-1.5 rounded-full border-2 border-dashed border-amber motion-safe:animate-[spin_3s_linear_infinite]"
        />
      )}
      <span
        className={cn(
          "font-sans text-[10px] font-bold",
          isDone ? "text-bg-base" : isFailed ? "text-danger" : "text-text-3",
        )}
      >
        {isDone ? "✓" : ""}
      </span>
    </span>
  )
}
