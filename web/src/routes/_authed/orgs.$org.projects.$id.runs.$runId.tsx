import { useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { getAccessToken } from "@/lib/apiClient"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { WorkbenchView } from "@/features/workflow/WorkbenchPage"
import { AssetThumb } from "@/features/workflow/AssetThumb.tsx"
import { AssetPreviewActions } from "@/features/workflow/AssetPreviewActions"
import { ScriptView } from "@/features/workflow/ScriptView"
import { StoryboardView } from "@/features/workflow/StoryboardView"
import {
  fetchAllEvents,
  useCancel,
  useProject,
  useProjectState,
  useRun,
  useScript,
  useShots,
  usePlans,
} from "@/features/workflow/api"
import { useProductionTimeline } from "@/features/workflow/useProductionTimeline"
import { useUpdateProject, usePromptStyles } from "@/features/projects/api"
import { useOrgTextModels, useOrgImageModels } from "@/features/cost/api"
import { useStorageConfigs } from "@/features/storage/api"
import { EditProjectDialog } from "@/features/projects/EditProjectDialog"
import type { StageId } from "@/lib/timeline"
import type { ProjectState, PipState, GraphNode } from "@/lib/projectState"

export const Route = createFileRoute(
  "/_authed/orgs/$org/projects/$id/runs/$runId"
)({
  component: RunWorkbenchPage,
})

type Selection =
  | { kind: "asset"; assetId: string }
  | { kind: "script"; todoId?: string }
  | { kind: "storyboard"; todoId?: string }
  | null

function RunWorkbenchPage() {
  const { org, id, runId } = Route.useParams()
  const navigate = useNavigate()
  const projectQuery = useProject(id)
  const project = projectQuery.data

  const plansQuery = usePlans(id)
  const plan = plansQuery.data?.find((p) => p.id === runId)

  // M5.1/M9: 规划与图片模型修改
  const updateProject = useUpdateProject(org)
  const textModelsQuery = useOrgTextModels(org)
  const imageModelsQuery = useOrgImageModels(org)
  const stylesQuery = usePromptStyles()
  const storageConfigsQuery = useStorageConfigs(org)

  // 选中态
  const [selection, setSelection] = useState<Selection>(null)
  // 抽屉数据 gated 拉取（DAG 节点携带 todoId 时按节点级工件拉取；默认轨道不带 todoId）
  const scriptQuery = useScript(
    selection?.kind === "script" ? id : "",
    runId,
    selection?.kind === "script" ? selection.todoId : undefined,
  )
  const shotsQuery = useShots(
    selection?.kind === "storyboard" ? id : "",
    runId,
    selection?.kind === "storyboard" ? selection.todoId : undefined,
  )

  // run 返回的 fallbackUsed 常驻 WarnStrip。
  const [fallbackUsed, setFallbackUsed] = useState(false)
  const showFallback = fallbackUsed || (plan?.fallbackUsed ?? false) || (project?.fallbackUsed ?? false)

  const run = useRun(id)
  const cancel = useCancel(id)

  // 权威工作流状态（REST 拉取 + SSE state 帧覆盖缓存）。
  const qc = useQueryClient()
  const stateQuery = useProjectState(id, runId)

  // 回放 → 续接实时（日志累积 + state 帧写回缓存）。
  const { log, conn } = useProductionTimeline({
    projectId: id,
    accessToken: getAccessToken(),
    status: stateQuery.data?.status,
    enabled: project != null,
    fetchAllEvents,
    planId: runId,
    onState: (s) => qc.setQueryData(["project-state", id, runId], s),
  })

  if (projectQuery.isLoading || plansQuery.isLoading) {
    return (
      <div className="p-6">
        <Skeleton className="h-[60px] w-full rounded-xl" />
      </div>
    )
  }
  if (projectQuery.isError || project == null) {
    return (
      <div className="grid h-full place-items-center text-text-2">
        <p>项目加载失败</p>
      </div>
    )
  }

  // 权威工作流状态（加载中回落 draft 草态）。
  const wfState: ProjectState = stateQuery.data ?? {
    projectId: id,
    version: 0,
    status: "draft",
    runStatus: "idle",
    stages: [],
    pips: [],
    assets: { total: 0, done: 0, pending: 0 },
    nodes: [],
    edges: [],
    isCustom: false,
  }

  // 只能对最新的/且是运行态的进行操作
  const isLatestPlan = !!(plansQuery.data && plansQuery.data.length > 0 && plansQuery.data[0].id === runId)
  const canRun = isLatestPlan
  const canCancel = isLatestPlan && (wfState.status === "running" || wfState.status === "planning")

  async function handleRun() {
    try {
      const res = await run.mutateAsync()
      setFallbackUsed(res.fallbackUsed)
      if (res.fallbackUsed) {
        toast.warning("Planner 输出畸形，已回落默认管线")
      } else {
        toast.success("已开始运行")
      }
      void navigate({
        to: "/orgs/$org/projects/$id/runs/$runId",
        params: { org, id, runId: res.planId },
      })
    } catch (err) {
      const status = (err as { status?: number }).status
      if (status === 429) {
        toast.error("配额已用尽，请稍后再试")
        return
      }
      toast.error("运行失败", {
        action: {
          label: "去配置模型",
          onClick: () =>
            void navigate({ to: "/orgs/$org/model-configs", params: { org } }),
        },
      })
    }
  }

  async function handleCancel() {
    try {
      await cancel.mutateAsync()
      toast.success("已取消运行")
      void plansQuery.refetch()
    } catch {
      toast.error("取消失败")
    }
  }

  const latestAssetId = [...wfState.pips]
    .reverse()
    .find((p) => p.status === "done" && p.assetId)?.assetId
  const previewAssetId =
    selection?.kind === "asset" ? selection.assetId : latestAssetId

  function handleSelectStage(stageId: StageId) {
    // 按该阶段对应的 todo 精确取产物：plan 内可能有多个 script/storyboard todo
    // （自定义/重跑），不带 todoId 会拉整个 plan 的镜头混在一起、编号重复。
    if (stageId === "S2") {
      const todoId = wfState.stages.find((s) => s.role === "script")?.todoId
      setSelection({ kind: "script", todoId })
    } else if (stageId === "S3") {
      const todoId = wfState.stages.find((s) => s.role === "storyboard")?.todoId
      setSelection({ kind: "storyboard", todoId })
    }
  }
  function handleSelectPip(pip: PipState) {
    if (pip.assetId) setSelection({ kind: "asset", assetId: pip.assetId })
  }
  function handleSelectNode(node: GraphNode) {
    if (node.type === "script") setSelection({ kind: "script", todoId: node.id })
    else if (node.type === "storyboard") setSelection({ kind: "storyboard", todoId: node.id })
    else if (node.assetId) setSelection({ kind: "asset", assetId: node.assetId })
  }

  const drawerKind =
    selection?.kind === "script" || selection?.kind === "storyboard"
      ? selection.kind
      : null

  const drawer = (
    <Sheet
      open={drawerKind != null}
      onOpenChange={(open) => {
        if (!open) setSelection(null)
      }}
    >
      <SheetContent className="w-full overflow-y-auto sm:max-w-xl">
        <SheetHeader>
          <SheetTitle>{drawerKind === "storyboard" ? "分镜" : "剧本"}</SheetTitle>
        </SheetHeader>
        {drawerKind === "script" && (
          <ScriptView
            script={scriptQuery.data}
            isLoading={scriptQuery.isLoading}
            isError={scriptQuery.isError}
          />
        )}
        {drawerKind === "storyboard" && (
          <StoryboardView
            shots={shotsQuery.data}
            isLoading={shotsQuery.isLoading}
            isError={shotsQuery.isError}
          />
        )}
      </SheetContent>
    </Sheet>
  )

  const isLive = wfState.runStatus !== "done"

  const plannerModelNode = (
    <div className="space-y-1 py-1">
      <div className="flex justify-between text-[12px] text-text-2">
        <span>规划模型</span>
        <span className="font-medium text-text-1">
          {project.plannerProvider && project.plannerModel
            ? `${project.plannerProvider} · ${project.plannerModel}`
            : "组织默认"}
        </span>
      </div>
      <div className="flex justify-between text-[12px] text-text-2">
        <span>图片模型</span>
        <span className="font-medium text-text-1">
          {project.imageProvider && project.imageModel
            ? `${project.imageProvider} · ${project.imageModel}`
            : "组织默认"}
        </span>
      </div>
      <div className="flex justify-end">
        <EditProjectDialog
          trigger={
            <button className="text-[11px] text-text-3 underline underline-offset-2 hover:text-text-1 cursor-pointer">
              编辑项目
            </button>
          }
          project={project}
          textModels={textModelsQuery.data}
          imageModels={imageModelsQuery.data}
          styles={stylesQuery.data}
          storageConfigs={storageConfigsQuery.data}
          onSubmit={(input) =>
            updateProject.mutateAsync({ id: project.id, ...input })
          }
          onSuccess={() => {
            toast.success("项目信息已更新")
            void projectQuery.refetch()
          }}
        />
      </div>
    </div>
  )

  return (
    <WorkbenchView
      project={{ ...project, fallbackUsed: showFallback }}
      plannerModelNode={plannerModelNode}
      state={wfState}
      log={log}
      conn={conn}
      live={isLive}
      fallbackUsed={showFallback || undefined}
      canRun={canRun}
      onRun={handleRun}
      onCancel={canCancel ? handleCancel : () => {}}
      isRunning={run.isPending || cancel.isPending}
      preview={
        previewAssetId ? (
          <div className="flex flex-col gap-3">
            <AssetThumb assetId={previewAssetId} alt="选中素材" className="h-[150px] w-full" />
            <AssetPreviewActions assetId={previewAssetId} className="flex gap-2" />
          </div>
        ) : undefined
      }
      onSelectStage={handleSelectStage}
      onSelectPip={handleSelectPip}
      onSelectNode={handleSelectNode}
      drawer={drawer}
      onOpenReview={() =>
        navigate({
          to: "/orgs/$org/review",
          params: { org },
          search: { project: project.id },
        })
      }
      onBack={() =>
        navigate({ to: "/orgs/$org/projects/$id", params: { org, id } })
      }
    />
  )
}
