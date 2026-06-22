import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { z } from "zod"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { useWorkflows } from "@/features/projects/workflowApi"
import { WorkflowCanvas } from "@/features/workflow-canvas/WorkflowCanvas"

// Phase 1：只读工作流画布路由。?wf= 选中要编辑的工作流（按 id 匹配项目下工作流）。
const workflowSearchSchema = z.object({
  wf: z.string().optional(),
})

export const Route = createFileRoute("/_authed/orgs/$org/projects/$id/workflow")({
  validateSearch: workflowSearchSchema,
  component: WorkflowCanvasPage,
})

function WorkflowCanvasPage() {
  const { org, id } = Route.useParams()
  const { wf } = Route.useSearch()
  const navigate = useNavigate()

  const workflowsQuery = useWorkflows(id)
  // workflowApi 已把信封 items 解析为 Workflow[]（nodes 为已解析的数组）。
  const workflow = workflowsQuery.data?.find((w) => w.id === wf)

  const goBack = () =>
    void navigate({
      to: "/orgs/$org/projects/$id",
      params: { org, id },
    })

  if (workflowsQuery.isLoading) {
    return (
      <div className="p-6 space-y-4">
        <Skeleton className="h-[40px] w-[200px]" />
        <Skeleton className="h-[400px] w-full rounded-xl" />
      </div>
    )
  }

  if (workflowsQuery.isError) {
    return (
      <div className="grid h-full place-items-center">
        <div className="flex flex-col items-center gap-3 text-center">
          <p className="text-sm text-text-2">加载失败</p>
          <Button variant="ghost" onClick={() => void workflowsQuery.refetch()}>
            重试
          </Button>
        </div>
      </div>
    )
  }

  if (!wf || !workflow) {
    return (
      <div className="grid h-full place-items-center">
        <div className="flex flex-col items-center gap-3 text-center">
          <p className="text-sm text-text-2">
            {wf ? "未找到该工作流" : "未指定工作流"}
          </p>
          <Button variant="ghost" onClick={goBack}>
            返回项目
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="h-full">
      <WorkflowCanvas
        workflowName={workflow.name}
        nodes={workflow.nodes}
        onBack={goBack}
      />
    </div>
  )
}
