import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { ApiError, apiFetch, apiJSON } from "@/lib/apiClient"
import type {
  ItemsEnvelope,
  Workflow,
  WorkflowTemplateMeta,
} from "@/lib/types"

// 工作流案例模板：列出内置案例模板，并从模板一键实例化一条新工作流。
// 镜像 features/projects/workflowApi.ts 的 TanStack Query 范式（apiJSON / queryKey / 失效）。

// GET /api/orgs/{org}/workflow-templates → {items: WorkflowTemplateMeta[]}（viewer+）。
// queryKey ["workflow-templates", org]。
export function useWorkflowTemplates(
  org: string,
): UseQueryResult<WorkflowTemplateMeta[]> {
  return useQuery({
    queryKey: ["workflow-templates", org],
    queryFn: () =>
      apiJSON<ItemsEnvelope<WorkflowTemplateMeta>>(
        `/api/orgs/${org}/workflow-templates`,
      ).then((env) => env.items),
    enabled: org !== "",
  })
}

// POST /api/projects/{id}/workflows/from-template body {templateId} → Workflow（editor+）。
// 后端幂等建好所需 llm 自定义节点类型、组装节点、创建工作流，返回新工作流。
// 成功后失效 ["workflows", projectId]（新工作流进入列表）。
export function useInstantiateTemplate(
  projectId: string,
): UseMutationResult<Workflow, Error, { templateId: string }> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ templateId }: { templateId: string }) =>
      apiJSON<Workflow>(
        `/api/projects/${projectId}/workflows/from-template`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ templateId }),
        },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["workflows", projectId],
      })
    },
  })
}

// POST /api/orgs/{org}/workflow-templates body {name,description,projectId,workflowId} → 201
//（editor+）。服务端从该 workflow 快照建 org 级模板。成功后失效 ["workflow-templates", org]。
export function useSaveTemplate(
  org: string,
): UseMutationResult<
  WorkflowTemplateMeta,
  Error,
  { name: string; description: string; projectId: string; workflowId: string }
> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (body: {
      name: string
      description: string
      projectId: string
      workflowId: string
    }) =>
      apiJSON<WorkflowTemplateMeta>(`/api/orgs/${org}/workflow-templates`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["workflow-templates", org],
      })
    },
  })
}

// DELETE /api/orgs/{org}/workflow-templates/{id} → 204（editor+）。响应无 body，
// 故走底层 apiFetch 手动校验状态（apiJSON 会对空 body 解析失败）。成功后失效模板列表。
export function useDeleteTemplate(
  org: string,
): UseMutationResult<void, Error, string> {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: async (templateId: string) => {
      const res = await apiFetch(
        `/api/orgs/${org}/workflow-templates/${templateId}`,
        { method: "DELETE" },
      )
      if (!res.ok) {
        throw new ApiError(res.status, await res.text())
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: ["workflow-templates", org],
      })
    },
  })
}
