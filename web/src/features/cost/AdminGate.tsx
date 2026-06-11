import type { ReactNode } from "react"
import type { Role } from "@/app/rbac"

// admin-only 视图门禁（成本中心 / 模型配置）。
// 角色由 rbac.useRole 探针推断（200=admin / 403=非 admin）。这是 UX，不是安全边界——
// 后端对每个端点仍强制 RBAC（非 admin 即便直访也只会拿到 403）。
// loading 时显占位（避免门禁文案闪烁）；非 admin 时显"需要管理员权限"而非渲染内容。
export interface AdminGateProps {
  role: Role
  children: ReactNode
}

export function AdminGate({ role, children }: AdminGateProps) {
  if (role.isLoading) {
    return (
      <div className="grid h-full place-items-center text-text-3">
        <p>权限校验中…</p>
      </div>
    )
  }
  if (!role.isAdmin) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
        <p className="text-text-1">需要管理员权限</p>
        <p className="text-[12.5px] text-text-3">
          仅组织管理员可访问该页面，请联系管理员。
        </p>
      </div>
    )
  }
  return <>{children}</>
}
