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
import type {
  CustomNodeType,
  HttpParams,
  LlmParams,
  ScriptParams,
  UpsertCustomNodeTypeInput,
} from "@/lib/types"
import {
  useCustomNodeTypes,
  useCreateCustomNodeType,
  useUpdateCustomNodeType,
  useDeleteCustomNodeType,
} from "./api"
import { useOrgSecrets } from "@/features/org-secrets/api"
import { useRole } from "@/app/rbac"
import { LlmParamForm } from "./LlmParamForm"
import { HttpParamForm } from "./HttpParamForm"
import { ScriptParamForm } from "./ScriptParamForm"
import { CUSTOM_PALETTE } from "@/features/workflow-canvas/nodeColor"
import {
  useCrudResource,
  CrudResourcePage,
  DataView,
  ConfirmDialog,
} from "../common/crud"

// 默认 LLM 参数：userPrompt 为必填（其他可选）。
const DEFAULT_LLM_PARAMS: LlmParams = { userPrompt: "" }

// 默认 http 参数：method/url/headers 必填（其余可选）。
const DEFAULT_HTTP_PARAMS: HttpParams = { method: "GET", url: "", headers: {}, outputFormat: "text" }

// 默认 script 参数：code 必填（outputFormat 可选，默认 text）。
const DEFAULT_SCRIPT_PARAMS: ScriptParams = { code: "", outputFormat: "text" }

// 空表单状态（新建时使用）；默认 kind=llm。
function emptyDraft(): FormDraft {
  return { label: "", color: CUSTOM_PALETTE[0], kind: "llm", params: DEFAULT_LLM_PARAMS }
}

// 从已有类型预填草稿（编辑时使用）。kind/params 形状由后端条目决定。
function draftFrom(ct: CustomNodeType): FormDraft {
  return { label: ct.label, color: ct.color, kind: ct.kind, params: ct.params }
}

interface FormDraft {
  label: string
  color: string
  kind: "llm" | "http" | "script"
  params: LlmParams | HttpParams | ScriptParams
}

