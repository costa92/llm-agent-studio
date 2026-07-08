import { createFileRoute } from "@tanstack/react-router"
import { useRole } from "@/app/rbac"
import { AdminGate } from "@/features/cost/AdminGate"
import { AuditLogView } from "@/features/audit/AuditLogPage"
import { useAuditLog } from "@/features/audit/api"
import { requireOrgParam } from "@/app/org"

// 审计流水（admin-only）。导航入口按角色隐藏（AppShell）；直访路由 → AdminGate 拦（UX），
// 后端 GET /audit-log 仍强制 roleAdmin（非 admin 即便直访也只拿 403）。
export const Route = createFileRoute("/_authed/orgs/$org/audit")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: AuditPage,
})

function AuditPage() {
  const { org } = Route.useParams()
  const role = useRole(org)
  // 审计流水走 keyset 游标累积（useInfiniteQuery），多页信封串接成单数组。
  const audit = useAuditLog(org)
  const rows = audit.data?.pages.flatMap((p) => p.items)

  return (
    <AdminGate role={role}>
      <AuditLogView
        rows={rows}
        hasNextPage={audit.hasNextPage}
        isFetchingNextPage={audit.isFetchingNextPage}
        onLoadMore={() => void audit.fetchNextPage()}
        isLoading={audit.isLoading}
        isError={audit.isError}
        onRetry={() => void audit.refetch()}
      />
    </AdminGate>
  )
}
