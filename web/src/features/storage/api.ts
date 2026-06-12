import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { StorageConfig, UpsertStorageConfigInput } from "@/lib/types"

// 「可缺失」GET 的统一响应形状：{config: StorageConfig | null}（null = 未配置，非 404）。
interface StorageConfigEnvelope {
  config: StorageConfig | null
}

// org 存储配置：GET /api/orgs/{org}/storage-config → {config}（admin，getOrgStorageConfigHandler）。
// 未配置 → config:null（回退全局默认）；secret 永不回显，仅报 hasSecret 布尔。
export function useOrgStorageConfig(
  org: string,
): UseQueryResult<StorageConfig | null> {
  return useQuery({
    queryKey: ["storage-config", "org", org],
    queryFn: () =>
      apiJSON<StorageConfigEnvelope>(`/api/orgs/${org}/storage-config`).then(
        (env) => env.config,
      ),
    enabled: org !== "",
  })
}

// 更新 org 存储配置：PUT /api/orgs/{org}/storage-config body=write shape
//   → 200 StorageConfig（admin，putOrgStorageConfigHandler）。
// secret 空 → 后端保留既有；非空 → 重新加密替换。要存 secret 但未配 STUDIO_CONFIG_ENC_KEY → 400。
export function useUpsertOrgStorageConfig(
  org: string,
): UseMutationResult<StorageConfig, Error, UpsertStorageConfigInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: UpsertStorageConfigInput) =>
      apiJSON<StorageConfig>(`/api/orgs/${org}/storage-config`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["storage-config", "org", org],
      })
    },
  })
}

// 删除 org 存储配置：DELETE /api/orgs/{org}/storage-config → 200 {ok:true}
//（admin，deleteOrgStorageConfigHandler）。删除后该 org 回退到全局默认；无配置 → 404。
export function useDeleteOrgStorageConfig(
  org: string,
): UseMutationResult<{ ok: boolean }, Error, void> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () =>
      apiJSON<{ ok: boolean }>(`/api/orgs/${org}/storage-config`, {
        method: "DELETE",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["storage-config", "org", org],
      })
    },
  })
}

// 全局默认存储配置：GET /api/storage-config/global → {config}
//（any-org-admin，getGlobalStorageConfigHandler）。所有未单独配置的 org 共用此配置。
export function useGlobalStorageConfig(): UseQueryResult<StorageConfig | null> {
  return useQuery({
    queryKey: ["storage-config", "global"],
    queryFn: () =>
      apiJSON<StorageConfigEnvelope>(`/api/storage-config/global`).then(
        (env) => env.config,
      ),
  })
}

// 更新全局默认存储：PUT /api/storage-config/global body=write shape
//   → 200 StorageConfig（any-org-admin，putGlobalStorageConfigHandler）。修改影响全局。
export function useUpsertGlobalStorageConfig(): UseMutationResult<
  StorageConfig,
  Error,
  UpsertStorageConfigInput
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: UpsertStorageConfigInput) =>
      apiJSON<StorageConfig>(`/api/storage-config/global`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["storage-config", "global"],
      })
    },
  })
}
