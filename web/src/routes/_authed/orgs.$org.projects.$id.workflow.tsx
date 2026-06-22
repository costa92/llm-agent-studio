import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { z } from "zod"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { useWorkflows } from "@/features/projects/workflowApi"
import { useBasicPrompts, usePrompts } from "@/features/prompt/api"
import { WorkflowCanvas } from "@/features/workflow-canvas/WorkflowCanvas"

// Phase 2：可编辑工作流画布路由。?wf= 选中要编辑的工作流（按 id 匹配项目下工作流）。
// canvas run mode：?run= 在时进入运行模式（叠加该 run 的执行状态，只读）；无 ?run= = 编辑模式。
const workflowSearchSchema = z.object({
  wf: z.string().optional(),
  run: z.string().optional(),
})

export const Route = createFileRoute("/_authed/orgs/$org/projects/$id/workflow")({
  validateSearch: workflowSearchSchema,
  component: WorkflowCanvasPage,
})

function WorkflowCanvasPage() {
  const { org, id } = Route.useParams()
  const { wf, run } = Route.useSearch()
  const navigate = useNavigate()

  const workflowsQuery = useWorkflows(id)
  // workflowApi 已把信封 items 解析为 Workflow[]（nodes 为已解析的数组）。
  const workflow = workflowsQuery.data?.find((w) => w.id === wf)
  // 属性面板提示词选择所需：org 自建提示词 + 内置基础提示词（与旧编辑器同源）。
  const { data: prompts } = usePrompts(org)
  const { data: basics } = useBasicPrompts()

  // 模式由 ?run= 派生（URL 唯一真源）。运行模式 runId = ?run ?? 工作流最近一次 plan。
  const mode = run ? "run" : "edit"
  const runId = mode === "run" ? (run ?? workflow?.latestPlanId) : undefined

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

  // ?wf 指定但找不到 → not-found 错误态。?wf 缺省 → 新建模式（空画布）。
  if (wf && !workflow) {
    return (
      <div className="grid h-full place-items-center">
        <div className="flex flex-col items-center gap-3 text-center">
          <p className="text-sm text-text-2">未找到该工作流</p>
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
        workflowId={workflow?.id}
        projectId={id}
        org={org}
        workflowName={workflow?.name ?? ""}
        nodes={workflow?.nodes ?? []}
        prompts={prompts}
        basics={basics}
        mode={mode}
        runId={runId}
        onBack={goBack}
        onCreated={(newId) =>
          void navigate({
            to: "/orgs/$org/projects/$id/workflow",
            params: { org, id },
            search: { wf: newId },
          })
        }
        onModeChange={(next) =>
          void navigate({
            to: "/orgs/$org/projects/$id/workflow",
            params: { org, id },
            // 切运行：默认带工作流最近一次 plan；切编辑：丢弃 ?run。
            search:
              next === "run"
                ? { wf, run: workflow?.latestPlanId }
                : { wf },
          })
        }
        onSelectRun={(rid) =>
          void navigate({
            to: "/orgs/$org/projects/$id/workflow",
            params: { org, id },
            search: { wf, run: rid },
          })
        }
      />
    </div>
  )
}
