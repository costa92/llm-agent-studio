import { useFormContext, useForm, FormProvider } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { useState } from "react"
import { Loader2 } from "lucide-react"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"
import { Badge } from "@/components/studio/Badge"
import { ApiError } from "@/lib/apiClient"
import {
  useStorageConfigs,
  useCreateStorageConfig,
  useUpdateStorageConfig,
  useDeleteStorageConfig,
  useSetDefaultStorageConfig,
} from "./api"
import type { StorageConfig, UpsertStorageConfigInput } from "@/lib/types"
import {
  useCrudResource,
  CrudResourcePage,
  DataView,
  FormDialog,
  ConfirmDialog,
} from "../common/crud"
import { StorageModeFields } from "./StorageModeFields"
import {
  formSchema,
  defaultsFor,
  MODE_LABELS,
  type FormValues,
} from "./StorageModeFields.schema"

// re-export so existing callers of `import { MODE_LABELS } from "./StorageConfigPage"` still work.
export { MODE_LABELS }

// ─── Shared field class ───────────────────────────────────────────────────────
const fieldClass =
  "rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"

// ─── StorageConfigForm ────────────────────────────────────────────────────────
// 保持导出：测试 + 平台管理员全局存储页直接使用此组件。
// 内部通过 FormProvider 注入 rhf 上下文，让 StorageModeFields 通过 useFormContext 读写。

export interface StorageConfigFormProps {
  // 既有配置（用于预填 + hasSecret 提示）；null = 尚未配置。
  initial: StorageConfig | null | undefined
  // 提交 → POST（新建）或 PUT（更新）；返回 Promise 让表单 await，400 由调用方 toast。
  onSubmit: (input: UpsertStorageConfigInput) => Promise<StorageConfig>
  // org 覆盖区显示「停用 = 回退全局」提示；全局区不显示。
  isOrgScope: boolean
  // id 前缀，避免同页多表单 id 冲突。默认 "form"。
  idPrefix?: string
}

// 单个存储配置表单：name + StorageModeFields（mode 下拉 + per-mode 条件字段 + enabled）。
// 使用 FormProvider 包装，让 StorageModeFields 通过 useFormContext 读写同一个 rhf 实例。
export function StorageConfigForm({ initial, onSubmit, isOrgScope, idPrefix = "form" }: StorageConfigFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const methods = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: defaultsFor(initial),
  })
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = methods

  const fid = (s: string) => `${idPrefix}-sc-${s}`

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const sc = await onSubmit({
        name: values.name.trim(),
        mode: values.mode,
        endpoint: values.endpoint.trim(),
        region: values.region.trim(),
        bucket: values.bucket.trim(),
        accessKeyId: values.accessKeyId.trim(),
        secret: values.secret, // 空 = 保留既有；后端据此不改 secret_enc。
        useSsl: values.useSsl,
        publicPrefix: values.publicPrefix.trim(),
        enabled: values.enabled,
      })
      // 提交成功后刷新表单基线（hasSecret 等由父组件 refetch 重渲染驱动）。
      void sc
    } catch (err) {
      // 后端 400（缺加密密钥 / 校验失败）等 → 优先透传后端 body，文案带具体原因
      // 比泛化「请检查参数」有用（例如 STUDIO_CONFIG_ENC_KEY 缺失这种只能后端告诉用户的）。
      if (err instanceof ApiError && err.body) {
        setSubmitError(`保存失败：${err.body}`)
      } else {
        setSubmitError(err instanceof Error ? "保存失败，请检查参数" : "保存失败")
      }
    }
  })

  return (
    <FormProvider {...methods}>
      <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <div className="flex flex-col gap-1.5">
          <label htmlFor={fid("name")} className="text-[13px] font-medium text-text-1">配置名称</label>
          <input
            id={fid("name")}
            placeholder="如 主存储桶"
            aria-invalid={errors.name != null}
            {...register("name")}
            className={fieldClass}
          />
          {errors.name && (
            <p className="text-[12px] text-danger">{errors.name.message}</p>
          )}
        </div>

        {/* mode-conditional 字段（含 mode 下拉 + per-mode 区块 + enabled）由 StorageModeFields 管理。 */}
        <StorageModeFields initial={initial} isOrgScope={isOrgScope} idPrefix={idPrefix} />

        {submitError && (
          <p role="alert" className="text-[12px] text-danger">
            {submitError}
          </p>
        )}

        <div className="flex justify-end">
          <Button type="submit" variant="amber" disabled={isSubmitting}>
            {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            保存
          </Button>
        </div>
      </form>
    </FormProvider>
  )
}

// 关键字段展示：按 mode 返回最具代表性的一个字段值。
function keyField(config: StorageConfig): string {
  switch (config.mode) {
    case "s3":
    case "oss":
    case "cos":
      return config.bucket
    case "github":
      return `${config.accessKeyId}/${config.bucket}`
    case "localfs":
      return config.publicPrefix || "—"
  }
}

// ─── FormDialog name field ────────────────────────────────────────────────────
// name 字段小组件：在 FormDialog 的 FormProvider 上下文中渲染，通过 useFormContext 读写。
// 分离出来避免 StorageModeFields 包含 name（name 不是 mode-conditional 的）。
function FormDialogNameField({ idPrefix = "dlg" }: { idPrefix?: string }) {
  const {
    register,
    formState: { errors },
  } = useFormContext<FormValues>()
  const fid = (s: string) => `${idPrefix}-sc-${s}`
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={fid("name")} className="text-[13px] font-medium text-text-1">配置名称</label>
      <input
        id={fid("name")}
        placeholder="如 主存储桶"
        aria-invalid={errors.name != null}
        {...register("name")}
        className={fieldClass}
      />
      {errors.name && (
        <p className="text-[12px] text-danger">{errors.name.message}</p>
      )}
    </div>
  )
}

