import { useCallback, useEffect, useMemo, useState } from "react"
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
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import "./canvasTheme.css"
import type { ProjectStatus, WorkflowNode as WorkflowNodeType } from "@/lib/types"
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
import { EventLog } from "@/components/studio/EventLog"
import { getAccessToken } from "@/lib/apiClient"
import { statusLabel, statusVariant } from "@/features/projects/status"
import {
  fetchAllEvents,
  useCancel,
  useProjectAssets,
  useProjectState,
  usePlans,
  useRun,
  useScript,
  useShots,
  type Plan,
} from "@/features/workflow/api"
import { useAsset } from "@/features/review/api"
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
  type AssetKind,
  type ParentAnchor,
  type GroupCell,
} from "./runFanout"
import { resolveSelection, type RunSelection } from "./resolveSelection"
import { ItemInspector } from "./ItemInspector"
import { WorkflowNode } from "./WorkflowNode"
import { GroupNode, type GroupRunNodeData } from "./GroupNode"
import { RunMatrix } from "./RunMatrix"
import { RunEdge } from "./RunEdge"
import { markActiveEdges } from "./runEdges"
import { NODE_COLOR } from "./nodeColor"
import { autoLayout } from "@/lib/autoLayout"
import { useNodeTiming } from "@/features/workflow/useNodeTiming"
import { useTopologySettings } from "./useTopologySettings"
import { TopologySettingsPanel } from "./TopologySettingsPanel"
// 边活动判定改用已合的 runEdges.markActiveEdges（与 #127 的 computeEdgeActive 等价）；
// 仅保留节点可见性 computeNodeVisibility（focus/hideCompleted）。
import { computeNodeVisibility } from "./topologyUtils"
import type { Node as RFNodeBase } from "@xyflow/react"

const GRID = 16
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
  onSelectRun,
}: RunCanvasProps) {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { isAdmin } = useRole(org)

  const plansQuery = usePlans(projectId)
  const stateQuery = useProjectState(projectId, runId ?? "")
  const run = useRun(projectId)
  const cancel = useCancel(projectId)

  // 选中态：点节点 → 看工件（剧本/分镜抽屉 或 右栏资产预览 或大功能容器 Run Matrix）。
  const [selection, setSelection] = useState<RunSelection>(null)
  // 素材画廊开合。
  const [galleryOpen, setGalleryOpen] = useState(false)
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
  const onSelectCell = useCallback((nodeId: string, cell: GroupCell) => {
    setSelection({ kind: "group", groupId: nodeId, selectedTodoId: cell.todoId })
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
    const assetKindById = new Map<string, AssetKind>()
    for (const a of projectAssetsQuery.data ?? []) {
      assetKindById.set(a.id, (a.type as AssetKind) ?? "unknown")
    }
    return buildRunGroups(wfState, parents, assetKindById)
  }, [nodes, overlay, wfState, projectAssetsQuery.data])

  // 选中资产详情（hooks 规则：early return 前无条件调用）。
  // 用 stateQuery 的 pips 算回落 latestAssetId；选中大功能容器某页（image）则用该页 assetId。
  // useAsset 内部 enabled: id !== "" 防护空串不发请求。
  const latestAssetIdEarly = [...(stateQuery.data?.pips ?? [])]
    .reverse()
    .find((p) => p.status === "done" && p.assetId)?.assetId
  const selectedGroupCellEarly =
    selection?.kind === "group" && selection.selectedTodoId
      ? runGroups
          .get(selection.groupId)
          ?.cells.find((c) => c.todoId === selection.selectedTodoId)
      : undefined
  const previewAssetIdEarly =
    selection?.kind === "asset"
      ? selection.assetId
      : (selectedGroupCellEarly?.assetId ?? latestAssetIdEarly)
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
          cells: g.cells,
          counts: g.counts,
          expanded: expandedGroups.has(n.id),
          selectedTodoId:
            selection?.kind === "group" && selection.groupId === n.id
              ? selection.selectedTodoId
              : undefined,
          onToggle: onToggleGroup,
          onSelectCell,
        }
        return { ...n, position, hidden: vis.hidden, style, type: "groupRun", data }
      }
      return { ...n, position, hidden: vis.hidden, style, data: baseData }
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
    onSelectCell,
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
  const isRunning = run.isPending || cancel.isPending

  async function handleRun() {
    try {
      const res = await run.mutateAsync(undefined)
      if (res.fallbackUsed) {
        toast.warning("Planner 输出畸形，已回落默认管线")
      } else {
        toast.success("已开始运行")
      }
      onSelectRun(res.planId)
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

  // 抽屉：选中 script/storyboard 时打开 Sheet 看工件。
  const drawerKind =
    selection?.kind === "script" || selection?.kind === "storyboard"
      ? selection.kind
      : null

  // 无 runId：居中「尚无运行」空态 + 运行按钮。
  if (!runId) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 bg-bg-base text-center">
        <p className="text-[13px] text-text-2">尚无运行</p>
        <p className="text-[12px] text-text-3">点「运行」开始一次生产，状态将叠加到画布上</p>
        <Button variant="amber" onClick={handleRun} disabled={isRunning}>
          运行
        </Button>
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
        <section>
          <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
            事件日志
          </h4>
          <EventLog lines={log} />
        </section>
      </aside>

      {/* 中：只读画布。点节点 → 选中工件。 */}
      <div className="workflow-canvas relative flex-1">
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
          <MiniMap
            nodeColor={(n) => {
              // studio 与 groupRun 节点都带 data.node（groupRun 复用 storyboard 节点），统一取色。
              return (
                NODE_COLOR[(n as Node<StudioNodeData>).data.node.type] ??
                "var(--line)"
              )
            }}
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
            selectedTodoId={selection.selectedTodoId}
            onSelectCell={(cell) =>
              setSelection({
                kind: "group",
                groupId: selection.groupId,
                selectedTodoId: cell.todoId,
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
          {readyForReview && (
            <Button
              variant="ghost"
              onClick={() =>
                navigate({
                  to: "/orgs/$org/review",
                  params: { org },
                  search: { project: projectId },
                })
              }
            >
              去审核 →
            </Button>
          )}
          {canCancel && (
            <Button variant="ghost" onClick={handleCancel} disabled={isRunning}>
              取消
            </Button>
          )}
          <Button variant="amber" onClick={handleRun} disabled={isRunning}>
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
