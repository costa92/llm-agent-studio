import { useQuery } from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"

// 当前用户所属组织。GET /api/orgs → {items:[{id,name,role}]}。
// 共享 queryKey ["my-orgs"]（与 OrgLanding 同源，react-query 自动去重缓存），
// 供 AppShell 把 org id 解析成可读组织名。
export interface OrgItem {
  id: string
  name: string
  role: string
}

export function useMyOrgs() {
  return useQuery({
    queryKey: ["my-orgs"],
    queryFn: () => apiJSON<{ items: OrgItem[] }>("/api/orgs").then((e) => e.items),
  })
}
