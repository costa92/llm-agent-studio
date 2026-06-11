import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type {
  Asset,
  AssetDetail,
  ListEnvelope,
  RegenerateResponse,
} from "@/lib/types"

// 审核队列：GET /api/orgs/{org}/assets?status=pending_acceptance&type=image → {items, next_cursor}
// （viewer+，libraryHandler，keyset 分页）。status 过滤项已核实：internal/httpapi/m2handlers.go:175
// 透传 q.Get("status") 给 assets.LibraryFilter.Status，store.go:244 拼 a.status=$N。
// 审核看板只看待审图片，故固定 status=pending_acceptance + type=image。
export function useReviewQueue(org: string): UseQueryResult<Asset[]> {
  return useQuery({
    queryKey: ["review-queue", org],
    queryFn: () =>
      apiJSON<ListEnvelope<Asset>>(
        `/api/orgs/${org}/assets?status=pending_acceptance&type=image`,
      ).then((env) => env.items),
    enabled: org !== "",
  })
}

// 资产详情 + 版本血缘：GET /api/assets/{id} → {asset, versions}（viewer+）；不存在 → 404。
export function useAsset(id: string): UseQueryResult<AssetDetail> {
  return useQuery({
    queryKey: ["asset", id],
    queryFn: () => apiJSON<AssetDetail>(`/api/assets/${id}`),
    enabled: id !== "",
  })
}

// 采纳/失效后刷新审核队列 + 资产库（UI-spec §7.6：HITL 采纳即失效 review + library）。
function invalidateAfterHitl(
  queryClient: ReturnType<typeof useQueryClient>,
  org: string,
): void {
  void queryClient.invalidateQueries({ queryKey: ["review-queue", org] })
  void queryClient.invalidateQueries({ queryKey: ["library", org] })
}

// 采纳：POST /api/assets/{id}/accept → 200 {id, status:"accepted"}（admin）；
// 非 pending_acceptance → 409。
export function useAccept(
  org: string,
): UseMutationResult<{ id: string; status: string }, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiJSON<{ id: string; status: string }>(`/api/assets/${id}/accept`, {
        method: "POST",
      }),
    onSuccess: () => invalidateAfterHitl(queryClient, org),
  })
}

// 退回：POST /api/assets/{id}/reject → 200 {id, status:"rejected"}（admin）；非 pending → 409。
export function useReject(
  org: string,
): UseMutationResult<{ id: string; status: string }, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiJSON<{ id: string; status: string }>(`/api/assets/${id}/reject`, {
        method: "POST",
      }),
    onSuccess: () => invalidateAfterHitl(queryClient, org),
  })
}

// 改 Prompt 重生成：POST /api/assets/{id}/regenerate body {prompt}
//   → 200 {newAssetId, todoId, status:"generating"}（admin）。
// 非 pending → 409；配额超限 → 429。
// 注：后端 regenerateHandler 仅解码 {prompt}（m2handlers.go:136-138），不读 params——故只发 {prompt}。
export interface RegenerateArgs {
  id: string
  prompt: string
}

export function useRegenerate(
  org: string,
): UseMutationResult<RegenerateResponse, Error, RegenerateArgs> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, prompt }: RegenerateArgs) =>
      apiJSON<RegenerateResponse>(`/api/assets/${id}/regenerate`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ prompt }),
      }),
    onSuccess: () => invalidateAfterHitl(queryClient, org),
  })
}
