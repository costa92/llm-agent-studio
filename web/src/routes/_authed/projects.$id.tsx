import { useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import { getAccessToken } from "@/lib/apiClient"
import { Skeleton } from "@/components/ui/skeleton"
import { WorkbenchView } from "@/features/workflow/WorkbenchPage"
import { AssetThumb } from "@/features/workflow/AssetThumb.tsx"
import {
  fetchAllEvents,
  useCancel,
  useProject,
  useRun,
} from "@/features/workflow/api"
import { useProductionTimeline } from "@/features/workflow/useProductionTimeline"

// T10：项目工作台 + 制片轨道 + SSE 实时。
export const Route = createFileRoute("/_authed/projects/$id")({
  component: WorkbenchPage,
})

function WorkbenchPage() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const projectQuery = useProject(id)
  const project = projectQuery.data

  // run 返回的 fallbackUsed 常驻 WarnStrip。
  const [fallbackUsed, setFallbackUsed] = useState(false)
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
      toast.error(status === 429 ? "配额已用尽，请稍后再试" : "运行失败")
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

  // 右栏预览：取最近一个已生成 asset（pip.assetId 来自 asset_generated payload，
  // 是契约可靠的取图入口）→ AssetThumb 走 /content 302→签名 URL（Step 4 决策）。
  const latestAssetId = [...state.pips]
    .reverse()
    .find((p) => p.status === "done" && p.assetId)?.assetId

  return (
    <WorkbenchView
      project={project}
      timeline={state}
      conn={conn}
      live={!isTerminal(project.status)}
      fallbackUsed={fallbackUsed || undefined}
      canRun={canRun}
      onRun={handleRun}
      onCancel={handleCancel}
      isRunning={run.isPending || cancel.isPending}
      preview={
        latestAssetId ? (
          <AssetThumb assetId={latestAssetId} alt="最新生成素材" className="h-[150px] w-full" />
        ) : undefined
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
