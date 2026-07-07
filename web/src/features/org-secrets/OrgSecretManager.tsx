import { useState } from "react"
import { KeyRound, Loader2, Plus } from "lucide-react"
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
import type { OrgSecret } from "@/lib/types"
import {
  useOrgSecrets,
  useCreateOrgSecret,
  useUpdateOrgSecret,
  useDeleteOrgSecret,
  type UpsertOrgSecretInput,
} from "./api"
import {
  useCrudResource,
  CrudResourcePage,
  DataView,
  ConfirmDialog,
} from "../common/crud"

interface FormDraft {
  name: string
  value: string
}

function emptyDraft(): FormDraft {
  return { name: "", value: "" }
}

// 编辑预填：name 取自既有密钥；value 永远留空（write-only，永不回显）。
function draftFrom(s: OrgSecret): FormDraft {
  return { name: s.name, value: "" }
}

// ─── 密钥编辑对话框 ────────────────────────────────────────────────────────────
// 独立组件而非内联，确保 open→reopen 时 useState 能通过 key prop 重置（Phase-1 教训）。
// 从父组件传 key={target?.id ?? "create"} 强制重新挂载以清除陈旧状态。

interface SecretDialogProps {
  open: boolean
  mode: "create" | "edit"
  initial: FormDraft
  submitting: boolean
  submitError: string | null
  onSubmit: (draft: FormDraft) => void
  onOpenChange: (open: boolean) => void
}

