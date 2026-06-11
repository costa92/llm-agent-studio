import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type {
  Aggregate,
  CatalogEntry,
  CreateModelConfigInput,
  ItemsEnvelope,
  LedgerEntry,
  ModelConfig,
  ProjectAggregate,
} from "@/lib/types"
import type { TimeRange } from "./format"

// 时间范围 → query string（from/to RFC3339，畸形后端 → 400；空则不带，全量）。
function rangeQuery(range: TimeRange): string {
  const sp = new URLSearchParams()
  if (range.from) sp.set("from", range.from)
  if (range.to) sp.set("to", range.to)
  const qs = sp.toString()
  return qs ? `?${qs}` : ""
}

// org 成本聚合：GET /api/orgs/{org}/cost?from=&to= → Aggregate（admin，orgCostHandler）。
export function useOrgCost(
  org: string,
  range: TimeRange,
): UseQueryResult<Aggregate> {
  return useQuery({
    queryKey: ["org-cost", org, range],
    queryFn: () => apiJSON<Aggregate>(`/api/orgs/${org}/cost${rangeQuery(range)}`),
    enabled: org !== "",
  })
}

// 按项目成本汇总：GET /api/orgs/{org}/cost/projects?from=&to= → {items: ProjectAggregate[]}
//（admin，orgCostProjectsHandler，最贵在前）。
export function useOrgCostProjects(
  org: string,
  range: TimeRange,
): UseQueryResult<ProjectAggregate[]> {
  return useQuery({
    queryKey: ["org-cost-projects", org, range],
    queryFn: () =>
      apiJSON<ItemsEnvelope<ProjectAggregate>>(
        `/api/orgs/${org}/cost/projects${rangeQuery(range)}`,
      ).then((env) => env.items),
    enabled: org !== "",
  })
}

// 生成明细台账：GET /api/orgs/{org}/generations?limit= → {items: LedgerEntry[]}
//（admin，orgGenerationsHandler，最近在前；注意此端点不读 from/to，仅 limit）。
export function useGenerations(
  org: string,
  limit = 50,
): UseQueryResult<LedgerEntry[]> {
  return useQuery({
    queryKey: ["generations", org, limit],
    queryFn: () =>
      apiJSON<ItemsEnvelope<LedgerEntry>>(
        `/api/orgs/${org}/generations?limit=${limit}`,
      ).then((env) => env.items),
    enabled: org !== "",
  })
}

// 模型目录：GET /api/model-catalog → {catalog: CatalogEntry[]}（auth-only，modelCatalogHandler）。
export function useModelCatalog(): UseQueryResult<CatalogEntry[]> {
  return useQuery({
    queryKey: ["model-catalog"],
    queryFn: () =>
      apiJSON<{ catalog: CatalogEntry[] }>(`/api/model-catalog`).then(
        (res) => res.catalog,
      ),
    staleTime: 60 * 60 * 1000,
  })
}

// org 模型配置列表：GET /api/orgs/{org}/model-configs → {items: ModelConfig[]}
//（admin，listModelConfigsHandler；无 API key 字段——密钥服务端管理）。
export function useModelConfigs(org: string): UseQueryResult<ModelConfig[]> {
  return useQuery({
    queryKey: ["model-configs", org],
    queryFn: () =>
      apiJSON<ItemsEnvelope<ModelConfig>>(
        `/api/orgs/${org}/model-configs`,
      ).then((env) => env.items),
    enabled: org !== "",
  })
}

// 创建模型配置：POST /api/orgs/{org}/model-configs body {kind,provider,model,enabled,isDefault,params}
//   → 200 ModelConfig（admin，createModelConfigHandler）。
// provider/model 缺失 → 400；含密钥型 param → 400 ErrSecretParam（见 configError.ts）。
export function useCreateModelConfig(
  org: string,
): UseMutationResult<ModelConfig, Error, CreateModelConfigInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: CreateModelConfigInput) =>
      apiJSON<ModelConfig>(`/api/orgs/${org}/model-configs`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["model-configs", org] })
    },
  })
}
