import { createFileRoute } from "@tanstack/react-router"
import { toast } from "sonner"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { StorageConfigView } from "@/features/storage/StorageConfigPage"
import {
  useDeleteOrgStorageConfig,
  useGlobalStorageConfig,
  useOrgStorageConfig,
  useUpsertGlobalStorageConfig,
  useUpsertOrgStorageConfig,
} from "@/features/storage/api"
import { storageConfigErrorMessage } from "@/features/storage/configError"
import type { StorageConfig, UpsertStorageConfigInput } from "@/lib/types"

// 存储配置（admin-only）：org 覆盖 + 全局默认。secret write-only，
// 要存密钥但未配 STUDIO_CONFIG_ENC_KEY → 后端 400 → toast。
export const Route = createFileRoute("/_authed/orgs/$org/storage-config")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: StorageConfigPage,
})

function StorageConfigPage() {
  const { org } = Route.useParams()
  const role = useRole(org)

  const orgConfig = useOrgStorageConfig(org)
  const globalConfig = useGlobalStorageConfig()
  const upsertOrg = useUpsertOrgStorageConfig(org)
  const delOrg = useDeleteOrgStorageConfig(org)
  const upsertGlobal = useUpsertGlobalStorageConfig()

  // 返回 Promise 让表单 await；成功 toast 在 then、失败 toast（含 400 缺加密密钥）在 catch。
  function handleOrgSubmit(
    input: UpsertStorageConfigInput,
  ): Promise<StorageConfig> {
    return upsertOrg.mutateAsync(input).then(
      (sc) => {
        toast.success("本组织存储配置已保存")
        return sc
      },
      (err: unknown) => {
        toast.error(storageConfigErrorMessage(err))
        throw err
      },
    )
  }

  function handleGlobalSubmit(
    input: UpsertStorageConfigInput,
  ): Promise<StorageConfig> {
    return upsertGlobal.mutateAsync(input).then(
      (sc) => {
        toast.success("全局默认存储配置已保存")
        return sc
      },
      (err: unknown) => {
        toast.error(storageConfigErrorMessage(err))
        throw err
      },
    )
  }

  // 删除：确认弹窗已在 view 内；此处只发请求 + toast。回退到全局默认。
  function handleOrgDelete(): Promise<void> {
    return delOrg.mutateAsync().then(
      () => {
        toast.success("已删除，回退到全局默认")
      },
      (err: unknown) => {
        toast.error(storageConfigErrorMessage(err))
        throw err
      },
    )
  }

  return (
    <AdminGate role={role}>
      <StorageConfigView
        orgConfig={orgConfig.data}
        orgLoading={orgConfig.isLoading}
        orgError={orgConfig.isError}
        onOrgRetry={() => void orgConfig.refetch()}
        onOrgSubmit={handleOrgSubmit}
        onOrgDelete={handleOrgDelete}
        globalConfig={globalConfig.data}
        globalLoading={globalConfig.isLoading}
        globalError={globalConfig.isError}
        onGlobalRetry={() => void globalConfig.refetch()}
        onGlobalSubmit={handleGlobalSubmit}
      />
    </AdminGate>
  )
}