// ─── StorageConfigView ────────────────────────────────────────────────────────
// 组织存储配置页（admin-only）：多配置列表 + 新增/编辑/删除/设为默认。
// 使用 CRUD 框架：CrudResourcePage + DataView(table) + FormDialog + ConfirmDialog。

export interface StorageConfigViewProps {
  org: string
}

export function StorageConfigView({ org }: StorageConfigViewProps) {
  const configsQuery = useStorageConfigs(org)
  const createMutation = useCreateStorageConfig(org)
  const updateMutation = useUpdateStorageConfig(org)
  const deleteMutation = useDeleteStorageConfig(org)
  const setDefaultMutation = useSetDefaultStorageConfig(org)

  const crud = useCrudResource<StorageConfig>({
    getId: (c) => c.id,
    create: (input) => createMutation.mutateAsync(input as UpsertStorageConfigInput),
    update: (id, input) =>
      updateMutation.mutateAsync({ id, input: input as UpsertStorageConfigInput }),
    remove: (id) => deleteMutation.mutateAsync(id),
    labels: { created: "存储配置已创建", updated: "存储配置已更新", deleted: "存储配置已删除" },
    errorMessage: (action, err) => {
      if (action === "delete" && err instanceof ApiError && err.status === 409) {
        return "该存储有历史素材引用，请改用停用"
      }
      return "操作失败，请重试"
    },
  })

  const editTarget = crud.dialog?.target ?? null
  const dialogDefaultValues = defaultsFor(editTarget)
  const dialogIdPrefix = crud.dialog?.mode === "create" ? "new" : (editTarget?.id ?? "edit")

  return (
    <>
      <CrudResourcePage
        title="存储配置"
        description="配置本组织专属的资产对象存储后端（本地磁盘 / S3 / 阿里云 OSS / 腾讯云 COS / GitHub 仓库）；未配置或停用时回退到全局默认。密钥仅写入、加密存储，不会回显。"
        createLabel="新增配置"
        onCreate={crud.openCreate}
        isLoading={configsQuery.isLoading}
        isError={configsQuery.isError}
        onRetry={() => void configsQuery.refetch()}
        isEmpty={(configsQuery.data ?? []).length === 0}
        emptyHint="暂无存储配置，点击「新增配置」开始。"
      >
        <DataView
          layout="table"
          items={configsQuery.data ?? []}
          getId={(c) => c.id}
          columns={[
            { key: "name", header: "名称", cell: (c) => c.name },
            { key: "mode", header: "类型", cell: (c) => MODE_LABELS[c.mode] },
            {
              key: "key",
              header: "关键字段",
              className: "font-mono text-[12px] text-text-2",
              cell: (c) => keyField(c),
            },
            {
              key: "enabled",
              header: "启用",
              cell: (c) => (
                <Badge variant={c.enabled ? "running" : "pending"}>
                  {c.enabled ? "已启用" : "已停用"}
                </Badge>
              ),
            },
            {
              key: "default",
              header: "默认",
              cell: (c) =>
                c.isDefault ? (
                  <Badge variant="done">默认</Badge>
                ) : (
                  <UiButton size="sm" onClick={() => setDefaultMutation.mutate(c.id)}>
                    设为默认
                  </UiButton>
                ),
            },
            {
              key: "secret",
              header: "密钥",
              cell: (c) => (c.hasSecret ? <Badge variant="done">已配置</Badge> : null),
            },
          ]}
          rowActions={[
            { key: "edit", label: "编辑", onClick: crud.openEdit },
            { key: "delete", label: "删除", variant: "destructive" as const, onClick: crud.requestDelete },
          ]}
        />
      </CrudResourcePage>

      {/* 新增/编辑 Dialog（FormDialog 内置 FormProvider + zodResolver）。 */}
      <FormDialog<FormValues>
        open={crud.dialog !== null}
        mode={crud.dialog?.mode ?? "create"}
        title={crud.dialog?.mode === "create" ? "新增存储配置" : "编辑存储配置"}
        description={
          crud.dialog?.mode === "create"
            ? "填写新存储配置的参数，密钥仅加密存储、不回显。"
            : "修改存储配置参数；留空密钥字段则保持不变。"
        }
        schema={formSchema}
        defaultValues={dialogDefaultValues}
        resetKey={editTarget?.id ?? "create"}
        submitting={crud.submitting}
        submitError={crud.submitError}
        onSubmit={(values) => void crud.submit(values)}
        onOpenChange={(open) => { if (!open) crud.closeDialog() }}
      >
        {/* name 字段（不在 StorageModeFields 中，需在 FormDialog children 里渲染）。 */}
        <FormDialogNameField idPrefix={dialogIdPrefix} />
        <StorageModeFields
          initial={editTarget}
          isOrgScope
          idPrefix={dialogIdPrefix}
        />
      </FormDialog>

      {/* 删除确认 Dialog。 */}
      <ConfirmDialog
        open={crud.deleteTarget !== null}
        title="确认删除存储配置？"
        description={`删除「${crud.deleteTarget?.name ?? ""}」后无法撤销。如该存储有历史素材引用，建议改用停用。`}
        confirmLabel="确认删除"
        variant="danger"
        confirming={crud.deleting}
        onConfirm={crud.confirmDelete}
        onCancel={crud.cancelDelete}
      />
    </>
  )
}
