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

export interface AssetsState {
  total: number
  done: number
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
}
