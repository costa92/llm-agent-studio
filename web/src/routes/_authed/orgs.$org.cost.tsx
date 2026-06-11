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
import { RANGE_PRESETS, rangeToParams } from "@/features/cost/format"

// T13：成本中心（admin-only）。导航入口已按角色隐藏（AppShell）；直访路由 → AdminGate 拦。
export const Route = createFileRoute("/_authed/orgs/$org/cost")({
  component: CostPage,
})

function CostPage() {
  const { org } = Route.useParams()
  const role = useRole(org)
  const [rangeValue, setRangeValue] = useState("30d")

  const preset = RANGE_PRESETS.find((p) => p.value === rangeValue) ?? RANGE_PRESETS[1]
  const range = rangeToParams(preset)

  const cost = useOrgCost(org, range)
  const projects = useOrgCostProjects(org, range)
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
