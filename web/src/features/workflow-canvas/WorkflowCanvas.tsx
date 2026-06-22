import { useCallback, useEffect, useMemo, useRef, useState } from "react"
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
  SelectionMode,
  type Node,
  type Connection,
  type OnSelectionChangeParams,
  type OnConnectStartParams,
  type FinalConnectionState,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import { Redo2, Undo2 } from "lucide-react"
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
  nextNodeId,
  duplicateNode,
  insertNodeOnEdge,
  cloneSelection,
  getHelperLines,
  seedPositions,
  standardPipeline,
  type StudioNodeData,
  type RFNode,
  type RFEdge,
} from "./canvasModel"
import { useUndoRedo } from "./useUndoRedo"
import { WorkflowNode } from "./WorkflowNode"
import { StudioEdge } from "./StudioEdge"
import { NodeTypePicker } from "./NodeTypePicker"
import { CanvasActionsProvider } from "./CanvasActionsContext"
import { HelperLines } from "./HelperLines"
import { NodePalette, PALETTE_DND_TYPE } from "./NodePalette"
import { PropertiesPanel } from "./PropertiesPanel"
import { NODE_COLOR } from "./nodeColor"
import { RunCanvas } from "./RunCanvas"
import { ModeToggle } from "./ModeToggle"

export type CanvasMode = "edit" | "run"

// 吸附网格步长（snap-to-grid + Background 点距 共用，保证视觉对齐）。
const GRID = 16

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
  // 模式：编辑（默认）/ 运行。由路由 ?run= 派生。
  mode?: CanvasMode
  // 运行模式选中的 run（plan）id；无则显示「尚无运行」。
  runId?: string
  // 顶栏「编辑 | 运行」段切换 → 路由改 ?run=（由路由注入）。
  onModeChange?: (next: CanvasMode) => void
  // 运行选择器/运行控制切换 run → 路由 ?run=（由路由注入）。
  onSelectRun?: (runId: string) => void
}

