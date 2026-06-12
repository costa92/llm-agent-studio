import { createFileRoute, Outlet } from "@tanstack/react-router"
import { PlatformGate } from "@/features/platform/PlatformAdminPage"

// 平台管理段布局（平台超级管理员专属）：顶层 /_authed/platform，非 org-scoped。
// 门禁集中于此（PlatformGate：loading 骨架 / 非管理员空态），由设置（index）与全部组织（orgs）
// 两个子页共用；后端 /api/platform/* 仍强制 403。本布局仅在门禁通过后透传 <Outlet/>。
export const Route = createFileRoute("/_authed/platform")({
  component: () => (
    <PlatformGate>
      <Outlet />
    </PlatformGate>
  ),
})
