import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  useReactFlow,
  type Node,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { toast } from "sonner"
import "./canvasTheme.css"
import type { InputField, ProjectStatus, WorkflowNode as WorkflowNodeType } from "@/lib/types"
import type { ProjectState, GraphNodeStatus } from "@/lib/projectState"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import { SseIndicator, type SseStatus } from "@/components/studio/SseIndicator"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog"
import { EventLog } from "@/components/studio/EventLog"
import { ApiError, getAccessToken } from "@/lib/apiClient"
import { statusLabel, statusVariant } from "@/features/projects/status"
import {
  fetchAllEvents,
  useCancel,
  useProjectAssets,
  useProjectState,
  usePlans,
  useScript,
  useShots,
  type Plan,
} from "@/features/workflow/api"
import { useRunWorkflow } from "@/features/projects/workflowApi"
import { RunInputsDialog } from "@/features/workflow/RunInputsDialog"
import { useAsset } from "@/features/review/api"
import { ReviewBoard } from "@/features/review/ReviewBoard"
import { useRole } from "@/app/rbac"
import { ScriptView } from "@/features/workflow/ScriptView"
import { StoryboardView } from "@/features/workflow/StoryboardView"
import { SelectedAssetPanel } from "@/features/workflow/SelectedAssetPanel"
import { AssetGalleryModal } from "@/features/workflow/AssetGalleryModal"
import { RunSummary } from "@/features/workflow/RunSummary"
import {
  useProductionTimeline,
  type SseConnState,
} from "@/features/workflow/useProductionTimeline"
import { useQueryClient } from "@tanstack/react-query"
import { toReactFlow, type RFEdge, type StudioNodeData } from "./canvasModel"
import { overlayRunStatus } from "./runOverlay"
import {
  buildRunGroups,
  type AssetMeta,
  type ParentAnchor,
  type RunPage,
} from "./runFanout"
import { resolveSelection, type RunSelection } from "./resolveSelection"
import { ItemInspector } from "./ItemInspector"
import { WorkflowNode } from "./WorkflowNode"
import { GroupNode, type GroupRunNodeData } from "./GroupNode"
import { RunMatrix } from "./RunMatrix"
import { RunCostSummary } from "./RunCostSummary"
import { RunPreview } from "./RunPreview"
import { RunEdge } from "./RunEdge"
import { markActiveEdges } from "./runEdges"
import { minimapStatusColor } from "./statusColor"
import { autoLayout } from "@/lib/autoLayout"
import { useNodeTiming } from "@/features/workflow/useNodeTiming"
import { useTopologySettings } from "./useTopologySettings"
import { TopologySettingsPanel } from "./TopologySettingsPanel"
// 边活动判定改用已合的 runEdges.markActiveEdges（与 #127 的 computeEdgeActive 等价）；
// 仅保留节点可见性 computeNodeVisibility（focus/hideCompleted）。
import { computeNodeVisibility } from "./topologyUtils"
import type { Node as RFNodeBase } from "@xyflow/react"

const GRID = 16
// 运行态 minimap 节点尺寸兜底：RunCanvas 挂载时其画布容器可能尚未定尺（run 数据加载中），
// ReactFlow 的节点测量落在测量竞态里 → node.measured 恒为空。MiniMap 的 nodeHasDimensions
// 门控要求 measured/width/initialWidth 至少其一存在，否则该节点一个方块都不画（PR-7 的
// 状态着色因此在运行态完全看不见）。给节点补 initialWidth/initialHeight 兜底——它们仅在
// measured 缺失时用于 minimap 取尺，绝不覆盖画布上的真实测量尺寸（见 @xyflow
// getNodeDimensions: measured ?? width ?? initialWidth）。studio 节点用近似常量；groupRun
// 折叠约 96、展开随页数估高。
const MINIMAP_STUDIO_W = 180
const MINIMAP_STUDIO_H = 64
const nodeTypes = { studio: WorkflowNode, groupRun: GroupNode }
// 运行态用只读 RunEdge（活动边渲流动粒子）；不复用编辑态 StudioEdge 的 +/× 控制簇。
const edgeTypes = { studio: RunEdge }

