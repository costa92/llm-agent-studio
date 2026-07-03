import { useState } from "react"
import { ChevronDown, ChevronRight } from "lucide-react"
import { cn } from "@/lib/utils"
import { formatCount, formatCurrency } from "@/features/cost/format"
import { usePlanCost } from "@/features/workflow/api"
import { todoTypeLabel } from "./nodeColor"

export interface RunCostSummaryProps {
  projectId: string
  planId: string
  // run 进行中时轮询刷新（新账本行随执行陆续落库）。
  live?: boolean
  className?: string
}

// 本次运行的 token/成本汇总：总计（费用 / tokens / 生成次数按 kind）+ 可展开按节点分解。
// 数据来自 GET /api/projects/{id}/plans/{planId}/cost（admin，成本资源族门槛）——
// 宿主（RunCanvas 左栏）须以 isAdmin 门控挂载，非 admin 不发请求。
// run 无账本行 → 空态文案而非一排 0。
export function RunCostSummary({ projectId, planId, live, className }: RunCostSummaryProps) {
  const q = usePlanCost(projectId, planId, live)
  const [open, setOpen] = useState(false)

  if (q.isLoading) {
    return <p className={cn("text-[12px] text-text-3", className)}>成本加载中…</p>
  }
  if (q.isError || !q.data) {
    return <p className={cn("text-[12px] text-text-3", className)}>成本数据暂不可用</p>
  }
  const c = q.data
  if (c.generations === 0) {
    return (
      <p data-slot="run-cost-empty" className={cn("text-[12px] text-text-3", className)}>
        本次运行暂无用量记录
      </p>
    )
  }
  // 按 kind 生成次数：「chat 1 · image 2」。键排序保证渲染稳定。
  const kinds = Object.entries(c.kindCounts)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([kind, n]) => `${kind} ${n}`)
    .join(" · ")

  return (
    <div data-slot="run-cost-summary" className={cn("flex flex-col gap-1.5", className)}>
      <div className="flex items-baseline justify-between">
        <span className="text-[12px] text-text-2">费用</span>
        <span className="font-mono text-[13px] text-text-1">{formatCurrency(c.costMicros)}</span>
      </div>
      <div className="flex items-baseline justify-between">
        <span className="text-[12px] text-text-2">Tokens</span>
        <span className="font-mono text-[12px] text-text-1">{formatCount(c.tokens)}</span>
      </div>
      <div className="flex items-baseline justify-between gap-2">
        <span className="shrink-0 text-[12px] text-text-2">生成</span>
        <span className="truncate font-mono text-[12px] text-text-2" title={kinds}>
          {kinds}
        </span>
      </div>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1 text-[12px] text-text-3 transition-colors hover:text-text-1"
      >
        {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        按节点分解 ({c.todos.length})
      </button>
      {open && (
        <ul data-slot="run-cost-todos" className="flex flex-col gap-1.5 border-t border-line pt-1.5">
          {c.todos.map((t) => (
            <li key={`${t.todoId}-${t.kind}-${t.provider}-${t.model}`} className="flex flex-col">
              <div className="flex items-baseline justify-between gap-2">
                <span className="truncate text-[12px] text-text-1">
                  {todoTypeLabel(t.todoType)}
                </span>
                <span className="shrink-0 font-mono text-[12px] text-text-1">
                  {formatCurrency(t.costMicros)}
                </span>
              </div>
              <div className="flex items-baseline justify-between gap-2 text-[11px] text-text-3">
                <span className="truncate" title={`${t.provider}/${t.model}`}>
                  {t.kind}
                  {t.model ? ` · ${t.model}` : ""}
                </span>
                <span className="shrink-0 font-mono">
                  {t.tokens > 0 ? `${formatCount(t.tokens)} tok` : `×${t.generations}`}
                </span>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
