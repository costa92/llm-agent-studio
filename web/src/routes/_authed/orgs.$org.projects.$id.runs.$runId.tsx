import { useState } from "react"
import { createFileRoute, useNavigate, Navigate } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { getAccessToken } from "@/lib/apiClient"
import { useSmartBack } from "@/lib/useSmartBack"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { WorkbenchView } from "@/features/workflow/WorkbenchPage"
import { SelectedAssetPanel } from "@/features/workflow/SelectedAssetPanel"
import { ScriptView } from "@/features/workflow/ScriptView"
import { StoryboardView } from "@/features/workflow/StoryboardView"
import { AssetGalleryModal } from "@/features/workflow/AssetGalleryModal"
import { Button } from "@/components/studio/Button"
import {
  fetchAllEvents,
  useProject,
  useProjectState,
  useScript,
  useShots,
  usePlans,
} from "@/features/workflow/api"
import { useAsset } from "@/features/review/api"
import { useRole } from "@/app/rbac"
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
  component: RunRouteGate,
})

// Phase 3：/runs/$runId 整合入口。自定义工作流 run（plan.workflowId 非空）重定向到
// 画布运行模式 /workflow?wf=&run=；遗留默认管线 run（workflowId 空）回落渲染旧
// RunWorkbenchPage。加载/错误态由 RunWorkbenchPage 自身处理（这里仅在 plans 就绪后判定）。
function RunRouteGate() {
  const { org, id, runId } = Route.useParams()
  const plansQuery = usePlans(id)
  const plan = plansQuery.data?.find((p) => p.id === runId)

  // plans 就绪且该 run 属于某自定义工作流 → 定向到画布运行模式（replace 不留历史）。
  if (plansQuery.isSuccess && plan?.workflowId) {
    return (
      <Navigate
        to="/orgs/$org/projects/$id/workflow"
        params={{ org, id }}
        search={{ wf: plan.workflowId, run: runId }}
        replace
      />
    )
  }
  // 加载中 / 加载失败 / 默认管线 run（无 workflowId）：渲染旧工作台（自带 loading/error 态）。
  return <RunWorkbenchPage />
}

type Selection =
  | { kind: "asset"; assetId: string }
  | { kind: "script"; todoId?: string }
  | { kind: "storyboard"; todoId?: string }
  | null

function RunWorkbenchPage() {
  const { org, id, runId } = Route.useParams()
  const navigate = useNavigate()
  // 顶栏返回：后退到打开工作台之前的那一页（运行列表 / 项目页），无历史时兜底回项目页。
  const goBack = useSmartBack(() => {
    void navigate({ to: "/orgs/$org/projects/$id", params: { org, id } })
  })
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

  const { isAdmin } = useRole(org)

  // 选中态
  const [selection, setSelection] = useState<Selection>(null)
  // 素材画廊抽屉开合。
  const [galleryOpen, setGalleryOpen] = useState(false)

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
  const showFallback = (plan?.fallbackUsed ?? false) || (project?.fallbackUsed ?? false)

  // 权威工作流状态（REST 拉取 + SSE state 帧覆盖缓存）。
  const qc = useQueryClient()
  const stateQuery = useProjectState(id, runId)

  // 选中资产详情（hooks 规则：early return 之前无条件调用）。
  // stateQuery 在 early return 前已拉取，用它的 pips 计算 latestAssetId 以得到完整的 previewAssetId；
  // useAsset 内部 enabled: id !== "" 防护空串不发请求。
  const latestAssetIdEarly = [...(stateQuery.data?.pips ?? [])]
    .reverse()
    .find((p) => p.status === "done" && p.assetId)?.assetId
  const previewAssetIdEarly =
    selection?.kind === "asset" ? selection.assetId : latestAssetIdEarly
  const previewDetailQuery = useAsset(previewAssetIdEarly ?? "")

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
  if (plansQuery.isError) {
    return (
      <div className="grid h-full place-items-center">
        <div className="flex flex-col items-center gap-3 text-text-2">
          <p>运行记录加载失败</p>
          <Button variant="ghost" onClick={() => void plansQuery.refetch()}>
            重试
          </Button>
        </div>
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

  const latestAssetId = [...wfState.pips]
    .reverse()
    .find((p) => p.status === "done" && p.assetId)?.assetId
  // 已生成素材（done 且有 assetId），按生成顺序——供「查看全部素材」画廊。
  const doneAssetIds = wfState.pips
    .filter((p) => p.status === "done" && p.assetId)
    .map((p) => p.assetId as string)
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
          <SheetDescription>
            {drawerKind === "storyboard" ? "该节点生成的分镜镜头详情" : "该节点生成的剧本内容详情"}
          </SheetDescription>
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

  const galleryTrigger =
    doneAssetIds.length > 0 ? (
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" onClick={() => setGalleryOpen(true)}>
          查看全部素材 ({doneAssetIds.length})
        </Button>
      </div>
    ) : undefined

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
      canRun={false}
      canCancel={false}
      onRun={() => {}}
      onCancel={() => {}}
      isRunning={false}
      preview={
        previewAssetId ? (
          <SelectedAssetPanel
            org={org}
            assetId={previewAssetId}
            isAdmin={isAdmin}
            detail={previewDetailQuery.data}
          />
        ) : undefined
      }
      onSelectStage={handleSelectStage}
      onSelectPip={handleSelectPip}
      onSelectNode={handleSelectNode}
      galleryTrigger={galleryTrigger}
      drawer={
        <>
          {drawer}
          <AssetGalleryModal
            assetIds={doneAssetIds}
            open={galleryOpen}
            onOpenChange={setGalleryOpen}
          />
        </>
      }
      onOpenReview={() =>
        navigate({
          to: "/orgs/$org/review",
          params: { org },
          search: { project: project.id },
        })
      }
      onBack={goBack}
    />
  )
}
