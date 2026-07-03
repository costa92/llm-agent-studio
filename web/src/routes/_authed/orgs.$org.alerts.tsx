import { createFileRoute } from "@tanstack/react-router"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { AlertSettingsView } from "@/features/alerts/AlertSettingsPage"

// org 告警设置（org-admin only）。门禁同其余 org 配置页：useRole 探针 + AdminGate；
// 后端 GET/PUT /api/orgs/{org}/alert-settings 仍强制 roleAdmin RBAC。
export const Route = createFileRoute("/_authed/orgs/$org/alerts")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: AlertsRoute,
})

function AlertsRoute() {
  const { org } = Route.useParams()
  const role = useRole(org)
  return (
    <AdminGate role={role}>
      <AlertSettingsView org={org} />
    </AdminGate>
  )
}
