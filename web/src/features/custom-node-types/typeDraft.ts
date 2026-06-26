import type { CustomNodeType, HttpParams, LlmParams, ScriptParams } from "@/lib/types"
import { CUSTOM_PALETTE } from "@/features/workflow-canvas/nodeColor"

// 类型化创建/编辑对话框（TypeDialog）的草稿模型 + 校验。抽成独立模块（非组件文件），
// 让 TypeDialog.tsx 仅导出组件（满足 react-refresh/only-export-components）。

// 默认 LLM 参数：userPrompt 为必填（其他可选）。
export const DEFAULT_LLM_PARAMS: LlmParams = { userPrompt: "" }

// 默认 http 参数：method/url/headers 必填（其余可选）。
export const DEFAULT_HTTP_PARAMS: HttpParams = { method: "GET", url: "", headers: {}, outputFormat: "text" }

// 默认 script 参数：code 必填（outputFormat 可选，默认 text）。
export const DEFAULT_SCRIPT_PARAMS: ScriptParams = { code: "", outputFormat: "text" }

export type NodeKind = "llm" | "http" | "script"

export interface FormDraft {
  label: string
  color: string
  kind: NodeKind
  params: LlmParams | HttpParams | ScriptParams
}

export function paramsForKind(kind: NodeKind): LlmParams | HttpParams | ScriptParams {
  return kind === "llm"
    ? DEFAULT_LLM_PARAMS
    : kind === "http"
      ? DEFAULT_HTTP_PARAMS
      : DEFAULT_SCRIPT_PARAMS
}

// 空表单状态（新建时使用）；kind 默认 llm，可由调用方（如画布快建 chip）指定。
export function emptyDraft(kind: NodeKind = "llm"): FormDraft {
  return { label: "", color: CUSTOM_PALETTE[0], kind, params: paramsForKind(kind) }
}

// 从已有类型预填草稿（编辑时使用）。kind/params 形状由后端条目决定。
export function draftFrom(ct: CustomNodeType): FormDraft {
  return { label: ct.label, color: ct.color, kind: ct.kind, params: ct.params }
}

// http header 值引用了密钥则为 secret-bearing（与 HttpParamForm 判定一致）。
const SECRET_REF_RE = /\{\{\s*secret:/
export function isSecretBearing(draft: FormDraft): boolean {
  if (draft.kind !== "http") return false
  const p = draft.params as HttpParams
  return Object.values(p.headers ?? {}).some((v) => SECRET_REF_RE.test(v))
}

// 草稿是否满足提交所需必填字段（按 kind 区分）。
export function draftValid(draft: FormDraft): boolean {
  if (draft.label.trim() === "") return false
  if (draft.kind === "llm") {
    return (draft.params as LlmParams).userPrompt.trim() !== ""
  }
  if (draft.kind === "script") {
    // code 必填（{{secret:}} 由后端拒绝，前端不重复校验）。
    return (draft.params as ScriptParams).code.trim() !== ""
  }
  const p = draft.params as HttpParams
  // url 必填且不得含 {{...}} 模板。
  return p.url.trim() !== "" && !/\{\{/.test(p.url)
}
