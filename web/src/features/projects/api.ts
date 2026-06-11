import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type {
  CreateProjectInput,
  ListEnvelope,
  Project,
  Style,
} from "@/lib/types"

// GET /api/orgs/{org}/projects → {items, next_cursor}（viewer+）。
// T9 只取首页 items；keyset 游标累积留资产库（T12 useInfiniteQuery）。
export function useProjects(org: string): UseQueryResult<Project[]> {
  return useQuery({
    queryKey: ["projects", org],
    queryFn: () =>
      apiJSON<ListEnvelope<Project>>(`/api/orgs/${org}/projects`).then(
        (env) => env.items,
      ),
    enabled: org !== "",
  })
}

// POST /api/orgs/{org}/projects body {name,brief,contentType,targetPlatform,style} → Project（editor+）。
// 成功后失效项目列表 Query。
export function useCreateProject(
  org: string,
): UseMutationResult<Project, Error, CreateProjectInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: CreateProjectInput) =>
      apiJSON<Project>(`/api/orgs/${org}/projects`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["projects", org] })
    },
  })
}

// GET /api/prompt-styles → {styles: Style[]}（auth-only）。建项目/重生成共用风格下拉。
export function usePromptStyles(): UseQueryResult<Style[]> {
  return useQuery({
    queryKey: ["prompt-styles"],
    queryFn: () =>
      apiJSON<{ styles: Style[] }>(`/api/prompt-styles`).then(
        (res) => res.styles,
      ),
    staleTime: 60 * 60 * 1000,
  })
}
