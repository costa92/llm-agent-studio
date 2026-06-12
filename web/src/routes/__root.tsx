import { createRootRoute, Outlet, useLocation, useNavigate } from "@tanstack/react-router"
import { Button } from "@/components/studio/Button"
import { OrgLanding } from "@/app/OrgLanding"
import { hasEmptyOrgPath } from "@/app/org"
import { tryRestoreSession } from "@/lib/apiClient"
import { Toaster } from "@/components/ui/sonner"

export const Route = createRootRoute({
  // 冷启动/硬刷新/深链：内存 token 丢失但刷新 cookie 仍有效时，
  // 在子路由 _authed 守卫跑前先静默恢复一次会话（幂等，已有 token 不发请求）。
  beforeLoad: async () => {
    await tryRestoreSession()
  },
  component: RootComponent,
  notFoundComponent: RootNotFound,
})

function RootComponent() {
  return (
    <>
      <Outlet />
      <Toaster position="bottom-right" />
    </>
  )
}

function RootNotFound() {
  const location = useLocation()
  const navigate = useNavigate()

  if (hasEmptyOrgPath(location.pathname)) {
    return <OrgLanding />
  }

  return (
    <div className="grid min-h-screen place-items-center bg-bg-base px-6 text-text-1">
      <div className="flex w-[min(420px,100%)] flex-col items-center gap-4 text-center">
        <div className="font-heading text-[13px] font-semibold text-amber">404</div>
        <h1 className="font-heading text-[22px] font-bold">页面不存在</h1>
        <p className="text-[12.5px] text-text-3">
          当前链接没有匹配的工作区页面。
        </p>
        <Button type="button" variant="amber" onClick={() => void navigate({ to: "/" })}>
          返回入口
        </Button>
      </div>
    </div>
  )
}