// SseConnState → SseIndicator 可视态（与 WorkbenchPage CONN_TO_STATUS 一致）。
const CONN_TO_STATUS: Record<SseConnState, SseStatus> = {
  idle: "disconnected",
  connected: "connected",
  reconnecting: "reconnecting",
  disconnected: "disconnected",
}

export interface RunCanvasProps {
  projectId: string
  org: string
  // 该次 run（plan）id；无则显示「尚无运行」空态。
  runId?: string
  // 工作流已保存节点（含 position）——运行模式用自存坐标，不用可编辑态。
  nodes: WorkflowNodeType[]
  // 该工作流 id：运行入口走 POST /workflows/{wfId}/run；缺省 → 运行按钮禁用。
  workflowId?: string
  // 运行期输入声明：非空则点「运行」先弹 RunInputsDialog，填完再发起 run。
  inputsSchema?: InputField[]
  onSelectRun: (runId: string) => void
}

// 运行模式画布：只读叠加某次 run 的执行状态。布局用工作流自存 position；
// 状态按 (type, 拓扑序序号) 结构映射注入 data.run（见 runOverlay）。
export function RunCanvas(props: RunCanvasProps) {
  return (
    <ReactFlowProvider>
      <RunCanvasInner {...props} />
    </ReactFlowProvider>
  )
}

