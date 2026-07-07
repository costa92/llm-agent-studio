import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type InfiniteData,
  type UseInfiniteQueryResult,
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

// 每页拉取条数（libraryHandler limit；后端 store.go 默认 50、上限 100，与成本台账同口径）。
const REVIEW_PAGE_LIMIT = 50

// 审核队列：GET /api/orgs/{org}/assets?status=pending_acceptance[&project=…]&limit=&cursor=
// → {items, next_cursor}（viewer+，libraryHandler，keyset 分页）。status/project 过滤项已核实：
// internal/httpapi/m2handlers.go:197-200 透传 q.Get("status")/q.Get("project") 给
// assets.LibraryFilter，store.go:238-245 拼 a.status=$N / a.project_id=$N。
// Phase 3 T4：移除硬编码 type=image，让 video/audio 待审资产也进队列；可选 project 筛选
// 由「去审核」CTA 携来的 ?project= 驱动（审核台默认仍是 org 级收件箱）。
// P1 修复：改 useInfiniteQuery 按 next_cursor 累积（原来单页 limit=50 静默截断，>50 的
// 待审积压永远够不到）。getNextPageParam 空串 → 停（同资产库 useLibrary / 成本 useGenerations）。
export function useReviewQueue(
  org: string,
  project?: string,
): UseInfiniteQueryResult<InfiniteData<ListEnvelope<Asset>>, Error> {
  return useInfiniteQuery({
    queryKey: ["review-queue", org, project ?? null],
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({
        status: "pending_acceptance",
        limit: String(REVIEW_PAGE_LIMIT),
      })
      if (project) params.set("project", project)
      if (pageParam) params.set("cursor", pageParam)
      return apiJSON<ListEnvelope<Asset>>(
        `/api/orgs/${org}/assets?${params.toString()}`,
      )
    },
    initialPageParam: "" as string,
    getNextPageParam: (last) => (last.next_cursor ? last.next_cursor : undefined),
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
// 另失效 project-state（前缀，仅刷新当前挂载的工作台查询）：accept/reject/regenerate
// 不发 run_event，SSE 版本门不会推 state 帧，故工作台「待审核 · N」徽标
//（取自 project-state 的 assets.pending）需主动失效才会刷新，否则停在旧值。
// review 态下 SSE 不在推帧，REST 重取无版本竞态，安全。
function invalidateAfterHitl(
  queryClient: ReturnType<typeof useQueryClient>,
  org: string,
): void {
  void queryClient.invalidateQueries({ queryKey: ["review-queue", org] })
  void queryClient.invalidateQueries({ queryKey: ["library", org] })
  void queryClient.invalidateQueries({ queryKey: ["project-state"] })
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
