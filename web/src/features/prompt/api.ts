import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { BuildPromptResponse, Prompt, CreatePromptInput } from "@/lib/types"

// 风格下拉与建项目/重生成共用 —— 复用 T9 已建的 usePromptStyles（GET /api/prompt-styles）。
export { usePromptStyles } from "@/features/projects/api"

// 预览拼装：POST /api/prompt/build body {prompt,style} → {prompt}（auth-only）。
//   后端 promptBuildHandler（m2handlers.go:74）仅解码 {prompt,style}，prompt 必填否则 400；
//   返回 {prompt: b.Build(prompt, style)}（空/未知 style → 原 prompt 不变）。
export interface BuildPromptArgs {
  prompt: string
  style: string
}

export function useBuildPrompt(): UseMutationResult<
  BuildPromptResponse,
  Error,
  BuildPromptArgs
> {
  return useMutation({
    mutationFn: ({ prompt, style }: BuildPromptArgs) =>
      apiJSON<BuildPromptResponse>(`/api/prompt/build`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ prompt, style }),
      }),
  })
}

export function usePrompts(org: string): UseQueryResult<Prompt[]> {
  return useQuery({
    queryKey: ["prompts", org],
    queryFn: () =>
      apiJSON<{ items: Prompt[] }>(`/api/orgs/${org}/prompts`).then(
        (res) => res.items,
      ),
    enabled: org !== "",
  })
}

export function useCreatePrompt(
  org: string,
): UseMutationResult<Prompt, Error, CreatePromptInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: CreatePromptInput) =>
      apiJSON<Prompt>(`/api/orgs/${org}/prompts`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["prompts", org] })
    },
  })
}

export function useUpdatePrompt(
  org: string,
): UseMutationResult<Prompt, Error, { id: string; input: CreatePromptInput }> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: string; input: CreatePromptInput }) =>
      apiJSON<Prompt>(`/api/orgs/${org}/prompts/${id}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["prompts", org] })
    },
  })
}

export function useDeletePrompt(
  org: string,
): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiJSON<void>(`/api/orgs/${org}/prompts/${id}`, {
        method: "DELETE",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["prompts", org] })
    },
  })
}
