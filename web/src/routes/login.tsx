import { createFileRoute } from "@tanstack/react-router"
import { z } from "zod"

// T4 占位：登录路由作为 `_authed` beforeLoad 的重定向目标。
// T6 在此实现 rhf+zod 登录表单 + AuthProvider 接入；登录成功后回跳 `redirect`。
const loginSearchSchema = z.object({
  redirect: z.string().optional(),
})

export const Route = createFileRoute("/login")({
  validateSearch: loginSearchSchema,
  component: LoginPage,
})

function LoginPage() {
  return (
    <div className="grid h-screen place-items-center bg-bg-base text-text-2">
      <p>登录</p>
    </div>
  )
}
