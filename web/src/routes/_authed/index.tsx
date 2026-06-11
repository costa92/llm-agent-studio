import { createFileRoute } from "@tanstack/react-router"
import { OrgLanding } from "@/app/OrgLanding"

// 根落地（已认证）：无 org-list 端点可派生默认 org（org slug 来自 URL，所有视图按 /orgs/$org 寻址）。
// 故提供可操作落地：输入已有 org 进入，或新建 org（POST /api/orgs → {id}，id 即路由 slug）。
// 二者皆导航到 /orgs/{org}/projects，避免用户停滞在静态占位上。
export const Route = createFileRoute("/_authed/")({
  component: OrgLanding,
})
