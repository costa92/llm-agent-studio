import { createFileRoute } from "@tanstack/react-router"
import { PlatformSettingsPage } from "@/features/platform/PlatformAdminPage"

// 平台设置页（/platform）：全局默认存储 + 平台管理员。门禁由父段布局 platform.tsx 的 PlatformGate 承担。
export const Route = createFileRoute("/_authed/platform/")({
  component: PlatformSettingsPage,
})
