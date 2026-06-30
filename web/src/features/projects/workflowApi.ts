import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type {
  CreateWorkflowInput,
  ItemsEnvelope,
  RunWorkflowResponse,
  Workflow,
} from "@/lib/types"

// 一个项目可有多条工作流（DAG）。本文件镜像 features/projects/api.ts 与 cost/api.ts
// 的 TanStack Query 范式。queryKey 统一用 ["workflows", projectId]。

// GET /api/projects/{id}/workflows → {items: Workflow[]}（viewer+）。
export function useWorkflows(projectId: string): UseQueryResult<Workflow[]> {
  return useQuery({
    queryKey: ["workflows", projectId],
    queryFn: () =>
      apiJSON<ItemsEnvelope<Workflow>>(
        `/api/projects/${projectId}/workflows`,
      ).then((env) => env.items),
    enabled: projectId !== "",
  })
}

// POST /api/projects/{id}/workflows body {name, nodes} → Workflow（editor+）。
// 成功后失效 ["workflows", projectId]。
export function useCreateWorkflow(
  projectId: string,
): UseMutationResult<Workflow, Error, CreateWorkflowInput> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: CreateWorkflowInput) =>
      apiJSON<Workflow>(`/api/projects/${projectId}/workflows`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["workflows", projectId],
      })
    },
  })
}

// PUT /api/projects/{id}/workflows/{wfId} body {name, nodes} → Workflow（editor+）。
// 成功后失效 ["workflows", projectId]。
export function useUpdateWorkflow(
  projectId: string,
): UseMutationResult<
  Workflow,
  Error,
  { wfId: string; input: CreateWorkflowInput }
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ wfId, input }: { wfId: string; input: CreateWorkflowInput }) =>
      apiJSON<Workflow>(`/api/projects/${projectId}/workflows/${wfId}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["workflows", projectId],
      })
    },
  })
}

// DELETE /api/projects/{id}/workflows/{wfId} → {ok:true}（editor+）。
// 成功后失效 ["workflows", projectId]。
export function useDeleteWorkflow(
  projectId: string,
): UseMutationResult<{ ok: boolean }, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (wfId: string) =>
      apiJSON<{ ok: boolean }>(
        `/api/projects/${projectId}/workflows/${wfId}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["workflows", projectId],
      })
    },
  })
}

// POST /api/projects/{id}/workflows/{wfId}/run → 202 {planId,valid,fallbackUsed,workflowId}
//（editor+）。可带运行期输入 {inputs}（wf.inputsSchema 非空时）；inputs 缺省 → 不带 body
//（与历史行为一致，零回归）。成功后失效工作流列表（更新 latestRunStatus/latestPlanId）+ 项目 + 运行历史。
export function useRunWorkflow(
  projectId: string,
): UseMutationResult<
  RunWorkflowResponse,
  Error,
  { wfId: string; inputs?: Record<string, unknown> }
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({
      wfId,
      inputs,
    }: {
      wfId: string
      inputs?: Record<string, unknown>
    }) =>
      apiJSON<RunWorkflowResponse>(
        `/api/projects/${projectId}/workflows/${wfId}/run`,
        inputs
          ? {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ inputs }),
            }
          : { method: "POST" },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["workflows", projectId],
      })
      void queryClient.invalidateQueries({ queryKey: ["project", projectId] })
      void queryClient.invalidateQueries({ queryKey: ["plans", projectId] })
    },
  })
}
