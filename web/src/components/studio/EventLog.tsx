import type { ReactNode } from "react"
import { ChevronRight } from "lucide-react"
import { cn } from "@/lib/utils"
import type { LogLine } from "@/lib/timeline"
import { friendlyLabel } from "@/lib/timeline"
import { groupByEmphasis, EMPHASIS_TITLE, latestSummary } from "./EventLog.schema"

export interface EventLogProps {
  lines: LogLine[]
  className?: string
  emptyText?: ReactNode
}

// 默认折叠的「事件详情」：折叠态只显「最新动态」一行（最后一条 logFor 文案 + 计数），
// 展开显按 emphasis 分组（小标题 + 组内按 seq）。lines 受控（随 SSE 累积自然重渲染）。
export function EventLog({ lines, className, emptyText = "暂无事件" }: EventLogProps) {
  if (lines.length === 0) {
    return <div className={cn("py-1 text-[11px] text-text-3", className)}>{emptyText}</div>
  }
  const summary = latestSummary(lines)
  const groups = groupByEmphasis(lines)
  return (
    <details className={cn("group rounded-lg border border-line bg-bg-surface", className)}>
      <summary className="flex cursor-pointer items-center justify-between gap-2 px-3 py-2 text-[12px] text-text-2 marker:content-none">
        <span className="flex items-center gap-1 font-medium text-text-1">
          <ChevronRight aria-hidden className="size-3.5 transition-transform group-open:rotate-90" />
          事件详情
        </span>
        {summary && (
          <span className="truncate text-[11px] text-text-3">
            最新动态：<span>{summary.text}</span> · 共 {summary.count} 条
          </span>
        )}
      </summary>
      <div className="border-t border-line px-3 py-2">
        {groups.map((g) => (
          <div key={g.emphasis} className="mb-2 last:mb-0">
            <h5 className="mb-1 text-[10.5px] font-semibold tracking-[0.06em] text-text-3">
              {g.emphasis === "other" ? "其他" : EMPHASIS_TITLE[g.emphasis]}
            </h5>
            {g.lines.map((line) => (
              <div
                key={line.seq}
                className="border-b border-dashed border-line py-[3px] font-mono text-[11px] text-text-3 last:border-b-0"
              >
                {friendlyLabel(line)}
              </div>
            ))}
          </div>
        ))}
      </div>
    </details>
  )
}
