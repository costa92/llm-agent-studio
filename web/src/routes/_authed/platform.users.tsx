import { createFileRoute } from "@tanstack/react-router"
import { AllUsersPage } from "@/features/platform/PlatformAdminPage"

// 用户管理页（/platform/users）：平台内所有用户一览。门禁由父段布局 platform.tsx 的 PlatformGate 承担。
export const Route = createFileRoute("/_authed/platform/users")({
  component: AllUsersPage,
})
