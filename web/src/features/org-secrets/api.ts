import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { OrgSecret } from "@/lib/types"

// org 密钥写入入参：{name, value}。value 仅写入、加密存储、永不回显。
// 编辑时 value 留空 = 后端保留既有值（仅改引用元数据，不重写密文）。
export interface UpsertOrgSecretInput {
  name: string
  value: string
}

// 组织级密钥列表：GET /api/orgs/{org}/secrets → {items: OrgSecret[]}（roleAdmin）。
// DTO 永不含 value，仅 {id, orgId, name, hasValue}。
export function useOrgSecrets(org: string): UseQueryResult<OrgSecret[]> {
  return useQuery({
    queryKey: ["org-secrets", org],
    queryFn: () =>
      apiJSON<{ items: OrgSecret[] }>(`/api/orgs/${org}/secrets`).then((d) => d.items),
    enabled: org !== "",
  })
}

// 创建密钥：POST /api/orgs/{org}/secrets body={name, value} → 200 OrgSecret。
// 未配 STUDIO_CONFIG_ENC_KEY → 400；名称重复 → 400。
export function useCreateOrgSecret(
  org: string,
): UseMutationResult<OrgSecret, Error, UpsertOrgSecretInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: UpsertOrgSecretInput) =>
      apiJSON<OrgSecret>(`/api/orgs/${org}/secrets`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["org-secrets", org] })
    },
  })
}

// 更新密钥：PUT /api/orgs/{org}/secrets/{name} body={name, value} → 200 OrgSecret。
// value 空串 = 保留既有密文；非空 = 重新加密替换。404 if name 不存在。
export function useUpdateOrgSecret(
  org: string,
): UseMutationResult<OrgSecret, Error, { name: string; input: UpsertOrgSecretInput }> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ name, input }: { name: string; input: UpsertOrgSecretInput }) =>
      apiJSON<OrgSecret>(`/api/orgs/${org}/secrets/${encodeURIComponent(name)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["org-secrets", org] })
    },
  })
}

// 删除密钥：DELETE /api/orgs/{org}/secrets/{name} → 200。404 if name 不存在。
export function useDeleteOrgSecret(
  org: string,
): UseMutationResult<unknown, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (name: string) =>
      apiJSON(`/api/orgs/${org}/secrets/${encodeURIComponent(name)}`, {
        method: "DELETE",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["org-secrets", org] })
    },
  })
}
