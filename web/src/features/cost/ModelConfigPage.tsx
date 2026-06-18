import { useState } from "react"
import { FormProvider, useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { Loader2 } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { Badge } from "@/components/studio/Badge"
import {
  CrudResourcePage,
  DataView,
  FormDialog,
  ConfirmDialog,
  useCrudResource,
} from "@/features/common/crud"
import type {
  CatalogEntry,
  CreateModelConfigInput,
  ModelConfig,
} from "@/lib/types"
import type { ListModelsInput, ListModelsResult } from "@/features/cost/api"
import {
  ModelConfigFields,
  KIND_LABELS,
  DEFERRED_KINDS,
  defaultsFor,
  parseParamsText,
  providersFor,
  formSchema,
  type FormValues,
} from "./ModelConfigFields"

// FormValues → CreateModelConfigInput（params 解析、空 base_url/apiKey 省略）。
// 非法 params JSON → parseParamsText 抛 Error（调用方按需兜底成 submitError）。
function toInput(values: FormValues): CreateModelConfigInput {
  const params = parseParamsText(values.paramsText)
  const baseUrl = values.baseUrl.trim() || undefined
  const apiKey = values.apiKey || undefined
  return {
    kind: values.kind,
    provider: values.provider,
    model: values.model.trim(),
    baseUrl,
    apiKey,
    enabled: values.enabled,
    isDefault: values.isDefault,
    params,
  }
}

export interface ModelConfigViewProps {
  configs: ModelConfig[] | undefined
  catalog: CatalogEntry[] | undefined
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  // 创建（提交 → Promise<ModelConfig>；密钥型 param → 400 由调用方 toast）。
  onCreate: (input: CreateModelConfigInput) => Promise<ModelConfig>
  // 编辑（按 id 更新 → Promise<ModelConfig>；apiKey 留空则后端保留既有密钥）。
  onUpdate: (id: string, input: CreateModelConfigInput) => Promise<ModelConfig>
  // 删除（按 id；确认后调用）。
  onDelete: (id: string) => Promise<void>
  // 拉取 provider 官方模型列表（可选；不传则不显示「拉取模型」按钮）。
  onListModels?: (input: ListModelsInput) => Promise<ListModelsResult>
  // 查看（解密回显）已存密钥（可选；仅编辑模式且配置含密钥时显示「显示已存」按钮）。
  onRevealKey?: (id: string) => Promise<string>
}

// 模型配置（admin-only）：按 kind 分组卡片 + 创建/编辑对话框。迁移到 CRUD 框架。
export function ModelConfigView({
  configs,
  catalog,
  isLoading,
  isError,
  onRetry,
  onCreate,
  onUpdate,
  onDelete,
  onListModels,
  onRevealKey,
}: ModelConfigViewProps) {
  const cat = catalog ?? []
  const items = configs ?? []

  const crud = useCrudResource<ModelConfig>({
    getId: (c) => c.id,
    create: (v) => onCreate(toInput(v as FormValues)),
    update: (id, v) => onUpdate(id, toInput(v as FormValues)),
    remove: (id) => onDelete(id),
    errorMessage: (_action, err) =>
      // 本地 params JSON 校验 → 显示其文案；其它后端错误由调用方 toast，这里兜底。
      err instanceof Error &&
      (err.message === "参数必须是 JSON 对象" ||
        err.message === "参数不是合法 JSON")
        ? err.message
        : "保存失败，请检查参数",
  })

  const editingTarget = crud.dialog?.mode === "edit" ? crud.dialog.target : null
  const dialogDefaultValues = defaultsFor(editingTarget, providersFor(cat))

  const emptyState = (
    <div className="flex flex-col items-center gap-3 py-20 text-center">
      <p className="text-text-1">尚未配置模型</p>
      <Button variant="amber" onClick={crud.openCreate}>
        添加第一个模型
      </Button>
    </div>
  )

  const loadingSkeleton = (
    <div className="flex flex-col gap-3">
      {Array.from({ length: 3 }).map((_, i) => (
        <Skeleton key={i} className="h-12 rounded-lg" />
      ))}
    </div>
  )

  return (
    <>
      <CrudResourcePage
        title="模型配置"
        description="可选用内置 provider 或 OpenAI 兼容端点；API key 仅写入、加密存储，不会回显。未填写密钥的配置回退服务端 env 密钥。"
        createLabel="添加模型"
        onCreate={isLoading ? undefined : crud.openCreate}
        isLoading={isLoading}
        isError={isError}
        onRetry={onRetry}
        isEmpty={items.length === 0}
        emptyState={emptyState}
        loadingSkeleton={loadingSkeleton}
      >
        <DataView<ModelConfig>
          layout="cards"
          items={items}
          getId={(c) => c.id}
          gridClassName="grid grid-cols-1 gap-4 md:grid-cols-2"
          // kind → 本地化标签作为分组 key（DataView 默认渲染 key 文本，故此处直接给中文
          // 标签才能保留「图像/文本/视频…」的视觉；二期 kind 追加「· 二期」）。
          groupBy={(c) =>
            (KIND_LABELS[c.kind] ?? c.kind) +
            (DEFERRED_KINDS.has(c.kind) ? " · 二期" : "")
          }
          rowActions={[
            {
              key: "edit",
              label: "编辑",
              variant: "ghost",
              ariaLabel: (c) => `编辑 ${c.provider} ${c.model}`,
              onClick: (c) => crud.openEdit(c),
            },
            {
              key: "delete",
              label: "删除",
              variant: "ghost",
              ariaLabel: (c) => `删除 ${c.provider} ${c.model}`,
              onClick: (c) => crud.requestDelete(c),
            },
          ]}
          renderCard={(c, actions) => (
            <div className="flex items-center justify-between rounded-xl border border-line bg-bg-surface px-4 py-2.5 text-[12.5px]">
              <span className="flex flex-col gap-0.5">
                <span className="text-text-1">
                  {c.provider} · {c.model}
                </span>
                {c.baseUrl && (
                  <span className="text-[11px] text-text-3">{c.baseUrl}</span>
                )}
              </span>
              <span className="flex items-center gap-2">
                <Badge variant={c.hasApiKey ? "done" : "pending"}>
                  {c.hasApiKey ? "已配置密钥" : "用服务端密钥"}
                </Badge>
                {c.isDefault && <Badge variant="done">默认</Badge>}
                <Badge variant={c.enabled ? "running" : "pending"}>
                  {c.enabled ? "已启用" : "已停用"}
                </Badge>
                {actions}
              </span>
            </div>
          )}
        />
      </CrudResourcePage>

      {/* 创建 / 编辑对话框 — 挂载在 CrudResourcePage 外部，确保空态下也可渲染。 */}
      <FormDialog<FormValues>
        open={crud.dialog != null}
        mode={crud.dialog?.mode ?? "create"}
        title={crud.dialog?.mode === "edit" ? "编辑模型配置" : "添加模型配置"}
        description={
          crud.dialog?.mode === "edit"
            ? "修改 provider / 类型 / model / base_url 等；API key 留空保持不变，填写则替换。"
            : "选择 provider（或 OpenAI 兼容端点）与类型，填写 model；可选填 base_url 与 API key（仅写入、加密存储）。"
        }
        contentClassName="max-w-2xl"
        schema={formSchema}
        defaultValues={dialogDefaultValues}
        resetKey={editingTarget?.id ?? "create"}
        submitLabel="保存"
        submitting={crud.submitting}
        submitError={crud.submitError}
        onSubmit={(values) => void crud.submit(values)}
        onOpenChange={(open) => {
          if (!open) crud.closeDialog()
        }}
      >
        <ModelConfigFields
          catalog={cat}
          initial={editingTarget ?? undefined}
          onListModels={onListModels}
          onRevealKey={onRevealKey}
        />
      </FormDialog>

      {/* 删除确认对话框（mirror 退回确认）：仅「确认删除」才调 onDelete。 */}
      <ConfirmDialog
        open={crud.deleteTarget != null}
        title="确认删除该模型配置？"
        description={
          crud.deleteTarget
            ? `删除 ${crud.deleteTarget.provider} · ${crud.deleteTarget.model} 后无法撤销。确认要删除吗？`
            : ""
        }
        confirmLabel="确认删除"
        confirming={crud.deleting}
        onConfirm={crud.confirmDelete}
        onCancel={crud.cancelDelete}
      />
    </>
  )
}

// ── 独立创建/编辑表单 + 对话框（供组件级测试 & 复用）────────────────────
// CreateModelConfigForm 自带 rhf FormProvider，可独立渲染（不依赖外层对话框）。

export interface CreateModelConfigFormProps {
  catalog: CatalogEntry[]
  onCreate: (input: CreateModelConfigInput) => Promise<ModelConfig>
  onSuccess?: (mc: ModelConfig) => void
  // 编辑模式：传入既有配置 → 表单预填；API key 留空则提交时省略（后端保留既有密钥）。
  initial?: ModelConfig
  onListModels?: (input: ListModelsInput) => Promise<ListModelsResult>
  onRevealKey?: (id: string) => Promise<string>
}

export function CreateModelConfigForm({
  catalog,
  onCreate,
  onSuccess,
  initial,
  onListModels,
  onRevealKey,
}: CreateModelConfigFormProps) {
  const providers = providersFor(catalog)
  const form = useForm<FormValues>({
    resolver: zodResolver(formSchema) as never,
    defaultValues: defaultsFor(initial, providers),
  })
  const {
    handleSubmit,
    setError,
    formState: { errors, isSubmitting },
  } = form

  const submit = handleSubmit(async (values) => {
    let input: CreateModelConfigInput
    try {
      input = toInput(values)
    } catch (err) {
      // 非法 params JSON → 本地拦下（不打后端）。
      setError("root", {
        message: err instanceof Error ? err.message : "参数不是合法 JSON",
      })
      return
    }
    try {
      const mc = await onCreate(input)
      onSuccess?.(mc)
    } catch (err) {
      // 后端 400 等 → 调用方 toast；此处兜底文案。
      setError("root", {
        message: err instanceof Error ? "保存失败，请检查参数" : "保存失败",
      })
    }
  })

  return (
    <FormProvider {...form}>
      <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <ModelConfigFields
          catalog={catalog}
          initial={initial}
          onListModels={onListModels}
          onRevealKey={onRevealKey}
        />
        {errors.root?.message && (
          <p role="alert" className="text-[12px] text-danger">
            {errors.root.message}
          </p>
        )}
        <DialogFooter>
          <Button type="submit" variant="amber" disabled={isSubmitting}>
            {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            保存
          </Button>
        </DialogFooter>
      </form>
    </FormProvider>
  )
}

export interface CreateModelConfigDialogProps extends CreateModelConfigFormProps {
  trigger: React.ReactNode
}

export function CreateModelConfigDialog({
  trigger,
  onSuccess,
  ...formProps
}: CreateModelConfigDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>添加模型配置</DialogTitle>
          <DialogDescription>
            选择 provider（或 OpenAI 兼容端点）与类型，填写 model；可选填 base_url 与 API
            key（仅写入、加密存储）。
          </DialogDescription>
        </DialogHeader>
        <CreateModelConfigForm
          {...formProps}
          onSuccess={(mc) => {
            setOpen(false)
            onSuccess?.(mc)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}

export interface EditModelConfigDialogProps {
  config: ModelConfig
  catalog: CatalogEntry[]
  // 按 id 更新（apiKey 留空 → 后端保留既有密钥）。
  onUpdate: (id: string, input: CreateModelConfigInput) => Promise<ModelConfig>
  trigger: React.ReactNode
  onSuccess?: (mc: ModelConfig) => void
  onListModels?: (input: ListModelsInput) => Promise<ListModelsResult>
  onRevealKey?: (id: string) => Promise<string>
}

// 编辑弹窗：复用创建表单，预填既有配置；提交走 onUpdate(id, input)。
export function EditModelConfigDialog({
  config,
  catalog,
  onUpdate,
  trigger,
  onSuccess,
  onListModels,
  onRevealKey,
}: EditModelConfigDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑模型配置</DialogTitle>
          <DialogDescription>
            修改 provider / 类型 / model / base_url 等；API key 留空保持不变，填写则替换。
          </DialogDescription>
        </DialogHeader>
        <CreateModelConfigForm
          catalog={catalog}
          initial={config}
          onCreate={(input) => onUpdate(config.id, input)}
          onListModels={onListModels}
          onRevealKey={onRevealKey}
          onSuccess={(mc) => {
            setOpen(false)
            onSuccess?.(mc)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
