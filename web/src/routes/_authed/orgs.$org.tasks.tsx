import { createFileRoute } from "@tanstack/react-router"
import { requireOrgParam } from "@/app/org"
import { TaskCenterPage } from "@/features/tasks/TaskCenterPage"

// 任务中心（项目运行看板）。org 任意成员 / viewer 可读 —— 无 AdminGate（只读跨项目看板）。
export const Route = createFileRoute("/_authed/orgs/$org/tasks")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: TasksRoute,
})

function TasksRoute() {
  const { org } = Route.useParams()
  return <TaskCenterPage org={org} />
}
