import { useNavigate } from "@tanstack/react-router"
import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import { usePlans } from "@/features/workflow/api"
import { statusLabel, statusVariant } from "@/features/projects/status"
import type { ProjectStatus } from "@/lib/types"

// 空态描述文案默认值：必须与项目详情页原文逐字一致，使概览页保持字节不变。
const DEFAULT_EMPTY_HINT =
  "该项目尚未开始任何生成任务。点击右上角“开始新生成”按钮以启动制片管线。"

// 运行历史表：从项目详情页概览抽出的 DRY 组件（错误/加载/空/表格四态）。
// 只渲染内部状态块（不含 <h3> / <section> 外壳），由调用方各自套自己的 chrome；
// 空态/错误态用 flex-1 撑满，设计为放进 flex flex-col 容器中。
export function ProjectRunsTable({
  projectId,
  org,
  emptyHint,
}: {
  projectId: string
  org: string
  emptyHint?: string
}) {
  const navigate = useNavigate()
  const plansQuery = usePlans(projectId)
  const plans = plansQuery.data || []

  if (plansQuery.isError) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center gap-3 text-center p-8">
        <p className="text-sm text-text-2">生成记录加载失败</p>
        <Button variant="ghost" onClick={() => void plansQuery.refetch()}>
          重试
        </Button>
      </div>
    )
  }

  if (plansQuery.isLoading) {
    return (
      <div className="flex-1 p-2">
        <Skeleton className="h-[200px] w-full rounded-xl" />
      </div>
    )
  }

  if (plans.length === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center text-center p-8">
        <div aria-hidden className="w-16 h-16 bg-bg-surface border border-line rounded-full flex items-center justify-center mb-4 text-text-3">
          📋
        </div>
        <p className="text-sm text-text-2 mb-2 font-semibold">暂无生成记录</p>
        <p className="text-xs text-text-3 max-w-xs mb-4">
          {emptyHint ?? DEFAULT_EMPTY_HINT}
        </p>
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-x-auto">
      <table className="w-full text-left border-collapse">
        <thead>
          <tr className="border-b border-line text-xs text-text-3">
            <th className="pb-3 font-semibold">序号</th>
            <th className="pb-3 font-semibold">生成记录 ID</th>
            <th className="pb-3 font-semibold">运行状态</th>
            <th className="pb-3 font-semibold">管线回落</th>
            <th className="pb-3 font-semibold">启动时间</th>
            <th className="pb-3 font-semibold text-right">操作</th>
          </tr>
        </thead>
        <tbody>
          {plans.map((plan, index) => {
            const runNum = plans.length - index

            return (
              <tr key={plan.id} className="border-b border-line/50 hover:bg-bg-surface/30 transition-colors text-sm text-text-1">
                <td className="py-3 font-semibold">#{runNum}</td>
                <td className="py-3 font-mono text-xs text-text-2 truncate max-w-[120px]" title={plan.id}>
                  {plan.id}
                </td>
                <td className="py-3">
                  <Badge variant={statusVariant(plan.status as ProjectStatus)}>
                    {statusLabel(plan.status as ProjectStatus)}
                  </Badge>
                </td>
                <td className="py-3">
                  {plan.fallbackUsed ? (
                    <Badge variant="rejected">
                      <span aria-hidden>⚠️</span> 已回落
                    </Badge>
                  ) : (
                    <span className="text-text-3 text-xs">-</span>
                  )}
                </td>
                <td className="py-3 text-xs text-text-2">
                  {new Date(plan.createdAt).toLocaleString()}
                </td>
                <td className="py-3 text-right">
                  {plan.workflowId ? (
                    <Button
                      variant="ghost"
                      onClick={() => {
                        void navigate({
                          to: "/orgs/$org/projects/$id/workflow",
                          params: { org, id: projectId },
                          search: { wf: plan.workflowId, run: plan.id },
                        })
                      }}
                    >
                      进入工作台 →
                    </Button>
                  ) : (
                    // 遗留空 workflowId run：无落地视图，不给跳转（跳了也会被门弹回）。
                    <span className="text-text-3 text-xs">—</span>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