function SecretDialog({
  open,
  mode,
  initial,
  submitting,
  submitError,
  onSubmit,
  onOpenChange,
}: SecretDialogProps) {
  const [draft, setDraft] = useState<FormDraft>(initial)

  function patch(partial: Partial<FormDraft>) {
    setDraft((d) => ({ ...d, ...partial }))
  }

  // 名称在编辑态不可改（密钥按 name 寻址）；新建态必填。
  // 新建态 value 必填；编辑态留空 = 保留原值。
  const nameInvalid = draft.name.trim() === ""
  const valueInvalid = mode === "create" && draft.value === ""

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (nameInvalid || valueInvalid) return
    onSubmit(draft)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{mode === "create" ? "新建密钥" : "编辑密钥"}</DialogTitle>
          <DialogDescription>
            {mode === "create"
              ? "组织级密钥（如第三方 API key）。密钥值加密存储、永不回显，仅供 http 节点以 {{secret:NAME}} 引用。"
              : "修改密钥值（名称不可变）。密钥值加密存储、永不回显。"}
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="flex flex-col gap-4 mt-2">
          {/* 名称 */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="secret-name" className="text-[13px] font-medium text-text-1">
              名称
            </Label>
            <Input
              id="secret-name"
              placeholder="如 PARTNER_KEY"
              value={draft.name}
              disabled={mode === "edit"}
              onChange={(e) => patch({ name: e.target.value })}
              className="text-[13px]"
            />
          </div>

          {/* 密钥值 — write-only */}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="secret-value" className="text-[13px] font-medium text-text-1">
              密钥值
            </Label>
            <Input
              id="secret-value"
              type="password"
              autoComplete="new-password"
              placeholder={mode === "edit" ? "留空保留原值" : "输入密钥值"}
              value={draft.value}
              onChange={(e) => patch({ value: e.target.value })}
              className="text-[13px]"
            />
            <p className="text-[11px] text-text-2">
              {mode === "edit"
                ? "留空保留原值；填入新值则重新加密替换。密钥值加密存储、永不回显。"
                : "密钥值加密存储、永不回显。"}
            </p>
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
            <Button type="submit" variant="amber" disabled={submitting || nameInvalid || valueInvalid}>
              {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
              {mode === "create" ? "创建" : "保存"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── OrgSecretManager ──────────────────────────────────────────────────────────
// 组织级密钥管理页：列表（name + 已设置 badge）+ 新建/编辑 Dialog（write-only value）+ 删除确认。
// 路由：/orgs/{org}/secrets（admin-only，AdminGate 在路由层；后端 roleAdmin 强制）。

export interface OrgSecretManagerProps {
  org: string
}

export function OrgSecretManager({ org }: OrgSecretManagerProps) {
  const query = useOrgSecrets(org)
  const createMutation = useCreateOrgSecret(org)
  const updateMutation = useUpdateOrgSecret(org)
  const deleteMutation = useDeleteOrgSecret(org)

  const crud = useCrudResource<OrgSecret>({
    getId: (s) => s.name,
    create: (input) => createMutation.mutateAsync(input as UpsertOrgSecretInput),
    update: (name, input) =>
      updateMutation.mutateAsync({ name, input: input as UpsertOrgSecretInput }),
    remove: (name) => deleteMutation.mutateAsync(name),
    labels: { created: "密钥已创建", updated: "密钥已更新", deleted: "密钥已删除" },
    errorMessage: (action, err) => {
      if (action !== "delete" && err instanceof ApiError && err.status === 400) {
        return err.message || "请求无效，请检查名称与密钥值"
      }
      return err instanceof Error ? err.message : "操作失败，请重试"
    },
  })

  const editTarget = crud.dialog?.target ?? null
  const dialogInitial = editTarget ? draftFrom(editTarget) : emptyDraft()

  function handleDialogSubmit(draft: FormDraft) {
    const input: UpsertOrgSecretInput = { name: draft.name.trim(), value: draft.value }
    void crud.submit(input)
  }

  return (
    <>
      <CrudResourcePage
        title="组织密钥"
        description="管理组织级密钥（如第三方 API key），供 http 自定义节点以 {{secret:NAME}} 引用；密钥值加密存储、无解密端点、永不回显（区别于模型密钥：模型密钥可由管理员显式解密查看并记入审计）。"
        createLabel="新建密钥"
        onCreate={crud.openCreate}
        isLoading={query.isLoading}
        isError={query.isError}
        onRetry={() => void query.refetch()}
        isEmpty={(query.data ?? []).length === 0}
        emptyState={
          <div className="flex flex-col items-center gap-3 py-20 text-center border border-dashed border-line rounded-xl bg-bg-surface">
            <KeyRound className="h-10 w-10 text-text-3 stroke-[1.5]" />
            <p className="text-text-2 font-medium">暂无密钥</p>
            <p className="text-text-3 text-xs max-w-sm">
              保存第三方 API key 等组织级密钥，http 自定义节点即可用 {"{{secret:NAME}}"} 引用。
            </p>
            <Button variant="amber" className="mt-2" onClick={crud.openCreate}>
              <Plus className="mr-1.5 h-4 w-4" /> 新建第一个密钥
            </Button>
          </div>
        }
      >
        <DataView
          layout="table"
          items={query.data ?? []}
          getId={(s) => s.id}
          columns={[
            { key: "name", header: "名称", className: "font-mono text-[12px]", cell: (s) => s.name },
            {
              key: "hasValue",
              header: "状态",
              cell: (s) =>
                s.hasValue ? (
                  <span className="rounded bg-amber/20 px-1.5 py-0.5 text-[11px] font-medium text-amber">
                    已设置
                  </span>
                ) : (
                  <span className="text-[11px] text-text-3">未设置</span>
                ),
            },
          ]}
          rowActions={[
            { key: "edit", label: "编辑", onClick: crud.openEdit },
            { key: "delete", label: "删除", variant: "destructive" as const, onClick: crud.requestDelete },
          ]}
        />
      </CrudResourcePage>

      {/* 新建/编辑对话框 — key 变化时强制重新挂载，清除内部 useState(initial) 陈旧值。 */}
      {crud.dialog !== null && (
        <SecretDialog
          key={editTarget?.id ?? "create"}
          open={crud.dialog !== null}
          mode={crud.dialog.mode}
          initial={dialogInitial}
          submitting={crud.submitting}
          submitError={crud.submitError}
          onSubmit={handleDialogSubmit}
          onOpenChange={(open) => { if (!open) crud.closeDialog() }}
        />
      )}

      {/* 删除确认对话框。 */}
      <ConfirmDialog
        open={crud.deleteTarget !== null}
        title="确认删除密钥？"
        description={`删除「${crud.deleteTarget?.name ?? ""}」后引用它的 http 节点将无法解析，请先移除引用。此操作不可撤销。`}
        confirmLabel="确认删除"
        variant="danger"
        confirming={crud.deleting}
        onConfirm={crud.confirmDelete}
        onCancel={crud.cancelDelete}
      />
    </>
  )
}