const nodeTypes = { studio: WorkflowNode }
const edgeTypes = { studio: StudioEdge }
const defaultEdgeOptions = { type: "studio" }

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
  onModeChange,
}: WorkflowCanvasProps) {
  const initial = useMemo(() => toReactFlow(nodes), [nodes])
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState(initial.nodes)
  const [rfEdges, setRfEdges, onEdgesChange] = useEdgesState(initial.edges)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  // 名称：编辑态用既有名（只读展示），新建态可编辑（无独立名称对话框）。
  const [workflowName, setWorkflowName] = useState(initialName)
  const { screenToFlowPosition, getNodes, getEdges, fitView } =
    useReactFlow<RFNode, RFEdge>()
  const { takeSnapshot, undo, redo, canUndo, canRedo } = useUndoRedo()
  const isCreate = !workflowId

  // ── 连线交互态（Phase B）────────────────────────────────────
  // onConnectStart 记录起点节点 + 句柄类型；onConnectEnd 若落在空白且起点为
  // source 句柄，则弹出节点类型选择器（create 模式）。边上「+」走 insert 模式。
  const connectFrom = useRef<{ nodeId: string; handleType: string } | null>(null)

  // ── 剪贴板 + 对齐辅助线状态（Phase C）─────────────────────────
  // clipboard 存 Ctrl+C 时的原始选区（节点 + 其内部边）；offset/重键延迟到粘贴时做。
  const clipboard = useRef<{ nodes: RFNode[]; edges: RFEdge[] } | null>(null)
  // helperLines 仅持有当前要画的引导线（flow 坐标）；拖动结束清空。
  const [helperLines, setHelperLines] = useState<{
    horizontal?: number
    vertical?: number
  }>({})
  const [picker, setPicker] = useState<
    | { mode: "create"; screenX: number; screenY: number; flow: { x: number; y: number }; source: string }
    | { mode: "insert"; screenX: number; screenY: number; flow: { x: number; y: number }; edgeId: string }
    | null
  >(null)

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
      takeSnapshot()
      const pos = screenToFlowPosition({ x: e.clientX, y: e.clientY })
      setRfNodes((nds) => addNodeAt(nds as RFNode[], type, pos, prompts))
    },
    [screenToFlowPosition, setRfNodes, prompts, takeSnapshot],
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
      // 仅在确实落边时记快照（被环路守卫拒绝的连接不应产生空撤销步）。
      takeSnapshot()
      setRfEdges((eds) =>
        addEdge(
          { ...conn, id: `${conn.source}->${conn.target}`, type: "studio" },
          eds,
        ),
      )
    },
    [rfNodes, rfEdges, setRfEdges, takeSnapshot],
  )

  // ── 从句柄拖到空白 → 弹选择器 → 新建并连接（Phase B）──────────
  const onConnectStart = useCallback(
    (_: unknown, { nodeId, handleType }: OnConnectStartParams) => {
      connectFrom.current = nodeId
        ? { nodeId, handleType: handleType ?? "source" }
        : null
    },
    [],
  )

  const onConnectEnd = useCallback(
    (event: MouseEvent | TouchEvent, connectionState: FinalConnectionState) => {
      // v12：toNode == null 表示落在空白 pane（未连到任何节点）。
      const droppedOnPane = connectionState.toNode == null
      const from = connectFrom.current
      if (droppedOnPane && from?.handleType === "source") {
        const { clientX, clientY } =
          "changedTouches" in event ? event.changedTouches[0] : event
        const flow = screenToFlowPosition({ x: clientX, y: clientY })
        setPicker({
          mode: "create",
          screenX: clientX,
          screenY: clientY,
          flow,
          source: from.nodeId,
        })
      }
      connectFrom.current = null
    },
    [screenToFlowPosition],
  )

  // 选择器选中类型后落地：create 模式新建节点 + 连边；insert 模式在边上拆分。
  const onPickType = useCallback(
    (type: string) => {
      if (!picker) return
      if (picker.mode === "create") {
        const id = nextNodeId(getNodes())
        // 一致性环路守卫：与全新节点不可能成环，但保留 pattern。
        const candidate = toStudioNodes(
          addNodeAt(getNodes(), type, picker.flow, prompts, id),
          [
            ...getEdges(),
            { id: `${picker.source}->${id}`, source: picker.source, target: id },
          ],
        )
        const err = findGraphError(candidate)
        if (err) {
          toast.error(err)
          setPicker(null)
          return
        }
        takeSnapshot()
        setRfNodes((nds) => addNodeAt(nds as RFNode[], type, picker.flow, prompts, id))
        setRfEdges((eds) => [
          ...eds,
          {
            id: `${picker.source}->${id}`,
            source: picker.source,
            target: id,
            type: "studio",
          },
        ])
      } else {
        // insert 模式：在边 A->B 上插入新节点 N（A->N, N->B）。
        const candidate = insertNodeOnEdge(
          getNodes(),
          getEdges(),
          picker.edgeId,
          type,
          picker.flow,
          prompts,
        )
        const err = findGraphError(toStudioNodes(candidate.nodes, candidate.edges))
        if (err) {
          toast.error(err)
          setPicker(null)
          return
        }
        takeSnapshot()
        setRfNodes(candidate.nodes)
        setRfEdges(candidate.edges)
      }
      setPicker(null)
    },
    [picker, getNodes, getEdges, prompts, setRfNodes, setRfEdges, takeSnapshot],
  )

  // ── 属性面板回调 ─────────────────────────────────────────
  const patchSelected = useCallback(
    (patch: Partial<WorkflowNodeType>) => {
      if (!selectedId) return
      takeSnapshot()
      setRfNodes((nds) =>
        (nds as RFNode[]).map((n) =>
          n.id === selectedId
            ? { ...n, data: { ...n.data, node: { ...n.data.node, ...patch } } }
            : n,
        ),
      )
    },
    [selectedId, setRfNodes, takeSnapshot],
  )

  // id 重命名：替换 RF 节点 id + data.node.id，并重键所有 source/target===oldId 的边
  //（边是 dependsOn 真源，故重键边即级联更新依赖）。
  const renameSelected = useCallback(
    (newId: string) => {
      const oldId = selectedId
      if (!oldId || newId === oldId) return
      takeSnapshot()
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
    [selectedId, setRfNodes, setRfEdges, takeSnapshot],
  )

  // 属性面板「删除节点」：程序化删除不会触发 <ReactFlow onBeforeDelete>，故此处显式记快照。
  const deleteSelected = useCallback(() => {
    const id = selectedId
    if (!id) return
    takeSnapshot()
    setRfNodes((nds) => (nds as RFNode[]).filter((n) => n.id !== id))
    setRfEdges((eds) => eds.filter((e) => e.source !== id && e.target !== id))
    setSelectedId(null)
  }, [selectedId, setRfNodes, setRfEdges, takeSnapshot])

  // ── 节点工具条 / 边控件回调（Phase B，经 CanvasActionsContext 下发）──────
  const onDuplicateNode = useCallback(
    (id: string) => {
      takeSnapshot()
      setRfNodes((nds) => duplicateNode(nds as RFNode[], id, prompts).nodes)
    },
    [setRfNodes, prompts, takeSnapshot],
  )

  // 工具条「删除节点」：程序化删除不触发 onBeforeDelete，故显式记快照 + 级联清边。
  const onDeleteNode = useCallback(
    (id: string) => {
      takeSnapshot()
      setRfNodes((nds) => (nds as RFNode[]).filter((n) => n.id !== id))
      setRfEdges((eds) => eds.filter((e) => e.source !== id && e.target !== id))
      setSelectedId((cur) => (cur === id ? null : cur))
    },
    [setRfNodes, setRfEdges, takeSnapshot],
  )

  const onDeleteEdge = useCallback(
    (id: string) => {
      takeSnapshot()
      setRfEdges((eds) => eds.filter((e) => e.id !== id))
    },
    [setRfEdges, takeSnapshot],
  )

  const onInsertOnEdge = useCallback(
    (id: string, screenX: number, screenY: number) => {
      setPicker({
        mode: "insert",
        screenX,
        screenY,
        flow: screenToFlowPosition({ x: screenX, y: screenY }),
        edgeId: id,
      })
    },
    [screenToFlowPosition],
  )

  const canvasActions = useMemo(
    () => ({ onDuplicateNode, onDeleteNode, onDeleteEdge, onInsertOnEdge }),
    [onDuplicateNode, onDeleteNode, onDeleteEdge, onInsertOnEdge],
  )

  // ── 标准管线一键填充 ─────────────────────────────────────
  // 画布非空时确认替换；填充后清空选中（旧节点已移除）。dirty 由快照比较自动反映。
  const onStandardPipeline = useCallback(() => {
    if (
      rfNodes.length > 0 &&
      !window.confirm("标准管线将替换当前画布上的全部节点，确认继续？")
    ) {
      return
    }
    takeSnapshot()
    const { nodes: pn, edges: pe } = toReactFlow(standardPipeline(prompts))
    setRfNodes(pn)
    setRfEdges(pe)
    setSelectedId(null)
  }, [rfNodes.length, prompts, setRfNodes, setRfEdges, takeSnapshot])

  // ── 自动整理 ─────────────────────────────────────────────
  // 按当前 EDGES 推导的依赖跑分层种子布局，仅覆盖 position（保留节点其余字段）；
  // 排完后 fitView 居中。可撤销（先记快照）。
  const onAutoTidy = useCallback(() => {
    takeSnapshot()
    const studio = toStudioNodes(getNodes(), getEdges())
    const seeded = seedPositions(studio)
    setRfNodes((nds) =>
      (nds as RFNode[]).map((n) => ({
        ...n,
        position: seeded.get(n.id) ?? n.position,
      })),
    )
    setTimeout(() => fitView({ duration: 300 }), 0)
  }, [getNodes, getEdges, setRfNodes, fitView, takeSnapshot])

  // ── 对齐辅助线（C3）─────────────────────────────────────────
  // 拖动中实时算引导线：与其它节点的边/中心落在阈值内则画线 + 吸附被拖节点位置。
  // 不在此记快照（onNodeDragStart 已记）；拖动结束清空引导线。
  const onNodeDrag = useCallback(
    (_: unknown, node: Node) => {
      const dragged = node as RFNode
      const l = getHelperLines(
        dragged,
        getNodes().filter((n) => n.id !== node.id),
      )
      setHelperLines({ horizontal: l.horizontal, vertical: l.vertical })
      if (l.snapX != null || l.snapY != null) {
        setRfNodes((nds) =>
          (nds as RFNode[]).map((n) =>
            n.id === node.id
              ? {
                  ...n,
                  position: {
                    x: l.snapX ?? n.position.x,
                    y: l.snapY ?? n.position.y,
                  },
                }
              : n,
          ),
        )
      }
    },
    [getNodes, setRfNodes],
  )

  const onNodeDragStop = useCallback(() => setHelperLines({}), [])

  // ── 撤销/重做键盘快捷键 ───────────────────────────────────
  // Ctrl/Cmd+Z → undo；Ctrl/Cmd+Shift+Z 或 Ctrl+Y → redo。
  // 编辑输入框（名称/提示词）时不劫持，让原生编辑撤销生效。
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const el = document.activeElement as HTMLElement | null
      const tag = el?.tagName
      const editing =
        tag === "INPUT" ||
        tag === "TEXTAREA" ||
        el?.isContentEditable === true
      if (editing) return
      if (!(e.metaKey || e.ctrlKey)) return
      const key = e.key.toLowerCase()
      if (key === "z" && !e.shiftKey) {
        e.preventDefault()
        undo()
      } else if ((key === "z" && e.shiftKey) || key === "y") {
        e.preventDefault()
        redo()
      } else if (key === "c") {
        // 复制：暂存当前选区（原始节点 + 其内部边）；offset/重键留给粘贴。
        const selNodes = getNodes().filter((n) => n.selected)
        if (selNodes.length === 0) return
        e.preventDefault()
        const ids = new Set(selNodes.map((n) => n.id))
        const selEdges = getEdges().filter(
          (ed) => ids.has(ed.source) && ids.has(ed.target),
        )
        clipboard.current = { nodes: selNodes, edges: selEdges as RFEdge[] }
      } else if (key === "v") {
        // 粘贴：按当前画布 id 重新分配 id（避让现有），整体偏移 +32/+32，选中克隆。
        if (!clipboard.current) return
        e.preventDefault()
        const clip = clipboard.current
        takeSnapshot()
        const { nodes: cloned, edges: clonedEdges } = cloneSelection(
          clip.nodes,
          clip.edges,
          new Set(clip.nodes.map((n) => n.id)),
          { x: 32, y: 32 },
          prompts,
          getNodes(),
        )
        setRfNodes((nds) => [
          ...(nds as RFNode[]).map((n) => ({ ...n, selected: false })),
          ...cloned,
        ])
        setRfEdges((eds) => [...eds, ...clonedEdges])
      } else if (key === "d") {
        // 原地复制：等同于「复制+粘贴」一步完成，不写剪贴板。
        const selNodes = getNodes().filter((n) => n.selected)
        if (selNodes.length === 0) return
        e.preventDefault()
        const ids = new Set(selNodes.map((n) => n.id))
        const selEdges = getEdges().filter(
          (ed) => ids.has(ed.source) && ids.has(ed.target),
        )
        takeSnapshot()
        const { nodes: cloned, edges: clonedEdges } = cloneSelection(
          selNodes,
          selEdges as RFEdge[],
          ids,
          { x: 32, y: 32 },
          prompts,
          getNodes(),
        )
        setRfNodes((nds) => [
          ...(nds as RFNode[]).map((n) => ({ ...n, selected: false })),
          ...cloned,
        ])
        setRfEdges((eds) => [...eds, ...clonedEdges])
      }
    }
    document.addEventListener("keydown", onKeyDown)
    return () => document.removeEventListener("keydown", onKeyDown)
  }, [undo, redo, getNodes, getEdges, setRfNodes, setRfEdges, prompts, takeSnapshot])

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
          {/* 编辑 | 运行 模式切换（新建态隐藏：尚无可运行的 workflow）。 */}
          {!isCreate && onModeChange && (
            <ModeToggle mode="edit" onChange={onModeChange} />
          )}
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            aria-label="撤销"
            title="撤销 (Ctrl/Cmd+Z)"
            disabled={!canUndo}
            onClick={undo}
            className="text-text-3 hover:text-text-1 disabled:cursor-not-allowed disabled:opacity-40"
          >
            <Undo2 className="h-4 w-4" aria-hidden />
          </button>
          <button
            type="button"
            aria-label="重做"
            title="重做 (Ctrl/Cmd+Shift+Z)"
            disabled={!canRedo}
            onClick={redo}
            className="text-text-3 hover:text-text-1 disabled:cursor-not-allowed disabled:opacity-40"
          >
            <Redo2 className="h-4 w-4" aria-hidden />
          </button>
          <button
            type="button"
            disabled={!dirty || saving}
            onClick={onSave}
            className="rounded-md border border-amber/30 px-3 py-1.5 text-[12px] font-medium text-amber hover:border-amber disabled:cursor-not-allowed disabled:opacity-50"
          >
            {saving ? "保存中…" : "保存"}
          </button>
        </div>
      </header>

      {/* 主区：三栏。 */}
      <div className="flex min-h-0 flex-1">
        <NodePalette
          onStandardPipeline={onStandardPipeline}
          onAutoTidy={onAutoTidy}
        />
        <div
          className="workflow-canvas relative flex-1"
          onDragOver={onDragOver}
          onDrop={onDrop}
        >
          <CanvasActionsProvider value={canvasActions}>
            <ReactFlow
              nodes={rfNodes}
              edges={rfEdges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onNodesDelete={onNodesDelete}
              onConnect={onConnect}
              onConnectStart={onConnectStart}
              onConnectEnd={onConnectEnd}
              onSelectionChange={onSelectionChange}
              onNodeDragStart={() => takeSnapshot()}
              onNodeDrag={onNodeDrag}
              onNodeDragStop={onNodeDragStop}
              onBeforeDelete={async () => {
                // 键盘删除节点/边在应用前记快照（覆盖 Delete/Backspace 路径）。
                takeSnapshot()
                return true
              }}
              nodeTypes={nodeTypes}
              edgeTypes={edgeTypes}
              defaultEdgeOptions={defaultEdgeOptions}
              deleteKeyCode={["Delete", "Backspace"]}
              selectionOnDrag
              panOnDrag={[1, 2]}
              selectionMode={SelectionMode.Partial}
              snapToGrid
              snapGrid={[GRID, GRID]}
              fitView
              proOptions={{ hideAttribution: false }}
            >
              <Background gap={GRID} />
              <Controls />
              <MiniMap
                nodeColor={(n) =>
                  NODE_COLOR[(n as Node<StudioNodeData>).data.node.type] ??
                  "var(--line)"
                }
              />
            </ReactFlow>
            {/* 对齐辅助线覆盖层（C3）：读视口 transform 在屏幕空间画引导线。 */}
            <HelperLines {...helperLines} />
          </CanvasActionsProvider>
          {/* 空画布提示：引导拖拽或一键标准管线，并说明框选/平移交互。 */}
          {rfNodes.length === 0 && (
            <div className="pointer-events-none absolute inset-0 grid place-items-center">
              <p className="rounded-md border border-line bg-bg-surface/80 px-4 py-2 text-center text-[12.5px] text-text-3">
                拖拽左侧节点到画布，或点「标准管线」快速开始
                <br />
                <span className="text-[11px] text-text-3">
                  左键框选，中键/右键平移
                </span>
              </p>
            </div>
          )}
          <NodeTypePicker
            open={!!picker}
            screenX={picker?.screenX ?? 0}
            screenY={picker?.screenY ?? 0}
            onPick={onPickType}
            onClose={() => setPicker(null)}
          />
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

// 运行模式壳：顶栏（返回 / 名 / 编辑|运行 切换）+ 只读运行画布。
// 编辑态的所有 hook/handler/面板都在 CanvasInner 内，运行态走独立子树（避免条件 hook）。
function RunShell({
  workflowName,
  projectId,
  org,
  nodes,
  runId,
  onBack,
  onModeChange,
  onSelectRun,
}: WorkflowCanvasProps) {
  return (
    <div className="flex h-full flex-col bg-bg-base">
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
          <h1 className="text-[14px] font-semibold text-text-1">{workflowName}</h1>
          {onModeChange && <ModeToggle mode="run" onChange={onModeChange} />}
        </div>
      </header>
      <RunCanvas
        projectId={projectId}
        org={org}
        runId={runId}
        nodes={nodes}
        onSelectRun={(rid) => onSelectRun?.(rid)}
      />
    </div>
  )
}

export function WorkflowCanvas(props: WorkflowCanvasProps) {
  if (props.mode === "run") {
    return <RunShell {...props} />
  }
  return (
    <ReactFlowProvider>
      <CanvasInner {...props} />
    </ReactFlowProvider>
  )
}
