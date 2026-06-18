import { Badge } from "@/components/studio/Badge"
import { cn } from "@/lib/utils"
import type { ProjectState } from "@/lib/projectState"
import { computeRunSummary } from "./RunSummary.schema"

export interface RunSummaryProps {
  state: ProjectState
  className?: string
}

// 运行总状态/进度概览条：runStatus 文案徽标 + 阶段 X/N + 素材 done/total + 细进度条。
// 纯表现，读权威 state；不引入新数据。与底部 SlateBar 分工：SlateBar=运行中底部动效，
// RunSummary=常驻信息条（含完成/失败态）。
// isCustom 工作流（stages 为空）退化为节点计数（done nodes / total nodes）。
export function RunSummary({ state, className }: RunSummaryProps) {
  const s = computeRunSummary(state)
  const pct = Math.max(0, Math.min(1, s.ratio)) * 100
  return (
    <div
      data-slot="run-summary"
      className={cn(
        "flex flex-wrap items-center gap-x-4 gap-y-1.5 border-b border-line px-4 py-2 sm:px-6",
        className,
      )}
    >
      <Badge variant={s.variant}>{s.runLabel}</Badge>
      <span className="font-mono text-[12px] text-text-2">{`阶段 ${s.stagesDone}/${s.stagesTotal}`}</span>
      <span className="font-mono text-[12px] text-text-2">{`素材 ${s.assetsDone}/${s.assetsTotal}`}</span>
      <span
        role="progressbar"
        aria-valuenow={Math.round(pct)}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label="运行进度"
        className="ml-auto h-2 w-40 max-w-[40%] overflow-hidden rounded-full bg-bg-base"
      >
        <span
          className="block h-full rounded-full bg-amber transition-[width] duration-300"
          style={{ width: `${pct}%` }}
        />
      </span>
    </div>
  )
}
