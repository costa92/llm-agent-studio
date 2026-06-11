import {
  useMutation,
  type UseMutationResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { BuildPromptResponse } from "@/lib/types"

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
