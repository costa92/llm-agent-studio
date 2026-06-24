import { useQuery, type UseQueryResult } from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { BuiltinNodeType } from "@/lib/types"

// Global built-in node catalog: GET /api/node-types/builtin → {items}. Static.
export function useBuiltinNodeTypes(): UseQueryResult<BuiltinNodeType[]> {
  return useQuery({
    queryKey: ["builtin-node-types"],
    queryFn: () =>
      apiJSON<{ items: BuiltinNodeType[] }>("/api/node-types/builtin").then((d) => d.items),
    staleTime: Infinity,
  })
}