function RunCanvasInner({
  projectId,
  org,
  runId,
  nodes,
  workflowId,
  inputsSchema,
  onSelectRun,
}: RunCanvasProps) {
  const qc = useQueryClient()
  const { isAdmin } = useRole(org)

  const plansQuery = usePlans(projectId)
  const stateQuery = useProjectState(projectId, runId ?? "")
  const runWorkflow = useRunWorkflow(projectId)
  const cancel = useCancel(projectId)
  // 运行期输入弹窗开合：inputsSchema 非空时点「运行」先弹表单，填完再发起 run。
  const [runInputsOpen, setRunInputsOpen] = useState(false)

  // 选中态：点节点 → 看工件（剧本/分镜抽屉 或 右栏资产预览 或大功能容器 Run Matrix）。
  const [selection, setSelection] = useState<RunSelection>(null)
  // 素材画廊开合。
  const [galleryOpen, setGalleryOpen] = useState(false)
  // 成品预览（全屏图文/歌词）开合。
  const [previewOpen, setPreviewOpen] = useState(false)
  // 变体 A：run 内融合审核抽屉（全屏 Dialog）开合 + 抽屉内选中资产（本地态，与画布选中独立）。
  const [reviewOpen, setReviewOpen] = useState(false)
  const [reviewSelectedId, setReviewSelectedId] = useState<string | null>(null)
  // 大功能容器折叠/展开（视图态，按画布节点 id）。绝不回写 dependsOn。
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())
  const onToggleGroup = useCallback((nodeId: string) => {
    setExpandedGroups((prev) => {
      const next = new Set(prev)
      if (next.has(nodeId)) next.delete(nodeId)
      else next.add(nodeId)
      return next
    })
  }, [])
  const onSelectPage = useCallback((nodeId: string, page: RunPage) => {
    setSelection({ kind: "group", groupId: nodeId, selectedPageKey: page.key })
  }, [])

  const { settings, update } = useTopologySettings(projectId)
  const timing = useNodeTiming(runId ?? "")
  const { fitView } = useReactFlow()

  // 抽屉数据 gated 拉取：非该类型时传 "" 不发请求（与 RunWorkbenchPage 一致）。
  const scriptQuery = useScript(
    selection?.kind === "script" ? projectId : "",
    runId,
    selection?.kind === "script" ? selection.todoId : undefined,
  )
  const shotsQuery = useShots(
    selection?.kind === "storyboard" ? projectId : "",
    runId,
    selection?.kind === "storyboard" ? selection.todoId : undefined,
  )
  // 该 run 项目资产：供画廊提示词元信息（按 assetId）。无 runId 时传 "" 不发请求。
  const projectAssetsQuery = useProjectAssets(runId ? projectId : "", undefined, runId)

  // 权威工作流状态（加载中回落 draft 草态）。useMemo 稳定化：未加载时 `??` 兜底对象
  // 每帧新建会令下游 overlay/runGroups memo 每帧失效，故记忆化。
  const wfState: ProjectState = useMemo(
    () =>
      stateQuery.data ?? {
        projectId,
        version: 0,
        status: "draft",
        runStatus: "idle",
        stages: [],
        pips: [],
        assets: { total: 0, done: 0, pending: 0 },
        nodes: [],
        edges: [],
        isCustom: false,
      },
    [stateQuery.data, projectId],
  )

  // overlay map：画布节点 id → run 状态（点节点解析选中态、画布注入 data.run 共用）。
  const overlay = useMemo(() => overlayRunStatus(nodes, wfState), [nodes, wfState])

  // 大功能容器分组：已命中 run 的 storyboard → 逐页 cell + 状态计数（Map<画布节点 id, RunGroup>）。
  const runGroups = useMemo(() => {
    const parents: ParentAnchor[] = nodes
      .filter((n) => n.type === "storyboard")
      .map((n) => ({ todoId: overlay.get(n.id)?.todoId, canvasNodeId: n.id }))
      .filter((p): p is ParentAnchor => p.todoId != null)
    const assetMetaById = new Map<string, AssetMeta>()
    for (const a of projectAssetsQuery.data ?? []) {
      assetMetaById.set(a.id, { kind: (a.type as AssetMeta["kind"]) ?? "unknown", shotId: a.shotId })
    }
    return buildRunGroups(wfState, parents, assetMetaById)
  }, [nodes, overlay, wfState, projectAssetsQuery.data])

  // 选中资产详情（hooks 规则：early return 前无条件调用）。
  // 用 stateQuery 的 pips 算回落 latestAssetId；选中大功能容器某页（image）则用该页 assetId。
  // useAsset 内部 enabled: id !== "" 防护空串不发请求。
  const latestAssetIdEarly = [...(stateQuery.data?.pips ?? [])]
    .reverse()
    .find((p) => p.status === "done" && p.assetId)?.assetId
  const selectedGroupPageEarly =
    selection?.kind === "group" && selection.selectedPageKey
      ? runGroups
          .get(selection.groupId)
          ?.pages.find((p) => p.key === selection.selectedPageKey)
      : undefined
  const previewAssetIdEarly =
    selection?.kind === "asset"
      ? selection.assetId
      : (selectedGroupPageEarly?.image?.assetId ?? latestAssetIdEarly)
  const previewDetailQuery = useAsset(previewAssetIdEarly ?? "")

  // SSE：回放 → 续接实时；驱动 live/conn 指示器与事件日志。state 帧写回缓存使画布实时刷新。
  const { log, conn } = useProductionTimeline({
    projectId,
    accessToken: getAccessToken(),
    status: stateQuery.data?.status,
    enabled: !!runId,
    fetchAllEvents,
    planId: runId,
    onState: (s) => qc.setQueryData(["project-state", projectId, runId ?? ""], s),
    onReplay: timing.onReplay,
    onFrame: timing.onFrame,
  })
  const live = wfState.runStatus !== "done" && !!runId

  const autoPos = useMemo(
    () => (settings.layout === "saved" ? null : autoLayout(nodes, settings.layout)),
    [nodes, settings.layout],
  )

  // 运行 rfNodes（融合 #136/#137 与 #127）：从已保存节点（自存 position / autoLayout 坐标）建，
  // 按 overlay 注入 data.run；有逐页扇出资产的 storyboard → 渲成可折叠大功能容器（groupRun）；
  // 同时叠加 #127 的 visibility（hidden/dim）+ timing chip + autoLayout 坐标。
  // 边的 active（流动粒子）走已合的 markActiveEdges（受设置面板 flowAnimation 控，见下 rfEdges）。
  const { rfNodes, baseEdges, hiddenIds, dimIds, visibleCount } = useMemo(() => {
    const { nodes: rn, edges: re } = toReactFlow(nodes)
    // 逻辑 hidden/dim id 集合（不回读 style，避免与其它 opacity 用途串扰）。
    const hidden = new Set<string>()
    const dim = new Set<string>()
    const mapped: RFNodeBase[] = rn.map((n) => {
      const run = overlay.get(n.id)
      const status: GraphNodeStatus = run?.status ?? "pending"
      const vis = computeNodeVisibility(status, settings)
      if (vis.hidden) hidden.add(n.id)
      if (vis.dimmed) dim.add(n.id)
      const t =
        settings.showTiming && run?.todoId
          ? timing.timingByTodoId.get(run.todoId)
          : undefined
      const position = autoPos?.get(n.id) ?? n.position
      const style = vis.dimmed ? { ...n.style, opacity: 0.35 } : n.style
      const baseData = {
        ...n.data,
        run,
        timing: t,
        highlightFailed: settings.focus === "failed",
      }
      const g = runGroups.get(n.id)
      if (g && n.data.node.type === "storyboard") {
        const data: GroupRunNodeData = {
          ...baseData,
          pages: g.pages,
          counts: g.counts,
          expanded: expandedGroups.has(n.id),
          selectedPageKey:
            selection?.kind === "group" && selection.groupId === n.id
              ? selection.selectedPageKey
              : undefined,
          onToggle: onToggleGroup,
          onSelectPage,
        }
        const expanded = data.expanded
        return {
          ...n,
          position,
          hidden: vis.hidden,
          style,
          type: "groupRun",
          data,
          // minimap 尺寸兜底（见 MINIMAP_STUDIO_* 注释）。groupRun 折叠 ~96、展开随页数估高。
          initialWidth: expanded ? 360 : 280,
          initialHeight: expanded ? 72 + g.pages.length * 96 : 96,
        }
      }
      return {
        ...n,
        position,
        hidden: vis.hidden,
        style,
        data: baseData,
        initialWidth: MINIMAP_STUDIO_W,
        initialHeight: MINIMAP_STUDIO_H,
      }
    })
    return {
      rfNodes: mapped,
      baseEdges: re as RFEdge[],
      hiddenIds: hidden,
      dimIds: dim,
      visibleCount: mapped.length - hidden.size,
    }
  }, [
    nodes,
    overlay,
    runGroups,
    expandedGroups,
    selection,
    onToggleGroup,
    onSelectPage,
    settings,
    timing.timingByTodoId,
    autoPos,
  ])

  // 活动边（markActiveEdges + RunEdge 粒子）受设置面板 flowAnimation 控；
  // 再叠加节点可见性导致的边 hidden/dim。
  const rfEdges = useMemo(() => {
    const active = markActiveEdges(baseEdges, overlay, settings.flowAnimation)
    return active.map((e) => {
      const edgeHidden = hiddenIds.has(e.source) || hiddenIds.has(e.target)
      const edgeDim = dimIds.has(e.source) || dimIds.has(e.target)
      return {
        ...e,
        hidden: edgeHidden,
        style: edgeDim ? { ...e.style, opacity: 0.35 } : e.style,
      }
    })
  }, [baseEdges, overlay, settings.flowAnimation, hiddenIds, dimIds])

  // 布局变更且 fitOnUpdate 时命令式 fitView。
  useEffect(() => {
    if (settings.layout !== "saved" && settings.fitOnUpdate) {
      const h = requestAnimationFrame(() => fitView({ duration: 200 }))
      return () => cancelAnimationFrame(h)
    }
  }, [settings.layout, settings.fitOnUpdate, fitView])

  // 点节点（运行模式只读）：按 overlay 命中项 + 节点类型解析选中态。
  // 未命中（pending/未匹配）节点点击无操作。
  function handleSelectNode(canvasNodeId: string) {
    const node = nodes.find((n) => n.id === canvasNodeId)
    if (!node) return
    const sel = resolveSelection(node.type, overlay.get(canvasNodeId))
    if (sel) setSelection(sel)
  }

  // 运行控制（移植 RunWorkbenchPage.handleRun/handleCancel）。
  const isLatestPlan = !!(
    plansQuery.data &&
    plansQuery.data.length > 0 &&
    plansQuery.data[0].id === runId
  )
  const canCancel =
    isLatestPlan && (wfState.status === "running" || wfState.status === "planning")
  const isRunning = runWorkflow.isPending || cancel.isPending

  // 运行入口（与项目页 handleRunWorkflow 一致）：inputsSchema 非空 → 先弹运行期
  // 表单；空 → 直接跑。workflowId 缺省时运行按钮已禁用，这里兜底 no-op。
  function handleRun() {
    if (!workflowId) return
    if (inputsSchema && inputsSchema.length > 0) {
      setRunInputsOpen(true)
      return
    }
    void doRunWorkflow()
  }

  async function doRunWorkflow(inputs?: Record<string, unknown>) {
    if (!workflowId) return
    try {
      const res = await runWorkflow.mutateAsync({ wfId: workflowId, inputs })
      if (res.fallbackUsed) {
        toast.warning("工作流校验未通过，已回落默认管线")
      } else {
        toast.success("已开始运行")
      }
      onSelectRun(res.planId)
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        toast.error("配额已用尽，请稍后再试")
        return
      }
      if (err instanceof ApiError && err.status === 400) {
        const msg = err.body.replace(/^invalid workflow:\s*/i, "").replace(/^custom workflow:\s*/i, "").trim()
        toast.error(msg || "工作流配置无效")
        return
      }
      toast.error("运行失败")
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

  const status = wfState.status
  const runStatus = wfState.runStatus
  const readyForReview = runStatus === "done" || status === "review"
  const showReviewBadge =
    runStatus === "done" && status !== "failed" && status !== "canceled"
  const badge = showReviewBadge ? (
    <Badge variant="pending">待审核 · {wfState.assets.pending}</Badge>
  ) : (
    <Badge variant={statusVariant(status)}>{statusLabel(status)}</Badge>
  )

  // 完成上升沿召唤：runStatus 从「非 done」跃到「done」（且非失败/取消）时弹一次 toast，
  //   带「开始审核」动作就地开抽屉。SSE 回放历史帧结束时 prev 已是 done，不会误触发。
  const prevRunStatus = useRef(runStatus)
  useEffect(() => {
    const was = prevRunStatus.current
    prevRunStatus.current = runStatus
    if (
      was !== "done" &&
      runStatus === "done" &&
      status !== "failed" &&
      status !== "canceled"
    ) {
      toast.success(`生成完成 · ${wfState.assets.pending} 张待审`, {
        action: { label: "开始审核", onClick: () => setReviewOpen(true) },
        duration: 8000,
      })
    }
    // wfState.assets.pending 仅取快照读数，不入依赖（避免 pending 变动重弹）。
  }, [runStatus, status]) // eslint-disable-line react-hooks/exhaustive-deps

  // 资产预览：选中资产则看选中，否则回落最近一个 done 且有 assetId 的 pip。
  const latestAssetId = [...wfState.pips]
    .reverse()
    .find((p) => p.status === "done" && p.assetId)?.assetId
  const previewAssetId =
    selection?.kind === "asset" ? selection.assetId : latestAssetId
  // 已生成素材（done 且有 assetId），按生成顺序——供「查看全部素材」画廊。
  const doneAssetIds = wfState.pips
    .filter((p) => p.status === "done" && p.assetId)
    .map((p) => p.assetId as string)
  // 画廊灯箱提示词：done 资产的 prompt/provider/model（按 assetId）。
  const assetMeta: Record<string, { prompt?: string; provider?: string; model?: string }> = {}
  for (const a of projectAssetsQuery.data ?? []) {
    if (a.status === "done") {
      assetMeta[a.id] = { prompt: a.prompt, provider: a.provider, model: a.model }
    }
  }

  // 成品预览门控：有已生成素材，或有自定义节点文本产物（歌词/故事）时可预览。
  const hasCustomText = wfState.nodes.some(
    (n) => n.type.startsWith("custom:") && !!n.output,
  )
  const canPreview = doneAssetIds.length > 0 || hasCustomText

  // 抽屉：选中 script/storyboard 时打开 Sheet 看工件。
  const drawerKind =
    selection?.kind === "script" || selection?.kind === "storyboard"
      ? selection.kind
      : null

  // 运行期输入表单：inputsSchema 非空时点「运行」弹出，填完再发起 run。
  // 条件挂载 → 每次打开都以最新 schema 重置内部表单态（与项目页一致）。
  const runInputsDialog = runInputsOpen && inputsSchema && inputsSchema.length > 0 && (
    <RunInputsDialog
      open
      onOpenChange={(o) => {
        if (!o) setRunInputsOpen(false)
      }}
      schema={inputsSchema}
      submitting={runWorkflow.isPending}
      onSubmit={async (inputs) => {
        await doRunWorkflow(inputs)
        setRunInputsOpen(false)
      }}
    />
  )

  // 无 runId：居中「尚无运行」空态 + 运行按钮。
  if (!runId) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 bg-bg-base text-center">
        <p className="text-[13px] text-text-2">尚无运行</p>
        <p className="text-[12px] text-text-3">点「运行」开始一次生产，状态将叠加到画布上</p>
        <Button variant="amber" onClick={handleRun} disabled={isRunning || !workflowId}>
          运行
        </Button>
        {runInputsDialog}
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1">
      {/* 左栏：运行选择器 + 运行汇总 + 事件日志。 */}
      <aside className="flex w-[240px] shrink-0 flex-col gap-4 overflow-y-auto border-r border-line bg-bg-surface p-4">
        <section>
          <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
            运行记录
          </h4>
          <RunSelector
            plans={plansQuery.data ?? []}
            currentRunId={runId}
            onSelectRun={onSelectRun}
          />
        </section>
        <section>
          <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
            运行汇总
          </h4>
          <RunSummary state={wfState} className="rounded-lg border border-line bg-bg-base px-3 py-2" />
        </section>
        {/* 本次运行成本：admin 门控（成本端点是 admin 门槛，非 admin 不发请求不吃 403）。 */}
        {isAdmin && (
          <section>
            <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
              本次成本
            </h4>
            <RunCostSummary
              projectId={projectId}
              planId={runId}
              live={live}
              className="rounded-lg border border-line bg-bg-base px-3 py-2"
            />
          </section>
        )}
        <section>
          <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
            事件日志
          </h4>
          <EventLog lines={log} />
        </section>
      </aside>

      {/* 中：只读画布。点节点 → 选中工件。 */}
      <div className="workflow-canvas relative flex-1">
        {/* 完成 banner：生成完成且抽屉未开时常驻细条，画布顶居中；pointer-events 只落在细条本身，
            不遮挡画布拖拽/缩放。点「开始审核」就地开融合抽屉。 */}
        {readyForReview && !reviewOpen && (
          <div className="pointer-events-none absolute left-1/2 top-4 z-10 -translate-x-1/2">
            <div className="pointer-events-auto flex items-center gap-3 rounded-full border border-line bg-bg-surface px-4 py-2 shadow-sm">
              <span className="text-[12.5px] text-text-1">
                ✓ 本次生成完成 · {wfState.assets.pending} 张待审
              </span>
              <Button variant="amber" onClick={() => setReviewOpen(true)}>
                开始审核
              </Button>
            </div>
          </div>
        )}
        <ReactFlow
          nodes={rfNodes}
          edges={rfEdges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable
          deleteKeyCode={null}
          onNodeClick={(_, node) => {
            // 大功能容器：点容器主体（非 header/子卡，二者已 stopPropagation）→ 选中该组，
            // 右栏渲 Run Matrix（不预选某页）。
            if (node.type === "groupRun") {
              setSelection({ kind: "group", groupId: node.id })
              return
            }
            handleSelectNode(node.id)
          }}
          fitView
          proOptions={{ hideAttribution: false }}
        >
          <Background gap={GRID} />
          <Controls showInteractive={false} />
          {/* 运行态 minimap：按 run 状态着色（导航 · 按状态着色）。studio 与 groupRun 节点
              都带 data.run（overlay 注入），统一取状态色；失败节点加 danger 描边强调。 */}
          <MiniMap
            pannable
            zoomable
            nodeColor={(n) =>
              minimapStatusColor((n as Node<StudioNodeData>).data.run?.status)
            }
            nodeStrokeColor={(n) =>
              (n as Node<StudioNodeData>).data.run?.status === "failed"
                ? "var(--danger)"
                : "transparent"
            }
            nodeStrokeWidth={3}
          />
        </ReactFlow>
        {rfNodes.length === 0 && (
          <div className="pointer-events-none absolute inset-0 grid place-items-center">
            <p className="rounded-md border border-line bg-bg-surface/80 px-4 py-2 text-center text-[12.5px] text-text-3">
              该工作流暂无节点
            </p>
          </div>
        )}
        {rfNodes.length > 0 && visibleCount === 0 && (
          <div className="pointer-events-none absolute inset-0 grid place-items-center">
            <div className="pointer-events-auto flex flex-col items-center gap-2 rounded-md border border-line bg-bg-surface/90 px-4 py-3 text-center shadow-sm">
              <p className="text-[12.5px] text-text-3">当前过滤隐藏了所有节点</p>
              <button
                type="button"
                onClick={() => update({ hideCompleted: false, focus: "none" })}
                className="rounded border border-line bg-bg-raised px-2 py-1 text-[12px] text-text-1 hover:bg-bg-surface hover:text-text-1"
              >
                清除过滤
              </button>
            </div>
          </div>
        )}
      </div>

      {/* 右：选中工件预览（资产 或 自定义节点文本产物）。无选中则回落最近 done 资产。 */}
      <aside className="flex w-[260px] shrink-0 flex-col overflow-y-auto border-l border-line bg-bg-surface p-4">
        <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
          选中工件
        </h4>
        {/* 选中大功能容器 → Run Matrix（逐页状态格 + 选中页产物）。取代 storyboard 旧 ItemInspector。
            P5d：其余选中态有 items → per-item inspector；items 缺省/空 → 回落标量面板。 */}
        {selection?.kind === "group" ? (
          <RunMatrix
            group={runGroups.get(selection.groupId)}
            selectedPageKey={selection.selectedPageKey}
            onSelectPage={(page) =>
              setSelection({
                kind: "group",
                groupId: selection.groupId,
                selectedPageKey: page.key,
              })
            }
            org={org}
            isAdmin={isAdmin}
            assetDetail={previewDetailQuery.data}
          />
        ) : selection?.items && selection.items.length > 0 ? (
          <ItemInspector items={selection.items} />
        ) : selection?.kind === "custom" ? (
          selection.outputFormat === "http-status" ? (
            <SuppressedBodyPanel content={selection.output} />
          ) : (
            <div className="flex flex-col gap-1.5">
              <p className="text-[11px] text-text-3">
                {selection.outputFormat === "json" ? "JSON 产物" : "文本产物"}
              </p>
              <pre className="overflow-auto rounded-md border border-line bg-bg-base p-2 text-[11px] leading-relaxed text-text-1 whitespace-pre-wrap break-words">
                {selection.output}
              </pre>
            </div>
          )
        ) : previewAssetId ? (
          <SelectedAssetPanel
            org={org}
            assetId={previewAssetId}
            isAdmin={isAdmin}
            detail={previewDetailQuery.data}
          />
        ) : (
          <div className="flex flex-1 flex-col items-center justify-center gap-1.5 py-16 text-center">
            <p className="text-[13px] text-text-2">选择一个节点查看产物</p>
            <p className="text-[12px] text-text-3">点剧本/分镜节点看文本，点图片节点看素材</p>
          </div>
        )}
      </aside>

      {/* 运行顶栏控制：徽标 / SSE / 去审核 / 运行控制——挂在主区右上浮层，与编辑顶栏区分。 */}
      <div className="pointer-events-none absolute right-[280px] top-[56px] z-10 flex items-center gap-2">
        <div className="pointer-events-auto flex items-center gap-2 rounded-md border border-line bg-bg-surface/90 px-3 py-1.5 shadow-sm">
          {/* 流动效果开关已并入 TopologySettingsPanel（settings.flowAnimation），不再单列。 */}
          <TopologySettingsPanel settings={settings} update={update} />
          {badge}
          {live && <SseIndicator status={CONN_TO_STATUS[conn]} />}
          {doneAssetIds.length > 0 && (
            <Button variant="ghost" onClick={() => setGalleryOpen(true)}>
              查看全部素材 ({doneAssetIds.length})
            </Button>
          )}
          {canPreview && (
            <Button variant="ghost" onClick={() => setPreviewOpen(true)}>
              成品预览
            </Button>
          )}
          {/* 完成态审核不再跳走：就地开融合抽屉，提升为 amber 主按钮（org 级审核台有自己入口）。 */}
          {readyForReview && (
            <Button variant="amber" onClick={() => setReviewOpen(true)}>
              开始审核
            </Button>
          )}
          {canCancel && (
            <Button variant="ghost" onClick={handleCancel} disabled={isRunning}>
              取消
            </Button>
          )}
          {/* 完成态时「重新运行」降级为次要动作，让「开始审核」当主 CTA；非完成态维持 amber。 */}
          <Button
            variant={readyForReview ? "ghost" : "amber"}
            onClick={handleRun}
            disabled={isRunning || !workflowId}
          >
            {runStatus === "idle" ? "运行" : "重新运行"}
          </Button>
        </div>
      </div>

      {/* 剧本/分镜抽屉：选中 script/storyboard 节点时打开。 */}
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

      {/* 素材画廊：top-bar「查看全部素材」打开。 */}
      <AssetGalleryModal
        assetIds={doneAssetIds}
        metaById={assetMeta}
        open={galleryOpen}
        onOpenChange={setGalleryOpen}
      />

      {/* 成品预览：top-bar「成品预览」打开。全屏图文/歌词，按内容启发式选模式。 */}
      {runId && (
        <RunPreview
          open={previewOpen}
          onOpenChange={setPreviewOpen}
          projectId={projectId}
          planId={runId}
          nodes={wfState.nodes}
        />
      )}

      {/* 变体 A：run 内融合审核抽屉（全屏 Dialog）。生成完成不跳走，就地审「刚生成这批」，
          复用 org 级审核容器 + inline 双栏（队列左 / 详情右）。选中态用本地 reviewSelectedId，
          与画布选中独立；关抽屉时重置。ReviewBoard 内部 useRole 决定 isAdmin（非 admin 只读）。 */}
      <Dialog
        open={reviewOpen}
        onOpenChange={(open) => {
          setReviewOpen(open)
          if (!open) setReviewSelectedId(null)
        }}
      >
        <DialogContent className="flex h-[92vh] max-h-[92vh] w-full max-w-[min(96vw,1080px)] flex-col gap-0 overflow-hidden bg-bg-surface p-0 sm:max-w-[min(96vw,1080px)]">
          {/* Radix a11y：DialogContent 必带 Title；审核详情自带可视标题，这里 sr-only。 */}
          <DialogTitle className="sr-only">审核</DialogTitle>
          <ReviewBoard
            org={org}
            projectFilter={projectId}
            inlineDetail
            selectedId={reviewSelectedId}
            onSelect={setReviewSelectedId}
            onBackToWork={() => setReviewOpen(false)}
            onOpenPreview={() => {
              setReviewOpen(false)
              setPreviewOpen(true)
            }}
          />
        </DialogContent>
      </Dialog>

      {runInputsDialog}
    </div>
  )
}

