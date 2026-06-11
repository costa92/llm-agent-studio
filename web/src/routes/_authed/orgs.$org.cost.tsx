import { createFileRoute } from "@tanstack/react-router"

// T4 占位 —— T13 实现成本中心（admin 门禁）。
export const Route = createFileRoute("/_authed/orgs/$org/cost")({
  component: CostPage,
})

function CostPage() {
  const { org } = Route.useParams()
  return (
    <div className="p-6 text-text-2">
      <h1 className="text-text-1">成本</h1>
      <p className="text-text-3">org: {org}</p>
    </div>
  )
}
