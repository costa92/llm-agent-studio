import { useQuery, type UseQueryResult } from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { TaskBoardResponse } from "@/lib/types"

// GET /api/orgs/{org}/tasks → {items: TaskRow[], counts}（任意 org 成员 / viewer 可读）。
// 跨项目运行看板，整包返回（含桶计数），tab 过滤在前端做。
export function useTaskBoard(org: string): UseQueryResult<TaskBoardResponse> {
  return useQuery({
    queryKey: ["task-board", org],
    queryFn: () => apiJSON<TaskBoardResponse>(`/api/orgs/${org}/tasks`),
    enabled: org !== "",
  })
}
