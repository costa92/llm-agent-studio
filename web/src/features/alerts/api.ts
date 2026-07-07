import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { AlertSettings } from "@/lib/types"

// 告警配置写入入参。开启任一类告警时后端要求 email 形似邮箱（400），且开启的运营告警
// 需正阈值；全部关闭时允许留空/保留旧值。
export interface UpdateAlertSettingsInput {
  email: string
  enabled: boolean
  budgetEnabled: boolean
  budgetThresholdMicros: number
  budgetWindowHours: number
  stuckEnabled: boolean
  stuckThresholdMinutes: number
  backlogEnabled: boolean
  backlogThreshold: number
}

// org 级 run 失败告警配置：GET /api/orgs/{org}/alert-settings → AlertSettings（roleAdmin）。
// 未配置的 org 返回零值默认（enabled=false, email=""），不是 404。
export function useAlertSettings(org: string): UseQueryResult<AlertSettings> {
  return useQuery({
    queryKey: ["alert-settings", org],
    queryFn: () => apiJSON<AlertSettings>(`/api/orgs/${org}/alert-settings`),
    enabled: org !== "",
  })
}

// 保存告警配置：PUT /api/orgs/{org}/alert-settings body={email, enabled} → 200 AlertSettings。
// 成功失效 ["alert-settings", org] 触发重新拉取。
export function useUpdateAlertSettings(
  org: string,
): UseMutationResult<AlertSettings, Error, UpdateAlertSettingsInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: UpdateAlertSettingsInput) =>
      apiJSON<AlertSettings>(`/api/orgs/${org}/alert-settings`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["alert-settings", org] })
    },
  })
}
