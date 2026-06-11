import { createRootRoute, Outlet, useLocation, useNavigate } from "@tanstack/react-router"
import { Button } from "@/components/studio/Button"
import { OrgLanding } from "@/app/OrgLanding"
import { hasEmptyOrgPath } from "@/app/org"
import { Toaster } from "@/components/ui/sonner"

export const Route = createRootRoute({
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
