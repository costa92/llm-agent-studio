import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { CustomNodeType, UpsertCustomNodeTypeInput } from "@/lib/types"

// 组织级自定义节点类型列表：GET /api/orgs/{org}/custom-node-types → {items: CustomNodeType[]}。
// typed 节点（有 typeId）通过此列表在画布中解析 kind+params。
export function useCustomNodeTypes(org: string): UseQueryResult<CustomNodeType[]> {
  return useQuery({
    queryKey: ["custom-node-types", org],
    queryFn: () =>
      apiJSON<{ items: CustomNodeType[] }>(`/api/orgs/${org}/custom-node-types`).then(
        (d) => d.items,
      ),
    enabled: org !== "",
  })
}

// 创建类型：POST /api/orgs/{org}/custom-node-types body=write shape → 200 CustomNodeType。
// slug 由后端从 label 派生；kind 当前只支持 "llm"。
export function useCreateCustomNodeType(
  org: string,
): UseMutationResult<CustomNodeType, Error, UpsertCustomNodeTypeInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: UpsertCustomNodeTypeInput) =>
      apiJSON<CustomNodeType>(`/api/orgs/${org}/custom-node-types`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["custom-node-types", org] })
    },
  })
}

// 更新类型：PUT /api/orgs/{org}/custom-node-types/{id} body=write shape → 200 CustomNodeType。
// 只改 label/color/params；slug/kind 不可变。
export function useUpdateCustomNodeType(
  org: string,
): UseMutationResult<CustomNodeType, Error, { id: string; input: UpsertCustomNodeTypeInput }> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpsertCustomNodeTypeInput }) =>
      apiJSON<CustomNodeType>(`/api/orgs/${org}/custom-node-types/${id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["custom-node-types", org] })
    },
  })
}

// 删除类型：DELETE /api/orgs/{org}/custom-node-types/{id} → 200
//（409 if in-use by workflow nodes）。被引用的类型不可删除，前端应提示用户先移除节点引用。
export function useDeleteCustomNodeType(
  org: string,
): UseMutationResult<unknown, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiJSON(`/api/orgs/${org}/custom-node-types/${id}`, {
        method: "DELETE",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["custom-node-types", org] })
    },
  })
}