// 从 http-status 产物内容 {"status":N} 解析状态码；解析失败返回 null。
export function parseHttpStatus(content: string): number | null {
  try {
    const obj = JSON.parse(content) as { status?: unknown }
    return typeof obj.status === "number" ? obj.status : null
  } catch {
    return null
  }
}

// http 节点响应体被安全策略抑制时的产物面板：只展示「已完成 + 状态码」，绝不 dump body。
export function SuppressedBodyPanel({ content }: { content: string }) {
  const status = parseHttpStatus(content)
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-[11px] text-text-3">HTTP 产物</p>
      <div className="rounded-md border border-line bg-bg-base p-2.5 text-[11px] leading-relaxed text-text-1">
        <p>已完成（响应体已按安全策略隐藏）</p>
        {status !== null && (
          <p className="mt-1 text-text-2">
            状态码：<span className="font-mono text-text-1">{status}</span>
          </p>
        )}
      </div>
    </div>
  )
}

// 运行选择器：列项目全部 plan（#N · createdAt · status），选中切 ?run=。
// 跨工作流过滤需 Phase 3 后端字段，Phase 1 列项目全部 plans。
function RunSelector({
  plans,
  currentRunId,
  onSelectRun,
}: {
  plans: Plan[]
  currentRunId: string
  onSelectRun: (runId: string) => void
}) {
  if (plans.length === 0) {
    return <p className="text-[12px] text-text-3">暂无运行记录</p>
  }
  return (
    <select
      aria-label="选择运行记录"
      value={currentRunId}
      onChange={(e) => onSelectRun(e.target.value)}
      className="w-full rounded-md border border-line bg-bg-base px-2 py-1.5 text-[12px] text-text-1 focus:border-amber focus:outline-none"
    >
      {plans.map((plan, index) => {
        const runNum = plans.length - index
        const when = new Date(plan.createdAt).toLocaleString()
        return (
          <option key={plan.id} value={plan.id}>
            #{runNum} · {when} · {statusLabel(plan.status as ProjectStatus)}
          </option>
        )
      })}
    </select>
  )
}
