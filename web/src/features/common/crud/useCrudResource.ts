import { useState } from "react"
import { toast } from "sonner"

export interface CrudConfig<T> {
  getId: (item: T) => string
  create: (input: unknown) => Promise<unknown>
  update: (id: string, input: unknown) => Promise<unknown>
  remove: (id: string) => Promise<unknown>
  labels?: { created?: string; updated?: string; deleted?: string }
  // 把错误映射成用户文案；action 区分 create/update/delete。返回的字符串用于 toast / submitError。
  errorMessage?: (action: "create" | "update" | "delete", err: unknown) => string
}

interface DialogState<T> { mode: "create" | "edit"; target: T | null }

export interface CrudResource<T> {
  dialog: DialogState<T> | null
  deleteTarget: T | null
  submitError: string | null
  submitting: boolean
  deleting: boolean
  openCreate: () => void
  openEdit: (item: T) => void
  closeDialog: () => void
  requestDelete: (item: T) => void
  cancelDelete: () => void
  confirmDelete: () => void
  submit: (values: unknown) => Promise<void>
}

const defaultErr = () => "操作失败，请重试"

// headless CRUD 状态机：拥有 dialog/deleteTarget 状态 + 调用注入的 create/update/remove
// （通常是各 api.ts hook 的 mutateAsync，已自带 invalidate），并接 toast/错误文案。
export function useCrudResource<T>(cfg: CrudConfig<T>): CrudResource<T> {
  const [dialog, setDialog] = useState<DialogState<T> | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<T | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const msg = cfg.errorMessage ?? defaultErr

  function openCreate() { setSubmitError(null); setDialog({ mode: "create", target: null }) }
  function openEdit(item: T) { setSubmitError(null); setDialog({ mode: "edit", target: item }) }
  function closeDialog() { setDialog(null); setSubmitError(null) }
  function requestDelete(item: T) { setDeleteTarget(item) }
  function cancelDelete() { setDeleteTarget(null) }

  function submit(values: unknown): Promise<void> {
    if (!dialog) return Promise.resolve()
    // Fix 3: edit 模式下 target 为 null（防御性保护，正常路径不可达）——no-op，不落 create。
    if (dialog.mode === "edit" && !dialog.target) {
      setSubmitting(false)
      return Promise.resolve()
    }
    const isEdit = dialog.mode === "edit"
    setSubmitting(true)
    setSubmitError(null)
    // Fix 4: 返回 promise 链，调用方可 await；.finally 仍保证 submitting 归位。
    const p = isEdit
      ? cfg.update(cfg.getId(dialog.target!), values)
      : cfg.create(values)
    return p.then(() => {
      toast.success(isEdit ? cfg.labels?.updated ?? "已更新" : cfg.labels?.created ?? "已创建")
      setDialog(null)
    }).catch((err) => {
      setSubmitError(msg(isEdit ? "update" : "create", err))
    }).finally(() => setSubmitting(false))
  }

  function confirmDelete() {
    if (!deleteTarget) return
    setDeleting(true)
    cfg.remove(cfg.getId(deleteTarget))
      .then(() => {
        toast.success(cfg.labels?.deleted ?? "已删除")
        setDeleteTarget(null)
      })
      .catch((err) => toast.error(msg("delete", err)))
      .finally(() => setDeleting(false))
  }

  return {
    dialog, deleteTarget, submitError, submitting, deleting,
    openCreate, openEdit, closeDialog, requestDelete, cancelDelete, confirmDelete, submit,
  }
}
