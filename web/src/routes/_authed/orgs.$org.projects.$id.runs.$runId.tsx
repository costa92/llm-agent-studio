import { createFileRoute, Navigate } from "@tanstack/react-router"
import { Skeleton } from "@/components/ui/skeleton"
import { usePlans } from "@/features/workflow/api"

export const Route = createFileRoute(
  "/_authed/orgs/$org/projects/$id/runs/$runId"
)({
  component: RunRouteGate,
})

// /runs/$runId 纯重定向门（永久保留：告警邮件 permalink 指向此路由，见
// internal/alerts/notifier.go）。plan.workflowId 非空 → 画布运行模式
// /workflow?wf=&run=；其余情况（遗留空 workflowId / plan 不存在 / 加载失败）
// → 回项目详情页。均 replace 不留历史。
function RunRouteGate() {
  const { org, id, runId } = Route.useParams()
  const plansQuery = usePlans(id)
  const plan = plansQuery.data?.find((p) => p.id === runId)

  if (plansQuery.isLoading) {
    return (
      <div className="p-6">
        <Skeleton className="h-[60px] w-full rounded-xl" />
      </div>
    )
  }
  if (plan?.workflowId) {
    return (
      <Navigate
        to="/orgs/$org/projects/$id/workflow"
        params={{ org, id }}
        search={{ wf: plan.workflowId, run: runId }}
        replace
      />
    )
  }
  return <Navigate to="/orgs/$org/projects/$id" params={{ org, id }} replace />
}
