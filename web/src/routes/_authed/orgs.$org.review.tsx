import { createFileRoute } from "@tanstack/react-router"

// T4 占位 —— T11 实现 HITL 审核看板（admin 门禁）。
export const Route = createFileRoute("/_authed/orgs/$org/review")({
  component: ReviewPage,
})

function ReviewPage() {
  const { org } = Route.useParams()
  return (
    <div className="p-6 text-text-2">
      <h1 className="text-text-1">审核</h1>
      <p className="text-text-3">org: {org}</p>
    </div>
  )
}
