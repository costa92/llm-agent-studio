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
  type Edge,
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
  directUpstreamIds,
  addNodeAt,
  reconnectEdge,
  duplicateNode,
  insertNodeOnEdge,
  cloneSelection,
  getHelperLines,
  seedPositions,
  standardPipeline,
  createNode,
  collectCustomTypes,
  applyTypeDisplay,
  hasUnboundCustomNode,
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
import { NodePalette, PALETTE_DND_TYPE, PALETTE_DND_TYPEID } from "./NodePalette"
import { PropertiesPanel } from "./PropertiesPanel"
import { InputsSchemaPanel } from "./InputsSchemaPanel"
import { useNodeTypes, useNodeTypesExprChannel } from "./api"
import { NODE_COLOR, isCustomType, slugify, descTypeFor } from "./nodeColor"
import { RunCanvas } from "./RunCanvas"
import { ModeToggle } from "./ModeToggle"
import { CanvasContextMenu, type ContextMenuItem } from "./CanvasContextMenu"
import { CustomTypeDialog, type CustomTypePayload } from "./CustomTypeDialog"
import { useCustomNodeTypes, useCreateCustomNodeType } from "@/features/custom-node-types/api"
import { TypeDialog } from "@/features/custom-node-types/TypeDialog"
import { type FormDraft, type NodeKind } from "@/features/custom-node-types/typeDraft"
import { useOrgSecrets } from "@/features/org-secrets/api"
import { useOrgTextModels } from "@/features/cost/api"
import { useRole } from "@/app/rbac"
import type {
  CustomNodeType,
  HttpParams,
  InputField,
  LlmParams,
  ScriptParams,
  UpsertCustomNodeTypeInput,
} from "@/lib/types"

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
  // 运行期输入声明（设计期编辑；新建/旧工作流缺省 = []）。
  inputsSchema?: InputField[]
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

// 保存载荷的稳定签名（name + studio nodes + 输入 schema）。用于 dirty 比较——
// 编辑输入 schema 同样要让「保存」可点，故纳入签名。
function snapshotOf(
  name: string,
  studioNodes: WorkflowNodeType[],
  inputsSchema: InputField[],
): string {
  return JSON.stringify({ name, nodes: studioNodes, inputsSchema })
}

