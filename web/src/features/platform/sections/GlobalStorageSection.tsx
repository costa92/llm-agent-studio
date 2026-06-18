import { toast } from "sonner"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { StorageConfigForm } from "@/features/storage/StorageConfigPage"
import { storageConfigErrorMessage } from "@/features/storage/configError"
import type { StorageConfig, UpsertStorageConfigInput } from "@/lib/types"
import {
  useGlobalStorageConfig,
  useUpsertGlobalStorageConfig,
} from "@/features/storage/api"

// ── 全局存储配置 ────────────────────────────────────────────────
export function GlobalStorageSection() {
  const globalConfig = useGlobalStorageConfig()
  const upsertGlobal = useUpsertGlobalStorageConfig()

  // 返回 Promise 让表单 await；成功 toast 在 then、失败 toast（含 400 缺加密密钥）在 catch。
  function handleSubmit(
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

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-line bg-bg-surface p-5">
      <header className="flex flex-col gap-1">
        <h2 className="font-heading text-[15px] font-semibold text-text-1">全局存储配置</h2>
        <p className="text-[12px] text-text-3">
          所有未单独配置的组织共用；修改影响全局。密钥仅写入、加密存储，不会回显。
        </p>
      </header>

      {globalConfig.isError ? (
        <div className="flex flex-col items-center gap-3 py-10 text-center">
          <p className="text-text-2">全局存储配置加载失败</p>
          <Button variant="ghost" onClick={() => void globalConfig.refetch()}>
            重试
          </Button>
        </div>
      ) : globalConfig.isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      ) : (
        // key 绑 config 同一性：刷新后基线随之重置。
        <StorageConfigForm
          key={globalConfig.data?.id ?? "empty"}
          initial={globalConfig.data}
          onSubmit={handleSubmit}
          isOrgScope={false}
        />
      )}
    </section>
  )
}
