import { useQuery, type UseQueryResult } from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { NodeTypeDescription, NodeTypesResponse } from "./nodeDescTypes"

function nodeTypesQuery(org: string) {
  return {
    queryKey: ["node-types", org],
    queryFn: () => apiJSON<NodeTypesResponse>(`/api/orgs/${org}/node-types`),
    enabled: org !== "",
  }
}

export function useNodeTypes(org: string): UseQueryResult<NodeTypeDescription[]> {
  return useQuery({ ...nodeTypesQuery(org), select: (d) => d.nodeTypes })
}

// P5：ExprChannel 能力旗标（来自同一 /node-types 响应，共享 react-query 缓存）。
// 字段级 varBindings 仅在其为 true 时可创作；默认/缺省 = false（fail-closed UX gate）。
export function useNodeTypesExprChannel(org: string): boolean {
  const { data } = useQuery({
    ...nodeTypesQuery(org),
    select: (d) => d.exprChannel ?? false,
  })
  return data ?? false
}
