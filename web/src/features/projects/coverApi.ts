import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { ApiError, apiFetch, apiJSON } from "@/lib/apiClient"
import type { Asset, ItemsEnvelope } from "@/lib/types"

// 项目封面图。封面本质是一个 image 资产，project.coverAssetId 指向它。
// 本文件镜像 features/projects/api.ts 与 workflowApi.ts 的 TanStack Query 范式。
// generate/upload/set 成功后统一失效 ["projects", org]（项目卡片的封面随之刷新）。

// POST /api/projects/{id}/cover/generate body {prompt?,provider?,model?} → {coverAssetId}（editor+）。
// prompt 留空 = 后端按项目名/风格兜底。成功后失效项目列表。
export function useGenerateCover(
  org: string,
): UseMutationResult<
  { coverAssetId: string },
  Error,
  { projectId: string; prompt?: string; provider?: string; model?: string }
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ projectId, prompt, provider, model }) =>
      apiJSON<{ coverAssetId: string }>(
        `/api/projects/${projectId}/cover/generate`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ prompt, provider, model }),
        },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["projects", org] })
    },
  })
}

// POST /api/projects/{id}/cover/upload multipart/form-data field=file → {coverAssetId}（editor+）。
// 关键：FormData 体绝不能手动设 Content-Type，否则浏览器无法附上 multipart boundary。
// 故用底层 apiFetch（原样透传 init，不强制 JSON 头）并手动解析 JSON。
export function useUploadCover(
  org: string,
): UseMutationResult<
  { coverAssetId: string },
  Error,
  { projectId: string; file: File }
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async ({ projectId, file }) => {
      const form = new FormData()
      form.append("file", file)
      const res = await apiFetch(`/api/projects/${projectId}/cover/upload`, {
        method: "POST",
        body: form,
      })
      if (!res.ok) {
        throw new ApiError(res.status, await res.text())
      }
      return (await res.json()) as { coverAssetId: string }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["projects", org] })
    },
  })
}

// PUT /api/projects/{id}/cover body {assetId} → 200（editor+）。assetId="" 清除封面。
// 成功后失效项目列表。
export function useSetCover(
  org: string,
): UseMutationResult<void, Error, { projectId: string; assetId: string }> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async ({ projectId, assetId }) => {
      const res = await apiFetch(`/api/projects/${projectId}/cover`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ assetId }),
      })
      if (!res.ok) {
        throw new ApiError(res.status, await res.text())
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["projects", org] })
    },
  })
}

// GET /api/projects/{id}/cover/options → {items: Asset[]}（viewer+）。项目可选封面的图片资产。
// enabled 由调用方控制（如对话框打开时才拉）。
export function useCoverOptions(
  projectId: string,
  enabled: boolean,
): UseQueryResult<Asset[]> {
  return useQuery({
    queryKey: ["cover-options", projectId],
    queryFn: () =>
      apiJSON<ItemsEnvelope<Asset>>(
        `/api/projects/${projectId}/cover/options`,
      ).then((env) => env.items),
    enabled,
  })
}
