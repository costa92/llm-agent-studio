import { useQuery } from "@tanstack/react-query"
import { apiFetch } from "@/lib/apiClient"

// 角色门禁策略：乐观显示 + 后端强制。
// access token 是 authz JWT，角色是 per-(org,scope) 的（后端 ResolveRole 决定），
// 前端不解析 JWT 取角色。改用对 admin-only 探针的成功/403 推断当前用户在该 org 是否 admin。
// 探针 = GET /api/orgs/{org}/model-configs（createModelConfigHandler/listModelConfigsHandler = admin）。
// 200 → admin；403 → 非 admin。结果缓存进 Query。
// 这是 UX（隐藏审核/成本/模型配置入口），不是安全边界——后端仍对每个写操作强制 RBAC。

// 探针返回的角色判定结果（admin / 非 admin）。
async function probeIsAdmin(org: string): Promise<boolean> {
  const res = await apiFetch(`/api/orgs/${org}/model-configs`)
  if (res.status === 403) return false
  // 200 = admin。其余非 2xx（如网络/500）按非 admin 保守处理（不放开入口）。
  return res.ok
}

export interface Role {
  isAdmin: boolean
  isLoading: boolean
}

export function useRole(org: string): Role {
  const query = useQuery({
    queryKey: ["role", org],
    queryFn: () => probeIsAdmin(org),
    enabled: org !== "",
    staleTime: 5 * 60 * 1000,
    retry: false,
  })

  return {
    isAdmin: query.data ?? false,
    // org 为空时不发探针（enabled:false），视为已就绪的非 admin。
    isLoading: org !== "" && query.isLoading,
  }
}