// http header 值引用了密钥则为 secret-bearing（与 HttpParamForm 判定一致）。
const SECRET_REF_RE = /\{\{\s*secret:/
function isSecretBearing(draft: FormDraft): boolean {
  if (draft.kind !== "http") return false
  const p = draft.params as HttpParams
  return Object.values(p.headers ?? {}).some((v) => SECRET_REF_RE.test(v))
}

// 草稿是否满足提交所需必填字段（按 kind 区分）。
function draftValid(draft: FormDraft): boolean {
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

// ─── 类型编辑对话框 ────────────────────────────────────────────────────────────
// 独立组件而非内联，确保 open→reopen 时 useState 能通过 key prop 重置（Phase-1 教训）。
// 从父组件传 key={target?.id ?? "create"} 强制重新挂载以清除陈旧状态。
//
// 注：此处使用受控 value/onChange 表单（LlmParamForm），而非 FormDialog 的 react-hook-form
// 合约（FormDialog 要求 zodResolver schema + register/handleSubmit）。两者接口不兼容，
// 因此保留独立 Dialog，但把 submit/状态机委托给父组件的 useCrudResource（含 toast）。

interface TypeDialogProps {
  open: boolean
  mode: "create" | "edit"
  initial: FormDraft
  submitting: boolean
  submitError: string | null
  // 组织密钥名（供 http 表单的「插入密钥」下拉 + secret-bearing 判定）。
  secretNames: string[]
  // 当前用户是否 admin（http secret-bearing 类型仅 admin 可创建/保存；后端强制，前端镜像）。
  isAdmin: boolean
  onSubmit: (draft: FormDraft) => void
  onOpenChange: (open: boolean) => void
}

function TypeDialog({
  open,
  mode,
  initial,
  submitting,
  submitError,
  secretNames,
  isAdmin,
  onSubmit,
  onOpenChange,
}: TypeDialogProps) {
  const [draft, setDraft] = useState<FormDraft>(initial)

  function patch(partial: Partial<FormDraft>) {
    setDraft((d) => ({ ...d, ...partial }))
  }

  // 切换 kind 时重置 params 为该 kind 的默认形状（仅新建态可切 kind）。
  function setKind(kind: "llm" | "http" | "script") {
    setDraft((d) => ({
      ...d,
      kind,
      params:
        kind === "llm"
          ? DEFAULT_LLM_PARAMS
          : kind === "http"
            ? DEFAULT_HTTP_PARAMS
            : DEFAULT_SCRIPT_PARAMS,
    }))
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
                onChange={(e) => setKind(e.target.value as "llm" | "http" | "script")}
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
  // 组织密钥名（http 表单「插入密钥」+ secret-bearing 判定）；非 admin 列表会 403 返空。
  const secretsQuery = useOrgSecrets(org)
  const secretNames = (secretsQuery.data ?? []).map((s) => s.name)
  // 当前用户角色：secret-bearing http 类型仅 admin 可创建/保存（后端权威，前端镜像）。
  const { isAdmin } = useRole(org)

  const crud = useCrudResource<CustomNodeType>({
    getId: (ct) => ct.id,
    create: (input) => createMutation.mutateAsync(input as UpsertCustomNodeTypeInput),
    update: (id, input) =>
      updateMutation.mutateAsync({ id, input: input as UpsertCustomNodeTypeInput }),
    remove: (id) => deleteMutation.mutateAsync(id),
    labels: { created: "自定义节点类型已创建", updated: "自定义节点类型已更新", deleted: "自定义节点类型已删除" },
    errorMessage: (action, err) => {
      if (action !== "delete" && err instanceof ApiError && err.status === 403) {
        return "引用了密钥的 HTTP 类型需要管理员权限"
      }
      if (action !== "delete" && err instanceof ApiError && err.status === 409) {
        return "名称或 slug 已被占用，请使用其他名称"
      }
      if (action === "delete" && err instanceof ApiError && err.status === 409) {
        return "该类型已被工作流节点引用，请先移除引用后再删除"
      }
      return err instanceof Error ? err.message : "操作失败，请重试"
    },
  })

  const editTarget = crud.dialog?.target ?? null
  const dialogInitial = editTarget ? draftFrom(editTarget) : emptyDraft()

  function handleDialogSubmit(draft: FormDraft) {
    const input: UpsertCustomNodeTypeInput = {
      label: draft.label.trim(),
      color: draft.color,
      kind: draft.kind,
      params: draft.params,
    }
    void crud.submit(input)
  }

  return (
    <>
      <CrudResourcePage
        title="自定义节点类型"
        description="管理组织级 LLM 自定义节点类型，在工作流中以 custom: 节点引用；删除前需移除所有引用节点。"
        createLabel="新建类型"
        onCreate={crud.openCreate}
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
            { key: "edit", label: "编辑", onClick: crud.openEdit },
            { key: "delete", label: "删除", variant: "destructive" as const, onClick: crud.requestDelete },
          ]}
        />
      </CrudResourcePage>

      {/* 新建/编辑对话框 — key 变化时强制重新挂载，清除内部 useState(initial) 陈旧值。 */}
      {crud.dialog !== null && (
        <TypeDialog
          key={editTarget?.id ?? "create"}
          open={crud.dialog !== null}
          mode={crud.dialog.mode}
          initial={dialogInitial}
          submitting={crud.submitting}
          submitError={crud.submitError}
          secretNames={secretNames}
          isAdmin={isAdmin}
          onSubmit={handleDialogSubmit}
          onOpenChange={(open) => { if (!open) crud.closeDialog() }}
        />
      )}

      {/* 删除确认对话框。 */}
      <ConfirmDialog
        open={crud.deleteTarget !== null}
        title="确认删除自定义节点类型？"
        description={`删除「${crud.deleteTarget?.label ?? ""}」后无法撤销。`}
        confirmLabel="确认删除"
        variant="danger"
        confirming={crud.deleting}
        onConfirm={crud.confirmDelete}
        onCancel={crud.cancelDelete}
      />
    </>
  )
}
