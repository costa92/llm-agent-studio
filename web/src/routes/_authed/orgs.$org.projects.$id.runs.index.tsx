import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { ProjectRunsTable } from "@/features/workflow/ProjectRunsTable"

// `/runs`（无 runId）落地页：完整运行记录列表（复用项目详情页概览的运行历史表）。
// 保留此路由文件本身即消除裸 /runs 深链触发的 notFoundError（父 $id 路由未配
// notFoundComponent，靠 /runs 有匹配路由来抑制）。
export const Route = createFileRoute(
  "/_authed/orgs/$org/projects/$id/runs/",
)({
  component: RunsIndexPage,
})

function RunsIndexPage() {
  const { org, id } = Route.useParams()
  const navigate = useNavigate()

  return (
    <div className="flex flex-col h-full overflow-y-auto bg-bg-surface p-6 sm:p-8">
      <header className="border-b border-line pb-6 mb-8">
        <button
          type="button"
          onClick={() => void navigate({ to: "/orgs/$org/projects/$id", params: { org, id } })}
          className="text-[12px] text-text-3 hover:text-text-1 mb-1"
        >
          ← 返回项目详情
        </button>
        <h1 className="text-2xl font-bold text-text-1 font-heading">运行记录</h1>
      </header>

      <section className="bg-bg-surface border border-line rounded-xl p-5 shadow-sm min-h-[400px] flex flex-col">
        <ProjectRunsTable projectId={id} org={org} />
      </section>
    </div>
  )
}
