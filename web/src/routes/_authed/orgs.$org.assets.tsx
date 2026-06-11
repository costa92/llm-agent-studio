import { createFileRoute } from "@tanstack/react-router"

// T4 占位 —— T12 实现资产库（过滤 + keyset 分页 + 血缘）。
export const Route = createFileRoute("/_authed/orgs/$org/assets")({
  component: AssetsPage,
})

function AssetsPage() {
  const { org } = Route.useParams()
  return (
    <div className="p-6 text-text-2">
      <h1 className="text-text-1">资产</h1>
      <p className="text-text-3">org: {org}</p>
    </div>
  )
}
