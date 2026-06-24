import { useQuery, type UseQueryResult } from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { NodeTypeDescription, NodeTypesResponse } from "./nodeDescTypes"

export function useNodeTypes(org: string): UseQueryResult<NodeTypeDescription[]> {
  return useQuery({
    queryKey: ["node-types", org],
    queryFn: () =>
      apiJSON<NodeTypesResponse>(`/api/orgs/${org}/node-types`).then((d) => d.nodeTypes),
    enabled: org !== "",
  })
}
