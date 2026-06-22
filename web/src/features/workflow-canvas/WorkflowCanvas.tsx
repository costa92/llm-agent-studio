import { useCallback, useMemo, useRef, useState } from "react"
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  useReactFlow,
  addEdge,
  type Node,
  type Connection,
  type OnSelectionChangeParams,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { toast } from "sonner"
import "./canvasTheme.css"
import { ApiError } from "@/lib/apiClient"
import type {
  BasicPrompt,
  Prompt,
  WorkflowNode as WorkflowNodeType,
} from "@/lib/types"
import {
  useCreateWorkflow,
  useUpdateWorkflow,
} from "@/features/projects/workflowApi"
import { findGraphError } from "@/features/projects/WorkflowDialog.schema"
import {
  toReactFlow,
  toStudioNodes,
  addNodeAt,
  standardPipeline,
  type StudioNodeData,
  type RFNode,
  type RFEdge,
} from "./canvasModel"
import { WorkflowNode } from "./WorkflowNode"
import { NodePalette, PALETTE_DND_TYPE } from "./NodePalette"
import { PropertiesPanel } from "./PropertiesPanel"
import { NODE_COLOR } from "./nodeColor"

// Phase 2：可编辑工作流画布。三栏布局（节点面板 / 画布 / 属性面板）。
// EDGES 是 dependsOn 的唯一真源（见 canvasModel.toStudioNodes）：连线/断线/重命名
// 级联只维护边，保存时由边推导每个节点的 dependsOn。data.node 持有其余 studio 字段。
export interface WorkflowCanvasProps {
  workflowId?: string
  projectId: string
  org: string
  workflowName: string
  nodes: WorkflowNodeType[]
  prompts?: Prompt[]
  basics?: BasicPrompt[]
  // 返回项目页（顶栏「返回」）。由路由层注入，画布本身不持有路由知识。
  onBack?: () => void
  // 新建成功后导航到 ?wf=<newId>（由路由注入）。
  onCreated?: (workflowId: string) => void
}

const nodeTypes = { studio: WorkflowNode }

// 保存载荷的稳定签名（name + studio nodes）。用于 dirty 比较。
function snapshotOf(name: string, studioNodes: WorkflowNodeType[]): string {
  return JSON.stringify({ name, nodes: studioNodes })
}

