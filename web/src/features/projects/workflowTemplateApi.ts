import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseMutationResult,
  type UseQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
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
