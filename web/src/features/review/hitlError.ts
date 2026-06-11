import { ApiError } from "@/lib/apiClient"

// HITL 动作错误 → toast 文案映射。
//   409（asset not pending_acceptance）= 资产已被处理（并发防重，UI-spec §7.6 / 计划 T11 Step 4）。
//   429（generation quota exceeded for org）= 配额超限（仅重生成）。
//   其余 = 通用失败。
export function hitlErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 409) return "该资产已被处理（不是待审核状态）"
    if (err.status === 429) return "生成配额已用尽，请稍后再试"
  }
  return "操作失败，请重试"
}
