import { createFileRoute } from "@tanstack/react-router"
import { toast } from "sonner"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { ModelConfigView } from "@/features/cost/ModelConfigPage"
import {
  useCreateModelConfig,
  useDeleteModelConfig,
  useModelCatalog,
  useModelConfigs,
  useUpdateModelConfig,
} from "@/features/cost/api"
import { modelConfigErrorMessage } from "@/features/cost/configError"
import type { CreateModelConfigInput, ModelConfig } from "@/lib/types"

// T13：模型配置（admin-only）。含密钥型 param → 后端 400 ErrSecretParam → toast。
export const Route = createFileRoute("/_authed/orgs/$org/model-configs")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: ModelConfigsPage,
})

function ModelConfigsPage() {
  const { org } = Route.useParams()
  const role = useRole(org)

  const configs = useModelConfigs(org)
  const catalog = useModelCatalog()
  const create = useCreateModelConfig(org)
  const update = useUpdateModelConfig(org)
  const del = useDeleteModelConfig(org)

  // 返回 Promise 让表单 await；成功 toast 在 onSuccess、失败 toast（含 400 密钥拒绝）在 catch。
  function handleCreate(input: CreateModelConfigInput): Promise<ModelConfig> {
    return create.mutateAsync(input).then(
      (mc) => {
        toast.success("模型配置已保存")
        return mc
      },
      (err: unknown) => {
        toast.error(modelConfigErrorMessage(err))
        throw err
      },
    )
  }

  // 编辑：apiKey 留空 → 后端保留既有密钥；404（不存在/跨 org）等错误同样 toast。
  function handleUpdate(
    id: string,
    input: CreateModelConfigInput,
  ): Promise<ModelConfig> {
    return update.mutateAsync({ id, input }).then(
      (mc) => {
        toast.success("模型配置已更新")
        return mc
      },
      (err: unknown) => {
        toast.error(modelConfigErrorMessage(err))
        throw err
      },
    )
  }

  // 删除：确认弹窗已在 view 内；此处只发请求 + toast。
  function handleDelete(id: string): Promise<void> {
    return del.mutateAsync(id).then(
      () => {
        toast.success("模型配置已删除")
      },
      (err: unknown) => {
        toast.error(modelConfigErrorMessage(err))
        throw err
      },
    )
  }

  return (
    <AdminGate role={role}>
      <ModelConfigView
        configs={configs.data}
        catalog={catalog.data}
        isLoading={configs.isLoading}
        isError={configs.isError}
        onRetry={() => void configs.refetch()}
        onCreate={handleCreate}
        onUpdate={handleUpdate}
        onDelete={handleDelete}
      />
    </AdminGate>
  )
}
