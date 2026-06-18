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
import { modelConfigErrorMessage } from "./configError"
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
  // 创建（提交 → Promise<ModelConfig>；拒绝原样透传，错误映射/toast 由本组件 useCrudResource 处理）。
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
    labels: { created: "模型配置已保存", updated: "模型配置已更新", deleted: "模型配置已删除" },
    errorMessage: (_action, err) =>
      // 本地 params JSON 校验失败 → 原样显示；其余后端错误 → 复用 modelConfigErrorMessage 映射
      // （400 密钥型 param / 缺 provider+model / 未配置加密密钥等）。
      err instanceof Error &&
      (err.message === "参数必须是 JSON 对象" ||
        err.message === "参数不是合法 JSON")
        ? err.message
        : modelConfigErrorMessage(err),
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
          gridClassName="overflow-hidden rounded-xl border border-line bg-bg-surface [&>div:last-child>*]:border-b-0"
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
            <div className="flex items-center justify-between border-b border-line px-4 py-2.5 text-[12.5px]">
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
