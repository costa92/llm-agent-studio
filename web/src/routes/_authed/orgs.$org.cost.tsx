import { createFileRoute } from "@tanstack/react-router"
import { useState } from "react"
import { useRole } from "@/app/rbac"
import { AdminGate } from "@/features/cost/AdminGate"
import { CostCenterView } from "@/features/cost/CostCenterPage"
import {
  useGenerations,
  useOrgCost,
  useOrgCostProjects,
} from "@/features/cost/api"
import { requireOrgParam } from "@/app/org"

// T13：成本中心（admin-only）。导航入口已按角色隐藏（AppShell）；直访路由 → AdminGate 拦。
export const Route = createFileRoute("/_authed/orgs/$org/cost")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: CostPage,
})

function CostPage() {
  const { org } = Route.useParams()
  const role = useRole(org)
  const [rangeValue, setRangeValue] = useState("30d")

  // 钩子接 presetValue（稳定字符串）；range 生成挪进 queryFn 闭包，
  // 避免每次 render 推新 from/to 时间戳让 queryKey 永变 → refetch loop。
  const cost = useOrgCost(org, rangeValue)
  const projects = useOrgCostProjects(org, rangeValue)
  const generations = useGenerations(org)

  return (
    <AdminGate role={role}>
      <CostCenterView
        aggregate={cost.data}
        projects={projects.data}
        generations={generations.data}
        isLoading={cost.isLoading || projects.isLoading || generations.isLoading}
        isError={cost.isError || projects.isError || generations.isError}
        onRetry={() => {
          void cost.refetch()
          void projects.refetch()
          void generations.refetch()
        }}
        rangeValue={rangeValue}
        onRangeChange={setRangeValue}
      />
    </AdminGate>
  )
}
