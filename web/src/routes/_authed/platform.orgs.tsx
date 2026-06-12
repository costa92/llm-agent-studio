import { createFileRoute } from "@tanstack/react-router"
import { AllOrgsPage } from "@/features/platform/PlatformAdminPage"

// 全部组织页（/platform/orgs）：平台内所有组织一览。门禁由父段布局 platform.tsx 的 PlatformGate 承担。
export const Route = createFileRoute("/_authed/platform/orgs")({
  component: AllOrgsPage,
})
