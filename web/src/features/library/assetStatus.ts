import type { StudioBadgeProps } from "@/components/studio/Badge"

type BadgeVariant = NonNullable<StudioBadgeProps["variant"]>

// Asset status（worker.go/review.go：generating/submitted/pending_acceptance/accepted/rejected）
// → Badge variant（4 种）。accepted→done；rejected→rejected；其余在途→running/pending。
const VARIANT_BY_STATUS: Record<string, BadgeVariant> = {
  generating: "running",
  submitted: "running",
  pending_acceptance: "pending",
  accepted: "done",
  rejected: "rejected",
}

const LABEL_BY_STATUS: Record<string, string> = {
  generating: "生成中",
  submitted: "已提交",
  pending_acceptance: "待审核",
  accepted: "已采纳",
  rejected: "已退回",
}

export function assetStatusVariant(status: string): BadgeVariant {
  return VARIANT_BY_STATUS[status] ?? "pending"
}

export function assetStatusLabel(status: string): string {
  return LABEL_BY_STATUS[status] ?? status
}
