import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router"
import { z } from "zod"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { useWorkflows } from "@/features/projects/workflowApi"
import { useBasicPrompts, usePrompts } from "@/features/prompt/api"
import { WorkflowCanvas } from "@/features/workflow-canvas/WorkflowCanvas"
import { useSmartBack } from "@/lib/useSmartBack"

// Phase 2：可编辑工作流画布路由。?wf= 选中要编辑的工作流（按 id 匹配项目下工作流）。
// canvas run mode 模式派生（URL 唯一真源）：
//   ?run=X        → 运行模式，看该次 run；
//   ?mode=edit    → 显式编辑模式（「编辑」切换设此，避免被下面的默认态拉回运行）；
//   都没有        → 有 latestPlan 默认进运行模式（看最近一次 run），否则编辑（新建/未跑过）。
const workflowSearchSchema = z.object({
  wf: z.string().optional(),
  run: z.string().optional(),
  // mode=create 是显式「新建工作流」入口信号（区别于带 wf 的编辑/运行）。
  mode: z.enum(["edit", "run", "create"]).optional(),
})

export const Route = createFileRoute("/_authed/orgs/$org/projects/$id/workflow")({
  validateSearch: workflowSearchSchema,
  // 无 wf 且非显式 create → 重定向到项目页（工作流列表 + 新建入口）。
  // 防止误打开的 ?mode=edit 深链静默开一个空白新工作流并被保存成 stray 记录。
  beforeLoad: ({ search, params }) => {
    if (!search.wf && search.mode !== "create") {
      throw redirect({ to: "/orgs/$org/projects/$id", params })
    }
  },
  component: WorkflowCanvasPage,
})

function WorkflowCanvasPage() {
  const { org, id } = Route.useParams()
  const { wf, run, mode: modeParam } = Route.useSearch()
  const navigate = useNavigate()

  const workflowsQuery = useWorkflows(id)
  // workflowApi 已把信封 items 解析为 Workflow[]（nodes 为已解析的数组）。
  const workflow = workflowsQuery.data?.find((w) => w.id === wf)
  // 属性面板提示词选择所需：org 自建提示词 + 内置基础提示词（与旧编辑器同源）。
  const { data: prompts } = usePrompts(org)
  const { data: basics } = useBasicPrompts()

  // 模式派生：显式 ?run= 优先；?mode=edit 强制编辑；?mode=run 即使无 run/latestPlan 也进运行
  //（未跑过的工作流也能查看运行视图的空态）；否则有 latestPlan 默认运行。
  const explicitEdit = modeParam === "edit"
  const explicitRun = modeParam === "run"
  const runId = run ?? (explicitEdit ? undefined : workflow?.latestPlanId)
  const mode = runId || explicitRun ? "run" : "edit"

  // 顶栏「← 返回」：真正后退到打开画布之前的那一页（项目页 / 运行列表 / 上一个
  // 视图），无历史时兜底回项目页。视图内的编辑↔运行切换、选择 run、创建后跳转
  // 都用 replace 不留历史，故后退不会被这些内部切换困住。
  const goBack = useSmartBack(() => {
    void navigate({
      to: "/orgs/$org/projects/$id",
      params: { org, id },
    })
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
        inputsSchema={workflow?.inputsSchema}
        settings={workflow?.settings}
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
            // 创建落地是同一视图的状态迁移，不留历史——否则「返回」会退回空的新建画布。
            replace: true,
          })
        }
        onModeChange={(next) =>
          void navigate({
            to: "/orgs/$org/projects/$id/workflow",
            params: { org, id },
            // 切运行：带最近一次 plan + 显式 ?mode=run（未跑过的工作流 latestPlanId 为空，
            // 仅靠 run 无法进运行态，须显式 mode=run）；切编辑：显式 ?mode=edit（否则会被默认运行态拉回）。
            search:
              next === "run"
                ? { wf, run: workflow?.latestPlanId, mode: "run" }
                : { wf, mode: "edit" },
            // 编辑↔运行是同一工作流内的视图切换，不写历史——「返回」应离开工作流
            // 而非在编辑/运行间一格格倒退（切回另一态用「编辑|运行」开关）。
            replace: true,
          })
        }
        onSelectRun={(rid) =>
          void navigate({
            to: "/orgs/$org/projects/$id/workflow",
            params: { org, id },
            search: { wf, run: rid },
            // 切换查看的 run 是视图内状态，不留历史。
            replace: true,
          })
        }
      />
    </div>
  )
}
