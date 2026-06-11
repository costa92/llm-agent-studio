import { createFileRoute } from "@tanstack/react-router"
import { toast } from "sonner"
import { useRole } from "@/app/rbac"
import { AdminGate } from "@/features/cost/AdminGate"
import { ModelConfigView } from "@/features/cost/ModelConfigPage"
import { useCreateModelConfig, useModelCatalog, useModelConfigs } from "@/features/cost/api"
import { modelConfigErrorMessage } from "@/features/cost/configError"
import type { CreateModelConfigInput, ModelConfig } from "@/lib/types"

// T13：模型配置（admin-only）。含密钥型 param → 后端 400 ErrSecretParam → toast。
export const Route = createFileRoute("/_authed/orgs/$org/model-configs")({
  component: ModelConfigsPage,
})

function ModelConfigsPage() {
  const { org } = Route.useParams()
  const role = useRole(org)

  const configs = useModelConfigs(org)
  const catalog = useModelCatalog()
  const create = useCreateModelConfig(org)

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

  return (
    <AdminGate role={role}>
      <ModelConfigView
        configs={configs.data}
        catalog={catalog.data}
        isLoading={configs.isLoading}
        isError={configs.isError}
        onRetry={() => void configs.refetch()}
        onCreate={handleCreate}
      />
    </AdminGate>
  )
}