function CanvasInner({
  workflowId,
  projectId,
  org,
  workflowName: initialName,
  nodes,
  inputsSchema: initialInputsSchema,
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
  // 运行期输入 schema（设计期编辑）。右栏在「节点属性 / 工作流输入」间切换。
  const [inputsSchema, setInputsSchema] = useState<InputField[]>(
    initialInputsSchema ?? [],
  )
  const [rightPanel, setRightPanel] = useState<"props" | "inputs">("props")
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
    | { mode: "create"; screenX: number; screenY: number; flow: { x: number; y: number }; source?: string }
    | { mode: "insert"; screenX: number; screenY: number; flow: { x: number; y: number }; edgeId: string }
    | null
  >(null)
  // org 级 typed 自定义节点类型（注册表）。
  const { data: orgTypedTypes = [] } = useCustomNodeTypes(org)

  // 合并画布 annotation 类型（来自 collectCustomTypes）+ org 注册表 typed 类型。
  // typed 类型带 typeId（= org registry id），区分于 annotation（无 typeId）。
  const customTypes = useMemo(() => {
    // Only feed annotation nodes (no typeId) into collectCustomTypes — typed nodes
    // (typeId set) are already represented by their org-registry entry and must NOT
    // shadow it. If they were included, placing one typed node would replace the
    // registry entry's typeId with undefined, so re-dragging the slug creates a
    // non-runnable annotation node instead of a typed one (Important 3).
    const annotationOnly = (rfNodes as RFNode[]).filter((n) => !n.data.node.typeId)
    const annotation = collectCustomTypes(annotationOnly)
    const typed = orgTypedTypes.map((ct) => ({
      type: `custom:${ct.slug}`,
      label: ct.label,
      color: ct.color,
      typeId: ct.id,
    }))
    // annotation 类型以 type 去重；typed 类型优先（registry entry 始终覆盖 annotation）。
    const allAnnotationTypes = new Set(annotation.map((a) => a.type))
    const mergedTyped = typed.filter((t) => !allAnnotationTypes.has(t.type))
    return [...annotation, ...mergedTyped]
  }, [rfNodes, orgTypedTypes])

  // typeId → 注册表条目的快速查找表（PropertiesPanel 用于解析 {{name}} 模板 + 摘要）。
  // 携带完整条目（含 kind），调用方据 kind 取 LlmParams / HttpParams / ScriptParams。
  const typedTypeById = useMemo(() => {
    const m = new Map<string, CustomNodeType>()
    for (const ct of orgTypedTypes) {
      m.set(ct.id, ct)
    }
    return m
  }, [orgTypedTypes])
  const [typeDialog, setTypeDialog] = useState<
    | { mode: "create" }
    | { mode: "edit"; type: string; initial: CustomTypePayload }
    | null
  >(null)
  // 快建可运行类型（llm/http/script）：打开类型化创建对话框（TypeDialog）预置 kind。
  // 保存经注册表创建 → useCustomNodeTypes 失效 → 新 typed chip 自动出现，用户再拖入画布。
  // 注：这里走的是与「自定义节点」管理页相同的注册表路径——绝不创建 bare 泛型节点
  // （type:"llm"/"http"/"script" + 空 typeId 会绕过 S-1 危险参数过滤链）。
  const [quickCreate, setQuickCreate] = useState<NodeKind | null>(null)
  const [quickSubmitting, setQuickSubmitting] = useState(false)
  const [quickSubmitError, setQuickSubmitError] = useState<string | null>(null)
  const createTypedType = useCreateCustomNodeType(org)
  // http secret-bearing 类型守卫：密钥名（「插入密钥」下拉 + 判定）+ admin 角色（前端镜像，后端权威）。
  const secretsQuery = useOrgSecrets(org)
  const secretNames = useMemo(
    () => (secretsQuery.data ?? []).map((s) => s.name),
    [secretsQuery.data],
  )
  // org 文本模型 → 快建 llm 类型对话框的模型下拉（与画布/管理页同源同形）。
  const textModelsQuery = useOrgTextModels(org)
  const modelOptions = useMemo(
    () => (textModelsQuery.data ?? []).map((m) => ({ value: m.model, label: `${m.provider} · ${m.model}` })),
    [textModelsQuery.data],
  )
  const { isAdmin } = useRole(org)

  const onQuickCreateSubmit = useCallback(
    (draft: FormDraft) => {
      const input: UpsertCustomNodeTypeInput = {
        label: draft.label.trim(),
        color: draft.color,
        kind: draft.kind,
        params: draft.params,
      }
      setQuickSubmitting(true)
      setQuickSubmitError(null)
      createTypedType
        .mutateAsync(input)
        .then(() => {
          // onSuccess（api 层）已失效 useCustomNodeTypes → 新 typed chip 自动出现。
          setQuickCreate(null)
          toast.success("自定义节点类型已创建，可从面板拖入画布")
        })
        .catch((err: unknown) => {
          // 优雅呈现冲突/权限错误（对话框内联，不 dead-end）。
          if (err instanceof ApiError && err.status === 409) {
            setQuickSubmitError("名称或 slug 已被占用，请使用其他名称")
          } else if (err instanceof ApiError && err.status === 403) {
            setQuickSubmitError("引用了密钥的 HTTP 类型需要管理员权限")
          } else {
            setQuickSubmitError(err instanceof Error ? err.message : "创建失败，请重试")
          }
        })
        .finally(() => setQuickSubmitting(false))
    },
    [createTypedType],
  )
  // 右键上下文菜单态（Phase D）：kind 决定菜单项；targetId 为节点/边 id。
  const [menu, setMenu] = useState<
    | { kind: "pane"; screenX: number; screenY: number; canPaste: boolean }
    | { kind: "node"; screenX: number; screenY: number; targetId: string }
    | { kind: "edge"; screenX: number; screenY: number; targetId: string }
    | null
  >(null)

  // 载入时的 studio-model 快照，作为 dirty 比较基线。
  const loadedSnapshot = useRef(
    snapshotOf(
      initialName,
      toStudioNodes(initial.nodes, initial.edges),
      initialInputsSchema ?? [],
    ),
  )

  const createWorkflow = useCreateWorkflow(projectId)
  const updateWorkflow = useUpdateWorkflow(projectId)

  const currentSnapshot = snapshotOf(
    workflowName,
    toStudioNodes(rfNodes as RFNode[], rfEdges),
    inputsSchema,
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

  // 选中节点对应的 NodeTypeDescription（org 注册表）。供 PropertiesPanel 以只读
  // <PropertiesForm> 渲染类型参数摘要；缺省（无匹配）时面板回退到手写摘要。
  // 注意：勿用 `nodeTypes` 命名——会遮蔽模块级 `nodeTypes`（ReactFlow 的
  // 节点类型→组件映射，line ~101 + <ReactFlow nodeTypes={…}>），导致画布回退到
  // 默认节点、自定义 WorkflowNode 不挂载。
  const { data: nodeTypeDescs = [] } = useNodeTypes(org)
  // P5：ExprChannel 能力旗标（同一 /node-types 响应），用于 capability-gate 字段选择器。
  const exprChannel = useNodeTypesExprChannel(org)
  const nodeDesc = selected
    ? nodeTypeDescs.find((d) => d.type === selected.type)
    : undefined

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
      // typed 节点从 dataTransfer 读 typeId（palette chip 写入 PALETTE_DND_TYPEID）。
      const droppedTypeId = e.dataTransfer.getData(PALETTE_DND_TYPEID) || undefined
      const displayBase = isCustomType(type)
        ? customTypes.find((c) => c.type === type)
        : undefined
      const display = displayBase
        ? { ...displayBase, ...(droppedTypeId ? { typeId: droppedTypeId } : {}) }
        : undefined
      setRfNodes((nds) => addNodeAt(nds as RFNode[], type, pos, prompts, undefined, display))
    },
    [screenToFlowPosition, setRfNodes, prompts, takeSnapshot, customTypes],
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

  // ── 连线重连（Phase D）──────────────────────────────────────
  // 拖动已有边的端点到新节点：建候选图（去旧边+加新边）跑环守卫；通过则重键。
  // 拖到空白处时 ReactFlow 不触发 onReconnect → 边自动还原（不删，删除走 ×/Delete）。
  const onReconnect = useCallback(
    (oldEdge: Edge, conn: Connection) => {
      if (!conn.source || !conn.target) return
      const next = reconnectEdge(rfEdges, oldEdge.id, {
        source: conn.source,
        target: conn.target,
      })
      const err = findGraphError(toStudioNodes(rfNodes as RFNode[], next))
      if (err) {
        toast.error(err)
        return
      }
      takeSnapshot()
      setRfEdges(next)
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
  // display.typeId 非空 = typed 节点（org 注册表条目），写入节点实例。
  const onPickType = useCallback(
    (type: string, display?: { label: string; color: string; typeId?: string }) => {
      if (!picker) return
      if (picker.mode === "create") {
        const built = createNode(
          getNodes(),
          getEdges(),
          type,
          picker.flow,
          prompts,
          picker.source,
          display,
        )
        const err = findGraphError(toStudioNodes(built.nodes, built.edges))
        if (err) {
          toast.error(err)
          setPicker(null)
          return
        }
        takeSnapshot()
        setRfNodes(built.nodes)
        setRfEdges(built.edges)
      } else {
        // insert 模式：在边 A->B 上插入新节点 N（A->N, N->B）。
        const candidate = insertNodeOnEdge(
          getNodes(),
          getEdges(),
          picker.edgeId,
          type,
          picker.flow,
          prompts,
          display,
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

  const onCreateCustomType = useCallback(
    (p: CustomTypePayload) => {
      const base = `custom:${slugify(p.label)}`
      const existing = new Set(collectCustomTypes(getNodes()).map((c) => c.type))
      let type = base
      let i = 2
      while (existing.has(type)) { type = `${base}-${i}`; i += 1 }
      takeSnapshot()
      const pos = screenToFlowPosition({ x: 300, y: 200 })
      setRfNodes((nds) => createNode(nds as RFNode[], [], type, pos, prompts, undefined, p).nodes)
      setTypeDialog(null)
    },
    [getNodes, prompts, screenToFlowPosition, setRfNodes, takeSnapshot],
  )

  const onEditCustomTypeSubmit = useCallback(
    (p: CustomTypePayload) => {
      if (typeDialog?.mode !== "edit") return
      takeSnapshot()
      setRfNodes((nds) => applyTypeDisplay(nds as RFNode[], typeDialog.type, p.label, p.color))
      setTypeDialog(null)
    },
    [typeDialog, setRfNodes, takeSnapshot],
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

  // 节点尾部「+」快加（Phase D）：新节点落在 source 节点正下方（版式整洁，不取光标），
  // 选择器浮层落在点击 screen 坐标；选中类型后走 create(source=nodeId)。
  const onQuickAddFrom = useCallback(
    (nodeId: string, screenX: number, screenY: number) => {
      const src = getNodes().find((n) => n.id === nodeId)
      const flow = src
        ? { x: src.position.x, y: src.position.y + 120 }
        : screenToFlowPosition({ x: screenX, y: screenY })
      setPicker({ mode: "create", screenX, screenY, flow, source: nodeId })
    },
    [getNodes, screenToFlowPosition],
  )

  // 粘贴（Phase D 抽取）：at 给定时把克隆整体平移到该 flow 落点（右键菜单用），
  // 否则沿用 +32/+32（键盘 ⌘V 用）。克隆 fresh id + remap 内部边。
  const doPaste = useCallback(
    (at?: { x: number; y: number }) => {
      const clip = clipboard.current
      if (!clip || clip.nodes.length === 0) return
      let offset = { x: 32, y: 32 }
      if (at) {
        const minX = Math.min(...clip.nodes.map((n) => n.position.x))
        const minY = Math.min(...clip.nodes.map((n) => n.position.y))
        offset = { x: at.x - minX, y: at.y - minY }
      }
      takeSnapshot()
      const { nodes: cloned, edges: clonedEdges } = cloneSelection(
        clip.nodes,
        clip.edges,
        new Set(clip.nodes.map((n) => n.id)),
        offset,
        prompts,
        getNodes(),
      )
      setRfNodes((nds) => [
        ...(nds as RFNode[]).map((n) => ({ ...n, selected: false })),
        ...cloned,
      ])
      setRfEdges((eds) => [...eds, ...clonedEdges])
    },
    [prompts, getNodes, setRfNodes, setRfEdges, takeSnapshot],
  )

  // 全选（Phase D 抽取）：键盘 ⌘A / 右键菜单 共用。
  const selectAll = useCallback(() => {
    setRfNodes((nds) => (nds as RFNode[]).map((n) => ({ ...n, selected: true })))
  }, [setRfNodes])

  const canvasActions = useMemo(
    () => ({ onDuplicateNode, onDeleteNode, onDeleteEdge, onInsertOnEdge, onQuickAddFrom }),
    [onDuplicateNode, onDeleteNode, onDeleteEdge, onInsertOnEdge, onQuickAddFrom],
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

  // 按右键目标构建菜单项（复用既有 handler / doPaste / selectAll / fitView / 泛化 picker）。
  const menuItems = useMemo<ContextMenuItem[]>(() => {
    if (!menu) return []
    if (menu.kind === "pane") {
      const flow = screenToFlowPosition({ x: menu.screenX, y: menu.screenY })
      return [
        {
          label: "添加节点",
          onClick: () =>
            setPicker({ mode: "create", screenX: menu.screenX, screenY: menu.screenY, flow }),
        },
        {
          label: "粘贴",
          disabled: !menu.canPaste,
          onClick: () => doPaste(flow),
        },
        { label: "全选", onClick: selectAll },
        { label: "自动整理", onClick: onAutoTidy },
        { label: "适应视图", onClick: () => fitView({ duration: 300 }) },
      ]
    }
    if (menu.kind === "node") {
      return [
        { label: "复制", onClick: () => onDuplicateNode(menu.targetId) },
        {
          label: "从此添加下游",
          onClick: () => onQuickAddFrom(menu.targetId, menu.screenX, menu.screenY),
        },
        { label: "删除", danger: true, onClick: () => onDeleteNode(menu.targetId) },
      ]
    }
    return [
      {
        label: "插入节点",
        onClick: () => onInsertOnEdge(menu.targetId, menu.screenX, menu.screenY),
      },
      { label: "删除", danger: true, onClick: () => onDeleteEdge(menu.targetId) },
    ]
  }, [
    menu,
    screenToFlowPosition,
    doPaste,
    selectAll,
    onAutoTidy,
    fitView,
    onDuplicateNode,
    onQuickAddFrom,
    onDeleteNode,
    onInsertOnEdge,
    onDeleteEdge,
  ])

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
        if (!clipboard.current) return
        e.preventDefault()
        doPaste()
      } else if (key === "a") {
        e.preventDefault()
        selectAll()
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
  }, [undo, redo, getNodes, getEdges, setRfNodes, setRfEdges, prompts, takeSnapshot, doPaste, selectAll])

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
    const input = { name: workflowName, nodes: studioNodes, inputsSchema }
    const done = (saved: { id: string }, created: boolean) => {
      loadedSnapshot.current = snapshotOf(workflowName, studioNodes, inputsSchema)
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
    inputsSchema,
    workflowId,
    updateWorkflow,
    createWorkflow,
    onCreated,
  ])

  const runDisabled = useMemo(() => hasUnboundCustomNode(rfNodes as RFNode[]), [rfNodes])

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
            runDisabled ? (
              <span className="text-[12px] text-text-3" title="含未绑定类型的自定义节点 · 暂不支持运行">
                含未绑定类型的自定义节点 · 暂不支持运行
              </span>
            ) : (
              <ModeToggle mode="edit" onChange={onModeChange} />
            )
          )}
        </div>
        <div className="flex items-center gap-2">
          {/* 右栏切换：节点属性 / 工作流输入（设计期输入 schema 编辑器）。 */}
          <div className="flex overflow-hidden rounded-md border border-line text-[12px]">
            <button
              type="button"
              onClick={() => setRightPanel("props")}
              className={
                rightPanel === "props"
                  ? "bg-bg-base px-2.5 py-1 text-text-1"
                  : "px-2.5 py-1 text-text-3 hover:text-text-1"
              }
            >
              节点属性
            </button>
            <button
              type="button"
              onClick={() => setRightPanel("inputs")}
              className={
                rightPanel === "inputs"
                  ? "bg-bg-base px-2.5 py-1 text-text-1"
                  : "px-2.5 py-1 text-text-3 hover:text-text-1"
              }
            >
              工作流输入
            </button>
          </div>
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
          customTypes={customTypes}
          onQuickCreate={(kind) => { setQuickSubmitError(null); setQuickCreate(kind) }}
          onAddCustomType={() => setTypeDialog({ mode: "create" })}
          onEditCustomType={(type) => { const c = customTypes.find((x) => x.type === type); if (c) setTypeDialog({ mode: "edit", type, initial: { label: c.label, color: c.color } }) }}
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
              onReconnect={onReconnect}
              onConnectStart={onConnectStart}
              onConnectEnd={onConnectEnd}
              onSelectionChange={onSelectionChange}
              onPaneContextMenu={(e) => {
                e.preventDefault()
                const ev = e as MouseEvent
                setMenu({
                  kind: "pane",
                  screenX: ev.clientX,
                  screenY: ev.clientY,
                  canPaste: !!clipboard.current,
                })
              }}
              onNodeContextMenu={(e, node) => {
                e.preventDefault()
                setMenu({ kind: "node", screenX: e.clientX, screenY: e.clientY, targetId: node.id })
              }}
              onEdgeContextMenu={(e, edge) => {
                e.preventDefault()
                setMenu({ kind: "edge", screenX: e.clientX, screenY: e.clientY, targetId: edge.id })
              }}
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
              panOnDrag
              selectionKeyCode="Shift"
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
                  左键拖拽平移，Shift+拖拽框选，右键打开菜单
                </span>
              </p>
            </div>
          )}
          <NodeTypePicker
            open={!!picker}
            screenX={picker?.screenX ?? 0}
            screenY={picker?.screenY ?? 0}
            customTypes={customTypes}
            onPick={onPickType}
            onClose={() => setPicker(null)}
          />
          <CanvasContextMenu
            open={!!menu}
            screenX={menu?.screenX ?? 0}
            screenY={menu?.screenY ?? 0}
            items={menuItems}
            onClose={() => setMenu(null)}
          />
          {/* 条件渲染：每次打开都重新挂载，使 CustomTypeDialog 的 useState 从最新
              initial 重新初始化（避免复用上一次的 label/color 残留）。 */}
          {typeDialog && (
            <CustomTypeDialog
              open
              mode={typeDialog.mode}
              initial={typeDialog.mode === "edit" ? typeDialog.initial : undefined}
              onSubmit={typeDialog.mode === "edit" ? onEditCustomTypeSubmit : onCreateCustomType}
              onCancel={() => setTypeDialog(null)}
            />
          )}
          {/* 快建可运行类型对话框（注册表化的 typed 创建，预置 kind）。每次打开都以
              key=kind 重新挂载，确保 TypeDialog 的 useState 从最新 initialKind 初始化。 */}
          {quickCreate && (
            <TypeDialog
              key={quickCreate}
              open
              mode="create"
              initialKind={quickCreate}
              submitting={quickSubmitting}
              submitError={quickSubmitError}
              secretNames={secretNames}
              modelOptions={modelOptions}
              isAdmin={isAdmin}
              onSubmit={onQuickCreateSubmit}
              onOpenChange={(open) => { if (!open) { setQuickCreate(null); setQuickSubmitError(null) } }}
            />
          )}
        </div>
        {rightPanel === "inputs" ? (
          <InputsSchemaPanel schema={inputsSchema} onChange={setInputsSchema} />
        ) : (
        <PropertiesPanel
          node={selected}
          prompts={prompts}
          basics={basics}
          org={org}
          otherIds={otherIds}
          onPatch={patchSelected}
          onRename={renameSelected}
          onDelete={deleteSelected}
          typedParams={(() => {
            const ct = selected?.typeId ? typedTypeById.get(selected.typeId) : undefined
            return ct?.kind === "llm" ? (ct.params as LlmParams) : undefined
          })()}
          typedHttpParams={(() => {
            const ct = selected?.typeId ? typedTypeById.get(selected.typeId) : undefined
            return ct?.kind === "http" ? (ct.params as HttpParams) : undefined
          })()}
          typedScriptParams={(() => {
            const ct = selected?.typeId ? typedTypeById.get(selected.typeId) : undefined
            return ct?.kind === "script" ? (ct.params as ScriptParams) : undefined
          })()}
          upstreamNodes={
            selected
              ? (() => {
                  // 直接上游 id 集合从 EDGES 推导（单一真源），而非 selected.dependsOn——
                  // 后者对会话内新建/连线的节点是陈旧的 []（dependsOn 仅在保存时由边回填）。
                  const upstreamIds = new Set(
                    directUpstreamIds(rfEdges, selected.id),
                  )
                  return (rfNodes as RFNode[])
                    .filter((n) => upstreamIds.has(n.id))
                    .map((n) => {
                      // P5：按上游 node.type 在已持目录解析其 OutputSchema（字段选择器候选源）。
                      // descTypeFor 桥接 bare 内置名→studio.* desc 类型（否则裸 script 撞无 schema 的
                      // Starlark script 条目 → 字段选择器永不渲染，见 nodeColor.descTypeFor）。
                      const t = descTypeFor(n.data.node.type)
                      const desc = nodeTypeDescs.find((d) => d.type === t)
                      return {
                        id: n.id,
                        label: n.data.node.label ?? n.id,
                        outputSchema: desc?.outputSchema ?? [],
                      }
                    })
                })()
              : []
          }
          exprChannel={exprChannel}
          description={nodeDesc}
          onEditType={
            selected && isCustomType(selected.type)
              ? () => {
                  const c = customTypes.find((x) => x.type === selected.type)
                  if (c) setTypeDialog({ mode: "edit", type: selected.type, initial: { label: c.label, color: c.color } })
                }
              : undefined
          }
        />
        )}
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
