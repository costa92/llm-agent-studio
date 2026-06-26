import { useState } from "react"
import { Loader2 } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Input } from "@/components/ui/input"
import type { HttpParams, LlmParams, ScriptParams } from "@/lib/types"
import { LlmParamForm } from "./LlmParamForm"
import { HttpParamForm } from "./HttpParamForm"
import { ScriptParamForm } from "./ScriptParamForm"
import { CUSTOM_PALETTE } from "@/features/workflow-canvas/nodeColor"
import {
  type FormDraft,
  type NodeKind,
  emptyDraft,
  isSecretBearing,
  draftValid,
  paramsForKind,
} from "./typeDraft"

// ─── 类型编辑对话框 ────────────────────────────────────────────────────────────
// 独立组件而非内联，确保 open→reopen 时 useState 能通过 key prop 重置（Phase-1 教训）。
// 从父组件传 key={target?.id ?? "create"} 强制重新挂载以清除陈旧状态。
//
// 注：此处使用受控 value/onChange 表单（LlmParamForm），而非 FormDialog 的 react-hook-form
// 合约（FormDialog 要求 zodResolver schema + register/handleSubmit）。两者接口不兼容，
// 因此保留独立 Dialog，但把 submit/状态机委托给父组件的 useCrudResource（含 toast）。

export interface TypeDialogProps {
  open: boolean
  mode: "create" | "edit"
  // 预填草稿（编辑态必传）。新建态可省略，回退到 emptyDraft(initialKind)。
  initial?: FormDraft
  // 新建态种子 kind（画布快建 chip 用，如「+ HTTP 节点」直接以 http 打开）；
  // 仅当 initial 缺省时生效。
  initialKind?: NodeKind
  submitting: boolean
  submitError: string | null
  // 组织密钥名（供 http 表单的「插入密钥」下拉 + secret-bearing 判定）。
  secretNames: string[]
  // org 文本模型选项（供 llm 表单的模型下拉；与画布模型选择器同源同形）。
  modelOptions?: { value: string; label: string }[]
  // 当前用户是否 admin（http secret-bearing 类型仅 admin 可创建/保存；后端强制，前端镜像）。
  isAdmin: boolean
  onSubmit: (draft: FormDraft) => void
  onOpenChange: (open: boolean) => void
}

export function TypeDialog({
  open,
  mode,
  initial,
  initialKind,
  submitting,
  submitError,
  secretNames,
  modelOptions = [],
  isAdmin,
  onSubmit,
  onOpenChange,
}: TypeDialogProps) {
  const [draft, setDraft] = useState<FormDraft>(
    () => initial ?? emptyDraft(initialKind ?? "llm"),
  )

  function patch(partial: Partial<FormDraft>) {
    setDraft((d) => ({ ...d, ...partial }))
  }

  // 切换 kind 时重置 params 为该 kind 的默认形状（仅新建态可切 kind）。
  function setKind(kind: NodeKind) {
    setDraft((d) => ({ ...d, kind, params: paramsForKind(kind) }))
  }

  const secretBearing = isSecretBearing(draft)
  // 非 admin 且 secret-bearing → 禁止提交（后端会 403，前端镜像并给出提示）。
  const adminBlocked = secretBearing && !isAdmin
  const canSubmit = draftValid(draft) && !adminBlocked

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    onSubmit(draft)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{mode === "create" ? "新建自定义节点类型" : "编辑自定义节点类型"}</DialogTitle>
          <DialogDescription>
            {mode === "create"
              ? "定义一个可复用的自定义节点类型（组织级，LLM / HTTP / Script）。"
              : "修改节点类型的标签、颜色或参数（同名节点自动继承）；类型 kind 不可变。"}
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="flex flex-col gap-4 mt-2">
          {/* 标签 */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cnt-label" className="text-[13px] font-medium text-text-1">
              名称
            </Label>
            <Input
              id="cnt-label"
              placeholder="如 翻译助手"
              value={draft.label}
              onChange={(e) => patch({ label: e.target.value })}
              className="text-[13px]"
            />
          </div>

          {/* 颜色选择 */}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[13px] font-medium text-text-1">颜色</Label>
            <div className="flex flex-wrap gap-2">
              {CUSTOM_PALETTE.map((c) => (
                <button
                  key={c}
                  type="button"
                  aria-label={`颜色 ${c}`}
                  className="h-6 w-6 rounded-full border-2 transition-all"
                  style={{
                    backgroundColor: c,
                    borderColor: draft.color === c ? "var(--amber)" : "transparent",
                  }}
                  onClick={() => patch({ color: c })}
                />
              ))}
            </div>
          </div>

          {/* kind 选择：仅新建态可切（kind 不可变）；编辑态只读展示。 */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cnt-kind" className="text-[13px] font-medium text-text-1">类型</Label>
            {mode === "create" ? (
              <select
                id="cnt-kind"
                aria-label="kind"
                value={draft.kind}
                onChange={(e) => setKind(e.target.value as NodeKind)}
                className="h-8 rounded-md border border-input bg-background px-2 text-[13px] text-text-1 focus:ring-1 focus:ring-ring"
              >
                <option value="llm">LLM</option>
                <option value="http">HTTP</option>
                <option value="script">Script</option>
              </select>
            ) : (
              <Input
                value={draft.kind}
                disabled
                aria-label="kind"
                className="text-[13px] opacity-60 cursor-not-allowed"
              />
            )}
          </div>

          {/* 参数表单：按 kind 渲染 LLM / HTTP / Script。 */}
          <div className="border-t border-line pt-3">
            <p className="text-[12px] text-text-2 mb-3">
              {draft.kind === "llm" ? "LLM 参数" : draft.kind === "http" ? "HTTP 参数" : "Script 参数"}
            </p>
            {draft.kind === "llm" ? (
              <LlmParamForm
                value={draft.params as LlmParams}
                onChange={(params) => patch({ params })}
                modelOptions={modelOptions}
              />
            ) : draft.kind === "http" ? (
              <HttpParamForm
                value={draft.params as HttpParams}
                onChange={(params) => patch({ params })}
                secretNames={secretNames}
              />
            ) : (
              <ScriptParamForm
                value={draft.params as ScriptParams}
                onChange={(params) => patch({ params })}
              />
            )}
          </div>

          {/* 非 admin 创建 secret-bearing 类型的前置提示（后端是权威，前端镜像）。 */}
          {adminBlocked && (
            <p role="alert" className="text-[12px] text-danger">
              引用了密钥的 HTTP 类型需要管理员权限，当前账号无法创建或保存。
            </p>
          )}

          {submitError != null && submitError !== "" && (
            <p role="alert" className="text-[12px] text-danger">
              {submitError}
            </p>
          )}

          <DialogFooter>
            <UiButton type="button" variant="outline" onClick={() => onOpenChange(false)}>
              取消
            </UiButton>
            <Button type="submit" variant="amber" disabled={submitting || !canSubmit}>
              {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
              {mode === "create" ? "创建" : "保存"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
