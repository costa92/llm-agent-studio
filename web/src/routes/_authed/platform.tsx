import { createFileRoute } from "@tanstack/react-router"
import { PlatformAdminPage } from "@/features/platform/PlatformAdminPage"

// 平台管理页（平台超级管理员专属）：顶层 /_authed/platform，非 org-scoped。
// 组件级以 usePlatformWhoami 做门禁（loading 骨架 / 非管理员空态）；后端 /api/platform/* 仍强制 403。
export const Route = createFileRoute("/_authed/platform")({
  component: PlatformAdminPage,
})
