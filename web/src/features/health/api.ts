import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { HealthFailure, HealthReport, RepairResult } from "@/lib/types"

// 平台监控 / 数据健康的前端 API 钩子（/api/platform/health/*）。均经平台门禁（非平台管理员 → 403）。

// GET /api/platform/health（平台门禁）→ HealthReport：系统快照 + 一致性检查列表。
// refetchInterval 15s 给「实时监控」的观感（系统状态会随时间变化）。
export function useHealthReport(): UseQueryResult<HealthReport> {
  return useQuery({
    queryKey: ["platform", "health"],
    queryFn: () => apiJSON<HealthReport>(`/api/platform/health`),
    refetchInterval: 15000,
  })
}

// GET /api/platform/health/events?limit=（平台门禁）→ {items: HealthFailure[]}。最近运营失败 / 错误事件。
export function useHealthEvents(limit = 50): UseQueryResult<HealthFailure[]> {
  return useQuery({
    queryKey: ["platform", "health", "events", limit],
    queryFn: () =>
      apiJSON<{ items: HealthFailure[] }>(
        `/api/platform/health/events?limit=${limit}`,
      ).then((e) => e.items),
  })
}

// POST /api/platform/health/repair body {checkId} → RepairResult（平台门禁）。
// 成功失效 ["platform","health"] 触发报告刷新（count 随之归零 / 减少）。
export function useRepairCheck(): UseMutationResult<RepairResult, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiJSON<RepairResult>(`/api/platform/health/repair`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ checkId: id }),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["platform", "health"] })
    },
  })
}
