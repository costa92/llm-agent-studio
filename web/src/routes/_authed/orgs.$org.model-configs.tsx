import { createFileRoute } from "@tanstack/react-router"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { ModelConfigView } from "@/features/cost/ModelConfigPage"
import {
  useCreateModelConfig,
  useDeleteModelConfig,
  useListModels,
  useModelCatalog,
  useModelConfigs,
  useRevealModelKey,
  useUpdateModelConfig,
} from "@/features/cost/api"
import type { CreateModelConfigInput, ModelConfig } from "@/lib/types"

// T13：模型配置（admin-only）。成功/失败 toast 由 ModelConfigView 内的 useCrudResource 统一发出
// （与 Prompt/Storage 一致）；后端 400（密钥型 param 等）经 configError 映射后内联/吐司展示。
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
  const listModels = useListModels(org)
  const revealKey = useRevealModelKey(org)

  // 返回 mutateAsync 的 Promise（成功值/拒绝原样透传）；toast 与错误映射由 useCrudResource 统一处理。
  function handleCreate(input: CreateModelConfigInput): Promise<ModelConfig> {
    return create.mutateAsync(input)
  }

  // 编辑：apiKey 留空 → 后端保留既有密钥；404（不存在/跨 org）等错误经 useCrudResource 映射。
  function handleUpdate(
    id: string,
    input: CreateModelConfigInput,
  ): Promise<ModelConfig> {
    return update.mutateAsync({ id, input })
  }

  // 删除：确认弹窗已在 view 内；此处只发请求。
  function handleDelete(id: string): Promise<void> {
    return del.mutateAsync(id)
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
        onListModels={(input) => listModels.mutateAsync(input)}
        onRevealKey={(id) =>
          revealKey.mutateAsync(id).then((r) => r.apiKey)
        }
      />
    </AdminGate>
  )
}
