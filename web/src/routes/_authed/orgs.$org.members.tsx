import { createFileRoute } from "@tanstack/react-router"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { MembersPage } from "@/features/members/MembersPage"

// org 成员管理（org-admin only）。门禁同模型配置页：useRole 探针 + AdminGate；
// 后端对每个写操作仍强制 org-admin RBAC（列表 viewer 可读，写需 admin）。
export const Route = createFileRoute("/_authed/orgs/$org/members")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: MembersRoute,
})

function MembersRoute() {
  const { org } = Route.useParams()
  const role = useRole(org)

  return (
    <AdminGate role={role}>
      <MembersPage org={org} />
    </AdminGate>
  )
}
