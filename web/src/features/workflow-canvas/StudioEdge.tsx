import { useState } from "react"
import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  useReactFlow,
  type Edge,
  type EdgeProps,
} from "@xyflow/react"
import { cn } from "@/lib/utils"
import { useCanvasActions } from "./CanvasActionsContext"

type StudioEdgeData = { active?: boolean }

// 自定义边（Phase B）：在边中点渲染一个小控制簇。
// 常驻一个淡「+」按钮（插入拆分）；hover 时额外显示「删除」。
// EdgeLabelRenderer 子节点须 pointerEvents:'all' + nodrag nopan，否则点击会被画布平移吃掉。
export function StudioEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  style,
  data,
}: EdgeProps<Edge<StudioEdgeData>>) {
  const [hover, setHover] = useState(false)
  const { onDeleteEdge, onInsertOnEdge } = useCanvasActions()
  const { flowToScreenPosition } = useReactFlow()
  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })

  const active = data?.active === true

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        markerEnd={markerEnd}
        style={style}
        className={cn(active && "studio-edge-active")}
        data-slot="studio-edge-path"
        data-active={active ? "true" : "false"}
      />
      <EdgeLabelRenderer>
        <div
          data-slot="studio-edge-controls"
          className="nodrag nopan absolute flex items-center gap-1"
          style={{
            transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`,
            pointerEvents: "all",
          }}
          onMouseEnter={() => setHover(true)}
          onMouseLeave={() => setHover(false)}
        >
          <button
            type="button"
            aria-label="在此处插入节点"
            title="插入节点"
            onClick={() => {
              const screen = flowToScreenPosition({ x: labelX, y: labelY })
              onInsertOnEdge(id, screen.x, screen.y)
            }}
            className="grid h-5 w-5 place-items-center rounded-full border border-line bg-bg-raised text-[12px] leading-none text-text-2 opacity-60 shadow hover:opacity-100 hover:text-text-1"
          >
            +
          </button>
          {hover && (
            <button
              type="button"
              aria-label="删除连线"
              title="删除连线"
              onClick={() => onDeleteEdge(id)}
              className="grid h-5 w-5 place-items-center rounded-full border border-line bg-bg-raised text-[12px] leading-none text-danger shadow hover:bg-bg-surface"
            >
              ×
            </button>
          )}
        </div>
      </EdgeLabelRenderer>
    </>
  )
}
