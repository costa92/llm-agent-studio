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
import { cleanOrg } from "@/app/org"
import { useRole } from "@/app/rbac"
import { usePlatformWhoami } from "@/features/platform/api"
import { Button } from "@/components/studio/Button"

// 受保护布局：无 access token → 重定向 /login（携 redirect 回跳）。
// 登录流在 auth.login 里 setAccessToken；角色门禁（isAdmin）由 rbac.useRole 探针推断后注入 AppShell。
export const Route = createFileRoute("/_authed")({
  beforeLoad: ({ location }) => {
    if (getAccessToken() == null) {
      throw redirect({ to: "/login", search: { redirect: location.href } })
    }
  },
  component: AuthedLayout,
  // 已登录用户访问不存在的工作区路径：404 渲在 AppShell 的 Outlet 位置，
  // 保留侧栏 + 顶栏，不把用户甩到无壳的全屏 404（root 的 notFound 只兜未登录场景）。
  notFoundComponent: AuthedNotFound,
})

// AppShell 内的 404：填在 AuthedLayout 的 Outlet 位置，导航壳仍在。
function AuthedNotFound() {
  const navigate = useNavigate()
  const params = useParams({ strict: false }) as { org?: string }
  const org = cleanOrg(params.org)
  return (
    <div className="grid h-full place-items-center px-6 text-text-1">
      <div className="flex w-[min(420px,100%)] flex-col items-center gap-4 text-center">
        <div className="font-heading text-[13px] font-semibold text-amber">404</div>
        <h1 className="font-heading text-[22px] font-bold">页面不存在</h1>
        <p className="text-[12.5px] text-text-3">当前链接没有匹配的工作区页面。</p>
        <Button
          type="button"
          variant="amber"
          onClick={() => {
            if (org) void navigate({ to: "/orgs/$org/projects", params: { org } })
            else void navigate({ to: "/" })
          }}
        >
          {org ? "返回项目" : "返回入口"}
        </Button>
      </div>
    </div>
  )
}

function AuthedLayout() {
  // org 来自子路由 params（如 /orgs/$org/...）；顶层 /_authed/ 重定向前可能尚无。
  const params = useParams({ strict: false }) as { org?: string }
  const org = cleanOrg(params.org)
  const { isAdmin } = useRole(org)
  // 平台超级管理员门禁（非 org-scoped）：whoami 任意登录用户可调，决定是否展示「平台」导航入口。
  const platformWhoami = usePlatformWhoami()
  const { logout } = useAuth()
  const navigate = useNavigate()

  return (
    <AppShell
      org={org}
      isAdmin={isAdmin}
      isPlatformAdmin={platformWhoami.data ?? false}
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
