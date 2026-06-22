import { useMemo } from "react"
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  type Node,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"
import "./canvasTheme.css"
import type { ProjectStatus, WorkflowNode as WorkflowNodeType } from "@/lib/types"
import type { ProjectState } from "@/lib/projectState"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import { SseIndicator, type SseStatus } from "@/components/studio/SseIndicator"
import { getAccessToken } from "@/lib/apiClient"
import { statusLabel, statusVariant } from "@/features/projects/status"
import {
  fetchAllEvents,
  useCancel,
  useProjectState,
  usePlans,
  useRun,
  type Plan,
} from "@/features/workflow/api"
import {
  useProductionTimeline,
  type SseConnState,
} from "@/features/workflow/useProductionTimeline"
import { useQueryClient } from "@tanstack/react-query"
import { toReactFlow, type RFNode, type RFEdge, type StudioNodeData } from "./canvasModel"
import { overlayRunStatus } from "./runOverlay"
import { WorkflowNode } from "./WorkflowNode"
import { StudioEdge } from "./StudioEdge"
import { NODE_COLOR } from "./nodeColor"

const GRID = 16
const nodeTypes = { studio: WorkflowNode }
const edgeTypes = { studio: StudioEdge }

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

  const plansQuery = usePlans(projectId)
  const stateQuery = useProjectState(projectId, runId ?? "")
  const run = useRun(projectId)
  const cancel = useCancel(projectId)

  // 权威工作流状态（加载中回落 draft 草态）。
  const wfState: ProjectState = stateQuery.data ?? {
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
  }

  // SSE：驱动 live/conn 指示器（完整日志面板留 Phase 2）。state 帧写回缓存使画布实时刷新。
  const { conn } = useProductionTimeline({
    projectId,
    accessToken: getAccessToken(),
    status: stateQuery.data?.status,
    enabled: !!runId,
    fetchAllEvents,
    planId: runId,
    onState: (s) => qc.setQueryData(["project-state", projectId, runId ?? ""], s),
  })
  const live = wfState.runStatus !== "done" && !!runId

  // 运行 rfNodes：从已保存节点（自存 position）建，按 overlay 注入 data.run。
  const { rfNodes, rfEdges } = useMemo(() => {
    const { nodes: rn, edges: re } = toReactFlow(nodes)
    const overlay = overlayRunStatus(nodes, wfState)
    const withRun: RFNode[] = rn.map((n) => ({
      ...n,
      data: { ...n.data, run: overlay.get(n.id) },
    }))
    return { rfNodes: withRun, rfEdges: re as RFEdge[] }
  }, [nodes, wfState])

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
      const res = await run.mutateAsync()
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
      {/* 左栏：运行选择器 + 运行汇总占位。 */}
      <aside className="flex w-[240px] shrink-0 flex-col gap-4 border-r border-line bg-bg-surface p-4">
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
          <div className="space-y-1.5 text-[12px] text-text-2">
            <SummaryRow label="状态" value={statusLabel(status)} />
            <SummaryRow
              label="素材"
              value={`${wfState.assets.done}/${wfState.assets.total || "?"}`}
            />
            <SummaryRow label="待处理" value={String(wfState.assets.pending)} />
          </div>
        </section>
      </aside>

      {/* 中：只读画布。 */}
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
          fitView
          proOptions={{ hideAttribution: false }}
        >
          <Background gap={GRID} />
          <Controls showInteractive={false} />
          <MiniMap
            nodeColor={(n) =>
              NODE_COLOR[(n as Node<StudioNodeData>).data.node.type] ??
              "var(--line)"
            }
          />
        </ReactFlow>
        {rfNodes.length === 0 && (
          <div className="pointer-events-none absolute inset-0 grid place-items-center">
            <p className="rounded-md border border-line bg-bg-surface/80 px-4 py-2 text-center text-[12.5px] text-text-3">
              该工作流暂无节点
            </p>
          </div>
        )}
      </div>

      {/* 右：选中产物占位（Phase 2 实现点节点看产物）。 */}
      <aside className="flex w-[260px] shrink-0 flex-col border-l border-line bg-bg-surface p-4">
        <h4 className="mb-2 text-[11px] font-semibold tracking-[0.08em] text-text-3">
          选中工件
        </h4>
        <div className="flex flex-1 flex-col items-center justify-center gap-1.5 py-16 text-center">
          <p className="text-[13px] text-text-2">选择一个节点查看产物</p>
          <p className="text-[12px] text-text-3">（下一阶段）</p>
        </div>
      </aside>

      {/* 运行顶栏控制：徽标 / SSE / 去审核 / 运行控制——挂在主区右上浮层，与编辑顶栏区分。 */}
      <div className="pointer-events-none absolute right-[280px] top-[56px] z-10 flex items-center gap-2">
        <div className="pointer-events-auto flex items-center gap-2 rounded-md border border-line bg-bg-surface/90 px-3 py-1.5 shadow-sm">
          {badge}
          {live && <SseIndicator status={CONN_TO_STATUS[conn]} />}
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
    </div>
  )
}

function SummaryRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between">
      <span>{label}</span>
      <b className="font-medium text-text-1">{value}</b>
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
