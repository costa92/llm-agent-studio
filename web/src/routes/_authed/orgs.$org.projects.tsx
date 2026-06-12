import { createFileRoute, Outlet } from "@tanstack/react-router"
import { requireOrgParam } from "@/app/org"

// 项目区段布局：列表（index）与工作台（$id）同挂此 org-scoped 段下，
// 故 org param 在工作台路由也存活，AppShell 左侧导航轨随之点亮。本布局仅透传 <Outlet/>。
export const Route = createFileRoute("/_authed/orgs/$org/projects")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: () => <Outlet />,
})