function CanvasInner({
  workflowId,
  projectId,
  org,
  workflowName: initialName,
  nodes,
  prompts,
  basics,
  onBack,
  onCreated,
}: WorkflowCanvasProps) {
  const initial = useMemo(() => toReactFlow(nodes), [nodes])
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState(initial.nodes)
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState(initial.edges)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  // 名称：编辑态用既有名（只读展示），新建态可编辑（无独立名称对话框）。
  const [workflowName, setWorkflowName] = useState(initialName)
  const { screenToFlowPosition } = useReactFlow()
  const isCreate = !workflowId

  // 载入时的 studio-model 快照，作为 dirty 比较基线。
  const loadedSnapshot = useRef(
    snapshotOf(initialName, toStudioNodes(initial.nodes, initial.edges)),
  )

  const createWorkflow = useCreateWorkflow(projectId)
  const updateWorkflow = useUpdateWorkflow(projectId)

  const currentSnapshot = snapshotOf(
    workflowName,
    toStudioNodes(rfNodes as RFNode[], rfEdges),
  )
  const dirty = currentSnapshot !== loadedSnapshot.current
  const saving = createWorkflow.isPending || updateWorkflow.isPending

  // ── 选中节点 ──────────────────────────────────────────────
  const onSelectionChange = useCallback(
    ({ nodes: sel }: OnSelectionChangeParams) => {
      setSelectedId(sel.length === 1 ? sel[0].id : null)
    },
    [],
  )
  const selected =
    (rfNodes as RFNode[]).find((n) => n.id === selectedId)?.data.node ?? null

  // ── 拖入添加 ──────────────────────────────────────────────
  const onDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    e.dataTransfer.dropEffect = "move"
  }, [])

  const onDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault()
      const type = e.dataTransfer.getData(PALETTE_DND_TYPE)
      if (!type) return
      const pos = screenToFlowPosition({ x: e.clientX, y: e.clientY })
      setRfNodes((nds) => addNodeAt(nds as RFNode[], type, pos, prompts))
    },
    [screenToFlowPosition, setRfNodes, prompts],
  )

  // ── 连线（带环路守卫） ───────────────────────────────────
  const onConnect = useCallback(
    (conn: Connection) => {
      if (!conn.source || !conn.target) return
      // 用候选模型（target.dependsOn += source）跑 findGraphError 做环路守卫。
      const candidate = toStudioNodes(rfNodes as RFNode[], [
        ...rfEdges,
        {
          id: `${conn.source}->${conn.target}`,
          source: conn.source,
          target: conn.target,
        },
      ])
      const err = findGraphError(candidate)
      if (err) {
        toast.error(err)
        return
      }
      setRfEdges((eds) =>
        addEdge(
          { ...conn, id: `${conn.source}->${conn.target}` },
          eds,
        ),
      )
    },
    [rfNodes, rfEdges, setRfEdges],
  )

  // ── 属性面板回调 ─────────────────────────────────────────
  const patchSelected = useCallback(
    (patch: Partial<WorkflowNodeType>) => {
      if (!selectedId) return
      setRfNodes((nds) =>
        (nds as RFNode[]).map((n) =>
          n.id === selectedId
            ? { ...n, data: { ...n.data, node: { ...n.data.node, ...patch } } }
            : n,
        ),
      )
    },
    [selectedId, setRfNodes],
  )

  // id 重命名：替换 RF 节点 id + data.node.id，并重键所有 source/target===oldId 的边
  //（边是 dependsOn 真源，故重键边即级联更新依赖）。
  const renameSelected = useCallback(
    (newId: string) => {
      const oldId = selectedId
      if (!oldId || newId === oldId) return
      setRfNodes((nds) =>
        (nds as RFNode[]).map((n) =>
          n.id === oldId
            ? { ...n, id: newId, data: { ...n.data, node: { ...n.data.node, id: newId } } }
            : n,
        ),
      )
      setRfEdges((eds) =>
        eds.map((e) => {
          const source = e.source === oldId ? newId : e.source
          const target = e.target === oldId ? newId : e.target
          return source === e.source && target === e.target
            ? e
            : { ...e, source, target, id: `${source}->${target}` }
        }),
      )
      setSelectedId(newId)
    },
    [selectedId, setRfNodes, setRfEdges],
  )

  const deleteSelected = useCallback(() => {
    const id = selectedId
    if (!id) return
    setRfNodes((nds) => (nds as RFNode[]).filter((n) => n.id !== id))
    setRfEdges((eds) => eds.filter((e) => e.source !== id && e.target !== id))
    setSelectedId(null)
  }, [selectedId, setRfNodes, setRfEdges])

  // ── 标准管线一键填充 ─────────────────────────────────────
  // 画布非空时确认替换；填充后清空选中（旧节点已移除）。dirty 由快照比较自动反映。
  const onStandardPipeline = useCallback(() => {
    if (
      rfNodes.length > 0 &&
      !window.confirm("标准管线将替换当前画布上的全部节点，确认继续？")
    ) {
      return
    }
    const { nodes: pn, edges: pe } = toReactFlow(standardPipeline(prompts))
    setRfNodes(pn)
    setRfEdges(pe)
    setSelectedId(null)
  }, [rfNodes.length, prompts, setRfNodes, setRfEdges])

  // ── 键盘删除（Delete / Backspace）────────────────────────
  // ReactFlow 在画布 pane 聚焦时才触发；属性面板输入框内的退格不会冒泡到这里。
  // 删节点：级联清理其关联边（边是 dependsOn 真源 → 依赖随之清理）。
  const onNodesDelete = useCallback(
    (deleted: Node[]) => {
      const ids = new Set(deleted.map((n) => n.id))
      setRfEdges((eds) =>
        (eds as RFEdge[]).filter(
          (e) => !ids.has(e.source) && !ids.has(e.target),
        ),
      )
      setSelectedId((cur) => (cur && ids.has(cur) ? null : cur))
    },
    [setRfEdges],
  )

  // ── 保存 ─────────────────────────────────────────────────
  const onSave = useCallback(() => {
    const studioNodes = toStudioNodes(rfNodes as RFNode[], rfEdges)
    const input = { name: workflowName, nodes: studioNodes }
    const done = (saved: { id: string }, created: boolean) => {
      loadedSnapshot.current = snapshotOf(workflowName, studioNodes)
      toast.success("工作流已保存")
      if (created) onCreated?.(saved.id)
    }
    const onErr = (err: unknown) => {
      if (err instanceof ApiError && err.status === 400) {
        const msg = err.body
          .replace(/^invalid workflow:\s*/i, "")
          .replace(/^custom workflow:\s*/i, "")
          .trim()
        toast.error(msg || "工作流配置无效")
        return
      }
      toast.error("保存失败")
    }
    if (workflowId) {
      updateWorkflow
        .mutateAsync({ wfId: workflowId, input })
        .then((saved) => done(saved, false))
        .catch(onErr)
    } else {
      createWorkflow
        .mutateAsync(input)
        .then((saved) => done(saved, true))
        .catch(onErr)
    }
  }, [
    rfNodes,
    rfEdges,
    workflowName,
    workflowId,
    updateWorkflow,
    createWorkflow,
    onCreated,
  ])

  const otherIds = (rfNodes as RFNode[])
    .filter((n) => n.id !== selectedId)
    .map((n) => n.id)

  return (
    <div className="flex h-full flex-col bg-bg-base">
      {/* 顶栏：返回 / 工作流名 / 保存（仅 dirty 时可点）。 */}
      <header className="flex items-center justify-between border-b border-line bg-bg-surface px-4 py-2.5">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={onBack}
            className="text-[12px] text-text-3 hover:text-text-1"
          >
            ← 返回项目
          </button>
          <span className="text-[12px] text-text-3">/</span>
          {isCreate ? (
            <input
              type="text"
              value={workflowName}
              onChange={(e) => setWorkflowName(e.target.value)}
              placeholder="工作流名称"
              aria-label="工作流名称"
              className="rounded-md border border-line bg-bg-base px-2 py-1 text-[14px] font-semibold text-text-1 placeholder:text-text-3 focus:border-amber focus:outline-none"
            />
          ) : (
            <h1 className="text-[14px] font-semibold text-text-1">
              {workflowName}
            </h1>
          )}
        </div>
        <button
          type="button"
          disabled={!dirty || saving}
          onClick={onSave}
          className="rounded-md border border-amber/30 px-3 py-1.5 text-[12px] font-medium text-amber hover:border-amber disabled:cursor-not-allowed disabled:opacity-50"
        >
          {saving ? "保存中…" : "保存"}
        </button>
      </header>

      {/* 主区：三栏。 */}
      <div className="flex min-h-0 flex-1">
        <NodePalette onStandardPipeline={onStandardPipeline} />
        <div
          className="workflow-canvas relative flex-1"
          onDragOver={onDragOver}
          onDrop={onDrop}
        >
          <ReactFlow
            nodes={rfNodes}
            edges={rfEdges}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onNodesDelete={onNodesDelete}
            onConnect={onConnect}
            onSelectionChange={onSelectionChange}
            nodeTypes={nodeTypes}
            deleteKeyCode={["Delete", "Backspace"]}
            fitView
            proOptions={{ hideAttribution: false }}
          >
            <Background />
            <Controls />
            <MiniMap
              nodeColor={(n) =>
                NODE_COLOR[(n as Node<StudioNodeData>).data.node.type] ??
                "var(--line)"
              }
            />
          </ReactFlow>
          {/* 空画布提示：引导拖拽或一键标准管线。 */}
          {rfNodes.length === 0 && (
            <div className="pointer-events-none absolute inset-0 grid place-items-center">
              <p className="rounded-md border border-line bg-bg-surface/80 px-4 py-2 text-[12.5px] text-text-3">
                拖拽左侧节点到画布，或点「标准管线」快速开始
              </p>
            </div>
          )}
        </div>
        <PropertiesPanel
          node={selected}
          prompts={prompts}
          basics={basics}
          org={org}
          otherIds={otherIds}
          onPatch={patchSelected}
          onRename={renameSelected}
          onDelete={deleteSelected}
        />
      </div>
    </div>
  )
}

export function WorkflowCanvas(props: WorkflowCanvasProps) {
  return (
    <ReactFlowProvider>
      <CanvasInner {...props} />
    </ReactFlowProvider>
  )
}
