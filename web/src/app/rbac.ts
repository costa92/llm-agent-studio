import { useQuery } from "@tanstack/react-query"
import { apiFetch } from "@/lib/apiClient"

// 角色门禁策略：乐观显示 + 后端强制。
// access token 是 authz JWT，角色是 per-(org,scope) 的（后端 ResolveRole 决定），
// 前端不解析 JWT 取角色。改用后端只读端点 GET /api/orgs/{org}/members/me 拿调用者
// 在该 org 的有效角色字符串（viewer / editor / admin / org_admin），据此区分：
//   - isAdmin：审核 / 成本 / 模型配置等 admin-only 入口（后端 roleAdmin 强制）。
//   - canWrite：创建 / 编辑项目·工作流·封面·自定义节点类型等 editor+ 写入口（后端 roleEditor 强制）。
// 这是 UX（隐藏 viewer 点了必 403 的写 CTA，消除「点了没反应」死胡同），不是安全边界——
// 后端仍对每个写操作强制 RBAC。失败 / 网络错误按最保守（viewer 语义）处理，不放开任何写入口。

// probeRole 拉取调用者在该 org 的角色字符串；非成员/失败 → ""（最小权限）。
async function probeRole(org: string): Promise<string> {
  const res = await apiFetch(`/api/orgs/${org}/members/me`)
  if (!res.ok) return ""
  const body = (await res.json()) as { role?: string }
  return body.role ?? ""
}

export interface Role {
  role: string
  isAdmin: boolean
  canWrite: boolean
  isLoading: boolean
}

export function useRole(org: string): Role {
  const query = useQuery({
    queryKey: ["role", org],
    queryFn: () => probeRole(org),
    enabled: org !== "",
    staleTime: 5 * 60 * 1000,
    retry: false,
  })

  const role = query.data ?? ""
  const isAdmin = role === "admin" || role === "org_admin"
  return {
    role,
    isAdmin,
    // editor+ 可写；viewer 只读。
    canWrite: isAdmin || role === "editor",
    // org 为空时不发探针（enabled:false），视为已就绪的 viewer（无写权）。
    isLoading: org !== "" && query.isLoading,
  }
}
