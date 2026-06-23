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
import { ApiError } from "@/lib/apiClient"
import type { CustomNodeType, LlmParams, UpsertCustomNodeTypeInput } from "@/lib/types"
import {
  useCustomNodeTypes,
  useCreateCustomNodeType,
  useUpdateCustomNodeType,
  useDeleteCustomNodeType,
} from "./api"
import { LlmParamForm } from "./LlmParamForm"
import { CUSTOM_PALETTE } from "@/features/workflow-canvas/nodeColor"
import { CrudResourcePage, DataView, ConfirmDialog } from "../common/crud"

// 默认 LLM 参数：userPrompt 为必填（其他可选）。
const DEFAULT_PARAMS: LlmParams = { userPrompt: "" }

// 空表单状态（新建时使用）。
function emptyDraft(): FormDraft {
  return { label: "", color: CUSTOM_PALETTE[0], params: DEFAULT_PARAMS }
}

// 从已有类型预填草稿（编辑时使用）。
function draftFrom(ct: CustomNodeType): FormDraft {
  return { label: ct.label, color: ct.color, params: ct.params }
}

interface FormDraft {
  label: string
  color: string
  params: LlmParams
}

// ─── 类型编辑对话框 ────────────────────────────────────────────────────────────
// 独立组件而非内联，确保 open→reopen 时 useState 能通过 key prop 重置（Phase-1 教训）。
// 从父组件传 key={target?.id ?? "create"} 强制重新挂载以清除陈旧状态。

interface TypeDialogProps {
  open: boolean
  mode: "create" | "edit"
  initial: FormDraft
  submitting: boolean
  submitError: string | null
  onSubmit: (draft: FormDraft) => void
  onOpenChange: (open: boolean) => void
}

