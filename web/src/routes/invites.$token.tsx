import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router"
import { useState } from "react"
import { Loader2 } from "lucide-react"
import { Button } from "@/components/studio/Button"
import { ThemeSwitcher } from "@/components/studio/ThemeSwitcher"
import { getAccessToken, ApiError } from "@/lib/apiClient"
import { acceptInvite } from "@/features/members/api"

// 接受邀请页（/invites/{token}）：被邀请人凭邀请链接进入。须先登录——未登录先跳 /login
// 并携 redirect 回跳，登录后回到本页。接受成功后按邀请角色获得组织成员身份，跳转到该组织。
// 这是「不再要求管理员手动添加既有用户」的自助入口：自助注册 → 登录 → 接受。
export const Route = createFileRoute("/invites/$token")({
  beforeLoad: ({ location }) => {
    if (getAccessToken() == null) {
      throw redirect({ to: "/login", search: { redirect: location.href } })
    }
  },
  component: AcceptInvitePage,
})

// 按 HTTP 状态映射中文文案（后端：404 无效 / 409 已处理 / 410 过期 / 403 邮箱不符）。
function acceptErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.status) {
      case 403:
        return "此邀请是发给另一个邮箱的，请用受邀邮箱对应的账号登录后再接受。"
      case 404:
        return "邀请不存在或链接有误。"
      case 409:
        return "此邀请已被接受或已撤销。"
      case 410:
        return "此邀请已过期，请联系管理员重新邀请。"
    }
  }
  return "接受邀请失败，请稍后重试。"
}

function AcceptInvitePage() {
  const { token } = Route.useParams()
  const navigate = useNavigate()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")

  async function handleAccept() {
    setPending(true)
    setError("")
    try {
      const res = await acceptInvite(token)
      // 已入组织：跳到该 org 的项目页。
      await navigate({
        to: "/orgs/$org/projects",
        params: { org: res.orgId },
      })
    } catch (err) {
      setError(acceptErrorMessage(err))
      setPending(false)
    }
  }

  return (
    <div className="grid min-h-dvh place-items-center bg-bg-base px-6">
      <div className="absolute right-4 top-4">
        <ThemeSwitcher />
      </div>
      <div className="flex w-[min(420px,100%)] flex-col gap-5 rounded-xl border border-line bg-bg-surface p-7">
        <header className="flex flex-col gap-1.5">
          <h1 className="font-heading text-[20px] font-bold text-text-1">接受组织邀请</h1>
          <p className="text-[13px] text-text-3">
            你收到一个加入 AI Studio 组织的邀请。确认后将以受邀角色加入该组织。
          </p>
        </header>

        {error !== "" && (
          <p className="rounded-md border border-danger/35 bg-danger/12 px-3 py-2 text-[12.5px] text-danger">
            {error}
          </p>
        )}

        <div className="flex gap-2">
          <Button
            type="button"
            variant="amber"
            disabled={pending}
            onClick={() => void handleAccept()}
          >
            {pending && <Loader2 className="mr-1.5 h-[15px] w-[15px] animate-spin" />}
            接受邀请
          </Button>
          <Button
            type="button"
            variant="ghost"
            disabled={pending}
            onClick={() => void navigate({ to: "/" })}
          >
            稍后再说
          </Button>
        </div>
      </div>
    </div>
  )
}
