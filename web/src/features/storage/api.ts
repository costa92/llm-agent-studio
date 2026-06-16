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

// org 存储配置列表：GET /api/orgs/{org}/storage-configs → {items: StorageConfig[]}。
// 返回该 org 下所有存储配置；isDefault=true 的为当前默认；secret 永不回显，仅报 hasSecret 布尔。
export function useStorageConfigs(org: string): UseQueryResult<StorageConfig[]> {
  return useQuery({
    queryKey: ["storage-configs", org],
    queryFn: () =>
      apiJSON<{ items: StorageConfig[] }>(`/api/orgs/${org}/storage-configs`).then(
        (d) => d.items,
      ),
    enabled: org !== "",
  })
}

// 创建 org 存储配置：POST /api/orgs/${org}/storage-configs body=write shape → 200 StorageConfig。
// secret 空 → 后端保留既有；非空 → 重新加密替换。要存 secret 但未配 STUDIO_CONFIG_ENC_KEY → 400。
export function useCreateStorageConfig(
  org: string,
): UseMutationResult<StorageConfig, Error, UpsertStorageConfigInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: UpsertStorageConfigInput) =>
      apiJSON<StorageConfig>(`/api/orgs/${org}/storage-configs`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["storage-configs", org] })
    },
  })
}

// 更新 org 存储配置：PUT /api/orgs/{org}/storage-configs/{id} body=write shape → 200 StorageConfig。
export function useUpdateStorageConfig(
  org: string,
): UseMutationResult<StorageConfig, Error, { id: string; input: UpsertStorageConfigInput }> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: UpsertStorageConfigInput }) =>
      apiJSON<StorageConfig>(`/api/orgs/${org}/storage-configs/${id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["storage-configs", org] })
    },
  })
}

// 删除 org 存储配置：DELETE /api/orgs/{org}/storage-configs/{id} → 200
//（409 if in-use by a project）。删除后相关项目回退到 org 默认配置；无配置 → 404。
export function useDeleteStorageConfig(
  org: string,
): UseMutationResult<unknown, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiJSON(`/api/orgs/${org}/storage-configs/${id}`, {
        method: "DELETE",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["storage-configs", org] })
    },
  })
}

// 设置默认存储配置：POST /api/orgs/{org}/storage-configs/{id}/default → 200。
// 同 org 下将其他配置的 isDefault 置 false，该配置 isDefault 置 true。
export function useSetDefaultStorageConfig(
  org: string,
): UseMutationResult<unknown, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiJSON(`/api/orgs/${org}/storage-configs/${id}/default`, {
        method: "POST",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["storage-configs", org] })
    },
  })
}

// 全局默认存储配置：GET /api/platform/storage-config/global → {config}
//（平台管理员，platformAdmin → getGlobalStorageConfigHandler）。所有未单独配置的 org 共用此配置。
export function useGlobalStorageConfig(): UseQueryResult<StorageConfig | null> {
  return useQuery({
    queryKey: ["storage-config", "global"],
    queryFn: () =>
      apiJSON<StorageConfigEnvelope>(`/api/platform/storage-config/global`).then(
        (env) => env.config,
      ),
  })
}

// 更新全局默认存储：PUT /api/platform/storage-config/global body=write shape
//   → 200 StorageConfig（平台管理员，platformAdmin → putGlobalStorageConfigHandler）。修改影响全局。
export function useUpsertGlobalStorageConfig(): UseMutationResult<
  StorageConfig,
  Error,
  UpsertStorageConfigInput
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: UpsertStorageConfigInput) =>
      apiJSON<StorageConfig>(`/api/platform/storage-config/global`, {
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
