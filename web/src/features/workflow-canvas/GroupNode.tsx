import {
  Handle,
  Position,
  type NodeProps,
  type Node,
} from "@xyflow/react"
import { ChevronDown } from "lucide-react"
import { cn } from "@/lib/utils"
import { nodeDisplay } from "./nodeColor"
import { STATUS_VAR } from "./statusColor"
import { RunCell } from "./RunCell"
import type { StudioNodeData } from "./canvasModel"
import type { GroupCell, GroupCounts } from "./runFanout"

// 运行态画布的「大功能容器」节点（type="groupRun"）：一个有逐页扇出资产的 storyboard
// 渲成可折叠容器，取代旧的 6×N 平铺独立子节点。
//   折叠：标题 + [N 项] 徽标 + chevron + 逐页状态区间条（Dagster 式，每页一格按状态着色）。
//   展开：上面 chrome 不变 + 逐页 RunCell 子卡网格（容器内 HTML 网格，非 RF sub-flow）。
// 折叠/展开是「视图态」（RunCanvas 本地 state），绝不回写 dependsOn。

export interface GroupRunNodeData extends StudioNodeData {
  cells: GroupCell[]
  counts: GroupCounts
  expanded: boolean
  // 当前在 Run Matrix 选中的页（高亮对应子卡）。
  selectedTodoId?: string
  // 折叠/展开（header 点击，stopPropagation 不触发整体选中）。
  onToggle: (nodeId: string) => void
  // 子卡点击 → 路由到 Run Matrix 选中该页。
  onSelectCell: (nodeId: string, cell: GroupCell) => void
}

export type GroupRunRFNode = Node<GroupRunNodeData, "groupRun">

export function GroupNode({ id, data }: NodeProps<GroupRunRFNode>) {
  const { label } = nodeDisplay(data.node)
  const { cells, counts, expanded } = data

  return (
    <div
      data-slot="group-run-node"
      data-expanded={expanded}
      className="flex flex-col gap-2 rounded-xl border border-line bg-bg-surface px-3 py-2.5 shadow-sm"
      style={{ width: expanded ? 360 : 280 }}
    >
      <Handle type="target" position={Position.Top} />

      {/* 头部：标题 + 类型 + [N 项] 徽标 + chevron。点击折叠/展开。 */}
      <button
        type="button"
        data-slot="group-run-header"
        onClick={(e) => {
          e.stopPropagation()
          data.onToggle(id)
        }}
        className="flex items-center gap-2 text-left"
        aria-expanded={expanded}
      >
        <div className="flex min-w-0 flex-col gap-0.5">
          <span className="truncate text-[12.5px] font-semibold text-text-1">
            {data.node.id}
          </span>
          <span className="text-[11px] text-text-2">{label}</span>
        </div>
        <span
          data-slot="group-run-badge"
          className="ml-auto shrink-0 rounded-full border border-line bg-bg-raised px-2 py-0.5 text-[10px] text-text-2"
        >
          {counts.total} 项
        </span>
        <ChevronDown
          aria-hidden
          className={cn(
            "h-3.5 w-3.5 shrink-0 text-text-3 transition-transform",
            expanded && "rotate-180",
          )}
        />
      </button>

      {/* 状态区间条：每页一格，按状态着色（done 绿 / running 琥珀 / failed 红 / pending 线色）。 */}
      <div
        data-slot="group-run-bar"
        className="flex h-1.5 gap-px overflow-hidden rounded-full"
      >
        {cells.map((c) => (
          <span
            key={c.todoId}
            aria-hidden
            data-status={c.status}
            className="flex-1"
            style={{ background: STATUS_VAR[c.status] }}
          />
        ))}
      </div>

      {/* 展开：逐页子卡网格。 */}
      {expanded && (
        <div
          data-slot="group-run-grid"
          className="grid grid-cols-3 gap-1.5 pt-1"
        >
          {cells.map((c) => (
            <RunCell
              key={c.todoId}
              cell={c}
              selected={c.todoId === data.selectedTodoId}
              onSelect={() => data.onSelectCell(id, c)}
            />
          ))}
        </div>
      )}

      <Handle type="source" position={Position.Bottom} />
    </div>
  )
}
