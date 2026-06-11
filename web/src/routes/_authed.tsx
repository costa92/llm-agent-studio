import { createFileRoute, Outlet, redirect, useParams } from "@tanstack/react-router"
import { getAccessToken } from "@/lib/apiClient"
import { AppShell } from "@/app/AppShell"

// 受保护布局：无 access token → 重定向 /login（携 redirect 回跳）。
// T5/T6 在登录流里 setAccessToken；角色门禁（isAdmin）由 T6 rbac 注入到 AppShell。
export const Route = createFileRoute("/_authed")({
  beforeLoad: ({ location }) => {
    if (getAccessToken() == null) {
      throw redirect({ to: "/login", search: { redirect: location.href } })
    }
  },
  component: AuthedLayout,
})

function AuthedLayout() {
  // org 来自子路由 params（如 /orgs/$org/...）；顶层 /_authed/ 重定向前可能尚无。
  const params = useParams({ strict: false }) as { org?: string }
  return (
    <AppShell org={params.org ?? ""}>
      <Outlet />
    </AppShell>
  )
}
