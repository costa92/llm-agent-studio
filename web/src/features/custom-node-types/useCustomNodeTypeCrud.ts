import { ApiError } from "@/lib/apiClient"
import type { CustomNodeType, UpsertCustomNodeTypeInput } from "@/lib/types"
import {
  useCustomNodeTypes,
  useCreateCustomNodeType,
  useUpdateCustomNodeType,
  useDeleteCustomNodeType,
} from "./api"
import { useOrgSecrets } from "@/features/org-secrets/api"
import { useOrgTextModels } from "@/features/cost/api"
import { useRole } from "@/app/rbac"
import { emptyDraft, draftFrom, type FormDraft } from "./typeDraft"
import { useCrudResource } from "../common/crud"

// 组织级自定义节点类型 CRUD 逻辑（列表查询 + 增删改 mutation + useCrudResource 编排 +
// 409/403 错误映射 + 提交转换）。抽出来让「路由页」(CustomNodeTypeManager，全页外壳)
// 与「节点管理模态的自定义 tab」(CustomNodeTypesPanel，紧凑布局) 共享同一套逻辑，只各自
// 渲染不同外壳，避免重复维护高风险的 mutation/错误映射代码。
export function useCustomNodeTypeCrud(org: string) {
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

  return { query, crud, secretNames, modelOptions, isAdmin, editTarget, dialogInitial, handleDialogSubmit }
}
