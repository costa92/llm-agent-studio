import { createFileRoute, redirect } from "@tanstack/react-router"

// `/runs`（无 runId）不是一个有意义的落地页：把它重定向到项目工作台
// （已列出每个工作流 + 其最近一次 run）。同时消除裸 /runs 深链触发的
// notFoundError（父 $id 路由未配 notFoundComponent）。
export const Route = createFileRoute(
  "/_authed/orgs/$org/projects/$id/runs/",
)({
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/orgs/$org/projects/$id", params })
  },
})
