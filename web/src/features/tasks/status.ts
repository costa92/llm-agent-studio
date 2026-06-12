import type { TaskRow } from "@/lib/types"
import { statusLabel, statusVariant } from "@/features/projects/status"

// 复用项目状态的 Badge variant/label（7 状态 → 4 variant），任务行徽章共用同一套。
export { statusLabel, statusVariant }

// tab 分桶（UI mock §任务中心）。planning+running→运行中；review→待审核；
// completed→完成；failed→失败；draft→草稿；canceled→已取消。
export type TaskBucket =
  | "运行中"
  | "待审核"
  | "完成"
  | "失败"
  | "草稿"
  | "已取消"

export function taskBucket(status: string): TaskBucket {
  switch (status) {
    case "planning":
    case "running":
      return "运行中"
    case "review":
      return "待审核"
    case "completed":
      return "完成"
    case "failed":
      return "失败"
    case "canceled":
      return "已取消"
    case "draft":
    default:
      return "草稿"
  }
}

// 行内快捷动作的路由目标（容器用 useNavigate 消费）。
// review → 审核页并按项目收窄（?project=，与 orgs.$org.review.tsx 读取的参数名一致）。
// 其余（running/planning/completed/draft/canceled/failed）→ 项目工作台（route param 名为 id）。
export interface QuickAction {
  label: string
  to: string
  params: Record<string, string>
  search?: Record<string, string>
}

export function quickAction(row: TaskRow, org: string): QuickAction {
  if (row.status === "review") {
    return {
      label: "去审核",
      to: "/orgs/$org/review",
      params: { org },
      search: { project: row.projectId },
    }
  }
  return {
    label: "查看",
    to: "/orgs/$org/projects/$id",
    params: { org, id: row.projectId },
  }
}
