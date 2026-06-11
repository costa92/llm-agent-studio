import {
  createFileRoute,
  Outlet,
  redirect,
  useNavigate,
  useParams,
} from "@tanstack/react-router"
import { LogOut } from "lucide-react"
import { getAccessToken } from "@/lib/apiClient"
import { AppShell } from "@/app/AppShell"
import { useAuth } from "@/app/auth"
import { useRole } from "@/app/rbac"

// 受保护布局：无 access token → 重定向 /login（携 redirect 回跳）。
// 登录流在 auth.login 里 setAccessToken；角色门禁（isAdmin）由 rbac.useRole 探针推断后注入 AppShell。
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
  const org = params.org ?? ""
  const { isAdmin } = useRole(org)
  const { logout } = useAuth()
  const navigate = useNavigate()

  return (
    <AppShell
      org={org}
      isAdmin={isAdmin}
      avatar={
        <button
          type="button"
          title="登出"
          aria-label="登出"
          onClick={async () => {
            await logout()
            navigate({ to: "/login" })
          }}
          className="grid h-[30px] w-[30px] place-items-center rounded-full bg-bg-raised text-text-3 transition-colors hover:text-text-1"
        >
          <LogOut className="h-[16px] w-[16px]" />
        </button>
      }
    >
      <Outlet />
    </AppShell>
  )
}
