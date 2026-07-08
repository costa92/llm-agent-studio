// 对齐后端 internal/projectstate/state.go 的 ProjectState JSON 形状。
// 这是前端唯一的工作流状态真相源——不再由事件 reduce 推导(见 timeline.ts 瘦身)。
// 枚举字符串域与后端逐一对应;projectState.contract.test.ts 守护漂移。
import type { ProjectStatus } from "./types"

export type StageRole = "planner" | "script" | "storyboard" | "asset" | "review"
export type StageStatus2 = "blocked" | "pending" | "running" | "done" | "failed"
export type RunStatus2 = "idle" | "running" | "done"
export type PipStatus2 = "idle" | "running" | "done" | "failed"

export interface StageState {
  role: StageRole
  status: StageStatus2
  todoId?: string
}

export interface PipState {
  todoId: string
  status: PipStatus2
  assetId?: string
}

// GraphNode.status 与 StageStatus2 同域(后端 buildGraph 用 todoStatusToStage)。
export type GraphNodeStatus = StageStatus2

// InspectorBinaryRef 镜像后端 worker.BinaryRef / projectstate.InspectorBinaryRef：
// 指向 assets 表的细指针（字节永不内联，访问受控）。字段为 camelCase。
export interface InspectorBinaryRef {
  assetId: string
  mimeType: string
  kind: string
  // status omitempty（后端）→ 可缺省。
  status?: string
}

// InspectorItem 镜像后端 worker.Item / projectstate.InspectorItem：一个节点执行
// datum 的结构化 json + 可选 binary 引用。json 是 P2a 已落地的 canonical 对象
// （format='json' → 真实对象；format='text' → {"text":"..."}），逐字透传，前端不重解析。
export interface InspectorItem {
  json: unknown
  binary?: Record<string, InspectorBinaryRef>
}

export interface GraphNode {
  id: string
  label: string
  type: string
  status: GraphNodeStatus
  assetId?: string
  // custom 节点 (node_outputs) 的文本/JSON 产物，供运行视图选中面板渲染 (T3)。
  output?: string
  // output 非空时有意义；∈ "text" | "json"。
  outputFormat?: "text" | "json"
  // 该节点 node_outputs.items 的逐字透传（workflow-v2 P5d per-item inspector）。
  // additive：后端 omitempty → 老/标量节点无此键（undefined）。
  items?: InspectorItem[]
}

export interface GraphEdge {
  from: string
  to: string
}

export interface AssetsState {
  total: number
  done: number
  // 失败/取消的 asset todo 数(写存储失败等)。失败 run 据此显「N/M 素材写入失败」。
  // additive：旧后端不下发时按 0 处理。
  failed?: number
  pending: number
}

export interface ProblemError {
  todoId: string
  role?: string
  message: string
}

export interface PlanState {
  planId: string
  valid: boolean
  fallbackUsed: boolean
}

export interface ProjectState {
  projectId: string
  version: number
  status: ProjectStatus
  runStatus: RunStatus2
  plan?: PlanState
  stages: StageState[]
  pips: PipState[]
  assets: AssetsState
  error?: ProblemError
  nodes: GraphNode[]
  edges: GraphEdge[]
  isCustom: boolean
}
