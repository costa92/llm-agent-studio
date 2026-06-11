import { Fragment } from "react"
import { cn } from "@/lib/utils"

// 原型 .lineage / .lin-node：版本血缘链（v1 → v2 当前 …）。cur=amber 高亮。
// T10 建好供 T11 审核 Drawer 用（本里程碑 workbench 暂不展示血缘）。
export interface LineageNode {
  key: string
  label: string
  current?: boolean
}

export interface LineageTrailProps {
  nodes: LineageNode[]
  className?: string
}

export function LineageTrail({ nodes, className }: LineageTrailProps) {
  return (
    <div className={cn("flex flex-wrap items-center gap-1.5 text-[11px]", className)}>
      {nodes.map((node, i) => (
        <Fragment key={node.key}>
          {i > 0 && <span className="text-text-3">→</span>}
          <span
            data-current={node.current ? "" : undefined}
            className={cn(
              "rounded-md border px-2.5 py-[3px]",
              node.current
                ? "border-amber bg-amber/[0.08] text-amber"
                : "border-line text-text-3",
            )}
          >
            {node.label}
          </span>
        </Fragment>
      ))}
    </div>
  )
}
