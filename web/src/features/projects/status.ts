import type { ProjectStatus } from "@/lib/types"
import type { StudioBadgeProps } from "@/components/studio/Badge"

type BadgeVariant = NonNullable<StudioBadgeProps["variant"]>

// Project status（7 种，UI-spec §7.2）→ Badge variant（4 种：running/done/pending/rejected）。
// 进行中(planning/running)→running；完成(completed)→done；待处理(draft/review)→pending；终止(failed/canceled)→rejected。
const VARIANT_BY_STATUS: Record<ProjectStatus, BadgeVariant> = {
  draft: "pending",
  planning: "running",
  running: "running",
  review: "pending",
  completed: "done",
  failed: "rejected",
  canceled: "rejected",
}

const LABEL_BY_STATUS: Record<ProjectStatus, string> = {
  draft: "草稿",
  planning: "规划中",
  running: "生产中",
  review: "待审核",
  completed: "已完成",
  failed: "失败",
  canceled: "已取消",
}

export function statusVariant(status: ProjectStatus): BadgeVariant {
  return VARIANT_BY_STATUS[status] ?? "pending"
}

export function statusLabel(status: ProjectStatus): string {
  return LABEL_BY_STATUS[status] ?? status
}
