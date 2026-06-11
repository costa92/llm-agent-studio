import { createFileRoute } from "@tanstack/react-router"

// T4 占位 —— T9 实现项目列表 + 建项目视图。
export const Route = createFileRoute("/_authed/orgs/$org/projects")({
  component: ProjectsPage,
})

function ProjectsPage() {
  const { org } = Route.useParams()
  return (
    <div className="p-6 text-text-2">
      <h1 className="text-text-1">项目</h1>
      <p className="text-text-3">org: {org}</p>
    </div>
  )
}
