import { useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
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
import { ScriptView } from "@/features/workflow/ScriptView"
import { StoryboardView } from "@/features/workflow/StoryboardView"
import {
  fetchAllEvents,
  useCancel,
  useProject,
  useRun,
  useScript,
  useShots,
} from "@/features/workflow/api"
import { useProductionTimeline } from "@/features/workflow/useProductionTimeline"
import type { Pip, StageId } from "@/lib/timeline"

// T10：项目工作台 + 制片轨道 + SSE 实时。
export const Route = createFileRoute("/_authed/orgs/$org/projects/$id")({
  component: WorkbenchPage,
})

// T3：右栏/抽屉选中态——pip 选中切右栏预览；stage 选中开抽屉检视剧本/分镜。
type Selection =
  | { kind: "asset"; assetId: string }
  | { kind: "script" }
  | { kind: "storyboard" }
  | null

function WorkbenchPage() {
  const { org, id } = Route.useParams()
  const navigate = useNavigate()
  const projectQuery = useProject(id)
  const project = projectQuery.data

  // T3：选中态（null = 默认；asset = pip 选中预览；script/storyboard = 抽屉）。
  const [selection, setSelection] = useState<Selection>(null)
  // 抽屉数据 gated 拉取——仅在对应抽屉打开时才请求（enabled 门控）。
  const scriptQuery = useScript(selection?.kind === "script" ? id : "")
  const shotsQuery = useShots(selection?.kind === "storyboard" ? id : "")

  // run 返回的 fallbackUsed 常驻 WarnStrip。
  const [fallbackUsed, setFallbackUsed] = useState(false)
  const showFallback = fallbackUsed || (project?.fallbackUsed ?? false)
  const run = useRun(id)
  const cancel = useCancel(id)

  // 回放 → 续接实时（seq-dedup 吸收重叠）。完成态只回放不开流。
  const { state, conn } = useProductionTimeline({
    projectId: id,
    accessToken: getAccessToken(),
    status: project?.status,
    enabled: project != null,
    fetchAllEvents,
  })

  if (projectQuery.isLoading) {
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

  // 运行控制（运行/取消/重新运行）= editor+。rbac 仅有 admin 探针，无 editor 只读探针
  // （后端无 editor-gated GET）——按 rbac 文档"乐观显示 + 后端强制"乐观显示，editor+ 由
  // 后端 runHandler/cancelHandler 强制（viewer 点击 → 403 → toast）。
  const canRun = true

  async function handleRun() {
    try {
      const res = await run.mutateAsync()
      setFallbackUsed(res.fallbackUsed)
      if (res.fallbackUsed) {
        toast.warning("Planner 输出畸形，已回落默认管线")
      } else {
        toast.success("已开始运行")
      }
    } catch (err) {
      const status = (err as { status?: number }).status
      if (status === 429) {
        // 配额超限——明确归因，不挂模型动作。
        toast.error("配额已用尽，请稍后再试")
        return
      }
      // 后端 POST /run 不区分"未配模型"（ChatModelFor 总回落 planner 默认模型，
      // 真正的模型缺失/失效在 worker 异步执行时以 todo_failed 浮现，见 internal/worker/worker.go:559）。
      // 故运行失败原因无法在此干净区分——给一个不臆测原因的次级动作引导去配置模型。
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
    } catch {
      toast.error("取消失败")
    }
  }

  // 右栏预览：pip 选中 → 该工件；否则默认取最近一个已生成 asset（保留既有行为，
  // 用户显式点 pip 前不显空提示）。pip.assetId 来自 asset_generated payload，
  // 是契约可靠的取图入口 → AssetThumb 走 /content 302→签名 URL（Step 4 决策）。
  const latestAssetId = [...state.pips]
    .reverse()
    .find((p) => p.status === "done" && p.assetId)?.assetId
  // 选中 pip 优先；否则回落最近 asset。
  const previewAssetId =
    selection?.kind === "asset" ? selection.assetId : latestAssetId

  // T3：可检视阶段点击 → 设选中态打开抽屉（S2 剧本 / S3 分镜）。
  function handleSelectStage(stageId: StageId) {
    if (stageId === "S2") setSelection({ kind: "script" })
    else if (stageId === "S3") setSelection({ kind: "storyboard" })
  }
  // T3：done pip 点击 → 右栏预览切到该工件。
  function handleSelectPip(pip: Pip) {
    if (pip.assetId) setSelection({ kind: "asset", assetId: pip.assetId })
  }

  // 抽屉开合：script/storyboard 选中 → 打开；关闭则清回 null。
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

  return (
    <WorkbenchView
      project={project}
      timeline={state}
      conn={conn}
      live={!isTerminal(project.status)}
      fallbackUsed={showFallback || undefined}
      canRun={canRun}
      onRun={handleRun}
      onCancel={handleCancel}
      isRunning={run.isPending || cancel.isPending}
      preview={
        previewAssetId ? (
          <AssetThumb assetId={previewAssetId} alt="选中素材" className="h-[150px] w-full" />
        ) : undefined
      }
      onSelectStage={handleSelectStage}
      onSelectPip={handleSelectPip}
      drawer={drawer}
      // T2：审核台仍是 org 级收件箱，CTA 携 ?project= 做 SPA 跳转（Phase 3 接消费）。
      onOpenReview={() =>
        navigate({
          to: "/orgs/$org/review",
          params: { org },
          search: { project: project.id },
        })
      }
      onBack={() =>
        navigate({ to: "/orgs/$org/projects", params: { org: project.orgId } })
      }
    />
  )
}

function isTerminal(status: string): boolean {
  return ["completed", "review", "failed", "canceled"].includes(status)
}
