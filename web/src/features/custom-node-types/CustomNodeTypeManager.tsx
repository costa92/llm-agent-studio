import { ApiError } from "@/lib/apiClient"
import type {
  CustomNodeType,
  UpsertCustomNodeTypeInput,
} from "@/lib/types"
import {
  useCustomNodeTypes,
  useCreateCustomNodeType,
  useUpdateCustomNodeType,
  useDeleteCustomNodeType,
} from "./api"
import { useOrgSecrets } from "@/features/org-secrets/api"
import { useOrgTextModels } from "@/features/cost/api"
import { useRole } from "@/app/rbac"
import { TypeDialog } from "./TypeDialog"
import { emptyDraft, draftFrom, type FormDraft } from "./typeDraft"
import {
  useCrudResource,
  CrudResourcePage,
  DataView,
  ConfirmDialog,
} from "../common/crud"

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
  // org 文本模型 → llm 表单模型下拉选项（value=model, label="provider · model"，与画布同形）。
  const textModelsQuery = useOrgTextModels(org)
  const modelOptions = (textModelsQuery.data ?? []).map((m) => ({
    value: m.model,
    label: `${m.provider} · ${m.model}`,
  }))
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
          modelOptions={modelOptions}
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