function TypeDialog({
  open,
  mode,
  initial,
  submitting,
  submitError,
  onSubmit,
  onOpenChange,
}: TypeDialogProps) {
  const [draft, setDraft] = useState<FormDraft>(initial)

  function patch(partial: Partial<FormDraft>) {
    setDraft((d) => ({ ...d, ...partial }))
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (draft.label.trim() === "" || draft.params.userPrompt.trim() === "") return
    onSubmit(draft)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{mode === "create" ? "新建自定义节点类型" : "编辑自定义节点类型"}</DialogTitle>
          <DialogDescription>
            {mode === "create"
              ? "定义一个可复用的 LLM 节点类型（组织级）。"
              : "修改 LLM 节点类型的标签、颜色或参数（同名节点自动继承）。"}
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

          {/* kind 固定为 llm（Phase 2A 只支持 llm）*/}
          <div className="flex flex-col gap-1.5">
            <Label className="text-[13px] font-medium text-text-1">类型</Label>
            <Input
              value="llm"
              disabled
              aria-label="kind"
              className="text-[13px] opacity-60 cursor-not-allowed"
            />
          </div>

          {/* LLM 参数表单 */}
          <div className="border-t border-line pt-3">
            <p className="text-[12px] text-text-2 mb-3">LLM 参数</p>
            <LlmParamForm value={draft.params} onChange={(params) => patch({ params })} />
          </div>

          {submitError != null && submitError !== "" && (
            <p role="alert" className="text-[12px] text-danger">
              {submitError}
            </p>
          )}

          <DialogFooter>
            <UiButton type="button" variant="outline" onClick={() => onOpenChange(false)}>
              取消
            </UiButton>
            <Button
              type="submit"
              variant="amber"
              disabled={submitting || draft.label.trim() === "" || draft.params.userPrompt.trim() === ""}
            >
              {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
              {mode === "create" ? "创建" : "保存"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── CustomNodeTypeManager ─────────────────────────────────────────────────────
// 组织级自定义节点类型管理页：列表 + 新建/编辑 Dialog + 删除确认 + 409 (in-use) 错误提示。
// 路由：/orgs/{org}/custom-node-types（admin-only，AdminGate 在路由层）。

export interface CustomNodeTypeManagerProps {
  org: string
}

export function CustomNodeTypeManager({ org }: CustomNodeTypeManagerProps) {
  const query = useCustomNodeTypes(org)
  const createMutation = useCreateCustomNodeType(org)
  const updateMutation = useUpdateCustomNodeType(org)
  const deleteMutation = useDeleteCustomNodeType(org)

  // 对话框状态：null = 关闭；否则携带 mode + 目标（编辑时）。
  const [dialog, setDialog] = useState<{ mode: "create" | "edit"; target: CustomNodeType | null } | null>(null)
  // 删除确认目标。
  const [deleteTarget, setDeleteTarget] = useState<CustomNodeType | null>(null)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  // 提交状态 + 错误。
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)

  function openCreate() {
    setSubmitError(null)
    setDialog({ mode: "create", target: null })
  }

  function openEdit(ct: CustomNodeType) {
    setSubmitError(null)
    setDialog({ mode: "edit", target: ct })
  }

  function closeDialog() {
    setDialog(null)
    setSubmitError(null)
  }

  async function handleSubmit(draft: FormDraft) {
    if (!dialog) return
    setSubmitting(true)
    setSubmitError(null)
    const input: UpsertCustomNodeTypeInput = {
      label: draft.label.trim(),
      color: draft.color,
      kind: "llm",
      params: draft.params,
    }
    try {
      if (dialog.mode === "create") {
        await createMutation.mutateAsync(input)
      } else if (dialog.target) {
        await updateMutation.mutateAsync({ id: dialog.target.id, input })
      }
      closeDialog()
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setSubmitError("名称或 slug 已被占用，请使用其他名称")
      } else {
        setSubmitError(err instanceof Error ? err.message : "操作失败，请重试")
      }
    } finally {
      setSubmitting(false)
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    setDeleteError(null)
    try {
      await deleteMutation.mutateAsync(deleteTarget.id)
      setDeleteTarget(null)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setDeleteError("该类型已被工作流节点引用，请先移除引用后再删除")
      } else {
        setDeleteError(err instanceof Error ? err.message : "删除失败，请重试")
      }
      setDeleting(false)
    }
  }

  const editTarget = dialog?.target ?? null
  const dialogInitial = editTarget ? draftFrom(editTarget) : emptyDraft()

  return (
    <>
      <CrudResourcePage
        title="自定义节点类型"
        description="管理组织级 LLM 自定义节点类型，在工作流中以 custom: 节点引用；删除前需移除所有引用节点。"
        createLabel="新建类型"
        onCreate={openCreate}
        isLoading={query.isLoading}
        isError={query.isError}
        onRetry={() => void query.refetch()}
        isEmpty={(query.data ?? []).length === 0}
        emptyHint="暂无自定义节点类型，点击「新建类型」开始。"
      >
        <DataView
          layout="table"
          items={query.data ?? []}
          getId={(ct) => ct.id}
          columns={[
            {
              key: "label",
              header: "名称",
              cell: (ct) => (
                <span className="flex items-center gap-2">
                  <span
                    className="inline-block h-3 w-3 rounded-full flex-shrink-0"
                    style={{ backgroundColor: ct.color }}
                  />
                  {ct.label}
                </span>
              ),
            },
            { key: "slug", header: "Slug", className: "font-mono text-[12px] text-text-2", cell: (ct) => ct.slug },
            { key: "kind", header: "类型", cell: (ct) => ct.kind },
          ]}
          rowActions={[
            { key: "edit", label: "编辑", onClick: openEdit },
            { key: "delete", label: "删除", variant: "destructive" as const, onClick: (ct) => { setDeleteError(null); setDeleteTarget(ct) } },
          ]}
        />
      </CrudResourcePage>

      {/* 新建/编辑对话框 — key 变化时强制重新挂载，清除内部 useState(initial) 陈旧值。 */}
      {dialog !== null && (
        <TypeDialog
          key={editTarget?.id ?? "create"}
          open={dialog !== null}
          mode={dialog.mode}
          initial={dialogInitial}
          submitting={submitting}
          submitError={submitError}
          onSubmit={(draft) => void handleSubmit(draft)}
          onOpenChange={(open) => { if (!open) closeDialog() }}
        />
      )}

      {/* 删除确认对话框。 */}
      <ConfirmDialog
        open={deleteTarget !== null}
        title="确认删除自定义节点类型？"
        description={
          <>
            <span>删除「{deleteTarget?.label ?? ""}」后无法撤销。</span>
            {deleteError && (
              <span role="alert" className="block mt-2 text-danger text-[12px]">
                {deleteError}
              </span>
            )}
          </>
        }
        confirmLabel="确认删除"
        variant="danger"
        confirming={deleting}
        onConfirm={() => void confirmDelete()}
        onCancel={() => { setDeleteTarget(null); setDeleteError(null) }}
      />
    </>
  )
}
