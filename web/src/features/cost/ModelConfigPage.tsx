import { useState } from "react"
import { useForm, useWatch } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
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
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"
import { Button as UiButton } from "@/components/ui/button"
import { Badge } from "@/components/studio/Badge"
import type {
  CatalogEntry,
  CreateModelConfigInput,
  ModelConfig,
} from "@/lib/types"

// kind 标签：text（文本/对话）+ image/video/audio。
const KIND_LABELS: Record<string, string> = {
  text: "文本",
  image: "图像",
  video: "视频",
  audio: "音频",
  chat: "对话",
}
const DEFERRED_KINDS = new Set(["video", "audio"])

// 表单可选的 kind 顺序（chat 仅为兼容旧配置分组展示，不在创建表单暴露）。
const FORM_KINDS = ["image", "video", "audio", "text"] as const

// 自定义 OpenAI 兼容 provider：纯自由 model + 必填 base_url。
const COMPATIBLE_PROVIDER = "openai-compatible"
const COMPATIBLE_LABEL = "OpenAI 兼容 (自定义 base_url)"

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
}

// 模型配置（admin-only）：按 kind 分组配置表 + 创建表单。表单绝不含 API key 字段。
export function ModelConfigView({
  configs,
  catalog,
  isLoading,
  isError,
  onRetry,
  onCreate,
  onUpdate,
  onDelete,
}: ModelConfigViewProps) {
  // 删除确认弹窗：保存待删除配置；null = 弹窗关闭（mirror 退回确认模式）。
  const [deleteTarget, setDeleteTarget] = useState<ModelConfig | null>(null)
  if (isError) {
    return (
      <div className="flex flex-col items-center gap-3 py-20 text-center">
        <p className="text-text-2">模型配置加载失败</p>
        <Button variant="ghost" onClick={onRetry}>
          重试
        </Button>
      </div>
    )
  }

  // 按 kind 分组。
  const groups = new Map<string, ModelConfig[]>()
  for (const c of configs ?? []) {
    const arr = groups.get(c.kind) ?? []
    arr.push(c)
    groups.set(c.kind, arr)
  }

  const addButton = (
    <CreateModelConfigDialog
      catalog={catalog ?? []}
      onCreate={onCreate}
      trigger={<Button variant="amber">添加模型</Button>}
    />
  )

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="font-heading text-[22px] font-bold text-text-1">模型配置</h1>
        {!isLoading && (configs?.length ?? 0) > 0 && addButton}
      </header>

      <p className="text-[12px] text-text-3">
        可选用内置 provider 或 OpenAI 兼容端点；API key 仅写入、加密存储，不会回显。
        未填写密钥的配置回退服务端 env 密钥。
      </p>

      {isLoading ? (
        <div className="flex flex-col gap-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-12 rounded-lg" />
          ))}
        </div>
      ) : (configs?.length ?? 0) === 0 ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-1">尚未配置模型</p>
          <CreateModelConfigDialog
            catalog={catalog ?? []}
            onCreate={onCreate}
            trigger={<Button variant="amber">添加第一个模型</Button>}
          />
        </div>
      ) : (
        [...groups.entries()].map(([kind, list]) => (
          <section key={kind} className="flex flex-col gap-2">
            <h2 className="flex items-center gap-2 text-[11.5px] font-semibold tracking-[0.08em] text-text-3">
              {KIND_LABELS[kind] ?? kind}
              {DEFERRED_KINDS.has(kind) && (
                <span className="font-normal text-text-3">· 二期</span>
              )}
            </h2>
            <div className="overflow-hidden rounded-xl border border-line bg-bg-surface">
              {list.map((c) => (
                <div
                  key={c.id}
                  className="flex items-center justify-between border-b border-line px-4 py-2.5 last:border-b-0 text-[12.5px]"
                >
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
                    <EditModelConfigDialog
                      config={c}
                      catalog={catalog ?? []}
                      onUpdate={onUpdate}
                      trigger={
                        <UiButton variant="ghost" size="sm" aria-label={`编辑 ${c.provider} ${c.model}`}>
                          编辑
                        </UiButton>
                      }
                    />
                    <UiButton
                      variant="ghost"
                      size="sm"
                      aria-label={`删除 ${c.provider} ${c.model}`}
                      onClick={() => setDeleteTarget(c)}
                    >
                      删除
                    </UiButton>
                  </span>
                </div>
              ))}
            </div>
          </section>
        ))
      )}

      {/* 删除确认弹窗（mirror 退回确认）：仅「确认删除」才调 onDelete；「取消」零副作用。 */}
      <Dialog
        open={deleteTarget != null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null)
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认删除该模型配置？</DialogTitle>
            <DialogDescription>
              {deleteTarget
                ? `删除 ${deleteTarget.provider} · ${deleteTarget.model} 后无法撤销。确认要删除吗？`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <UiButton variant="outline" onClick={() => setDeleteTarget(null)}>
              取消
            </UiButton>
            <UiButton
              variant="destructive"
              onClick={() => {
                const id = deleteTarget?.id
                setDeleteTarget(null)
                if (id) void onDelete(id)
              }}
            >
              确认删除
            </UiButton>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

// rhf+zod 创建表单（BYO key）：provider（含 openai-compatible）+ kind + 自由 model
// + 可选 base_url + 可选 API key（写入即加密、永不回显）+ enabled/isDefault + params JSON。
// 校验：openai-compatible 必填 base_url + model（兼容端点离不开 base_url）。
const formSchema = z
  .object({
    provider: z.string().min(1, "请选择 provider"),
    kind: z.string().min(1, "请选择类型"),
    model: z.string().trim().min(1, "请填写 model"),
    baseUrl: z.string().trim(),
    apiKey: z.string(),
    enabled: z.boolean(),
    isDefault: z.boolean(),
    // 可选 params JSON 文本。空 = 不带 params；非法 JSON 校验报错。
    paramsText: z.string(),
  })
  .refine(
    (v) => v.provider !== COMPATIBLE_PROVIDER || v.baseUrl.length > 0,
    { path: ["baseUrl"], message: "请填写 Base URL（OpenAI 兼容端点必填）" },
  )

type FormValues = z.infer<typeof formSchema>

// catalog 里某 provider 下某 kind 的「常用模型」建议（快速填充 model 输入）。
function suggestionsFor(
  catalog: CatalogEntry[],
  provider: string,
  kind: string,
): CatalogEntry[] {
  if (provider === COMPATIBLE_PROVIDER) return []
  return catalog.filter((e) => e.provider === provider && e.kind === kind)
}

export interface CreateModelConfigFormProps {
  catalog: CatalogEntry[]
  onCreate: (input: CreateModelConfigInput) => Promise<ModelConfig>
  onSuccess?: (mc: ModelConfig) => void
  // 编辑模式：传入既有配置 → 表单预填 provider/kind/model/baseUrl/params；
  // API key 留空（hasApiKey 时提示「留空保持不变」），提交时空则后端保留既有密钥。
  initial?: ModelConfig
}

export function CreateModelConfigForm({
  catalog,
  onCreate,
  onSuccess,
  initial,
}: CreateModelConfigFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const isEdit = initial != null
  // catalog 里出现过的不重复 provider（保序）+ 末尾的 openai-compatible。
  const providers = [...new Set(catalog.map((e) => e.provider))]
  const {
    register,
    handleSubmit,
    setValue,
    control,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      provider: initial?.provider ?? providers[0] ?? COMPATIBLE_PROVIDER,
      kind: initial?.kind ?? "image",
      model: initial?.model ?? "",
      baseUrl: initial?.baseUrl ?? "",
      apiKey: "", // 编辑模式始终留空：空 = 保留既有密钥。
      enabled: initial?.enabled ?? true,
      isDefault: initial?.isDefault ?? false,
      paramsText: initial?.params ? JSON.stringify(initial.params) : "",
    },
  })

  const provider = useWatch({ control, name: "provider" })
  const kind = useWatch({ control, name: "kind" })
  const enabled = useWatch({ control, name: "enabled" })
  const isDefault = useWatch({ control, name: "isDefault" })

  const suggestions = suggestionsFor(catalog, provider, kind)
  // 当前 provider+kind 的建议里是否有「服务端未配置密钥」的条目（仅信息提示，不阻塞）。
  const hasUnavailableSuggestion = suggestions.some((e) => !e.available)
  const isCompatible = provider === COMPATIBLE_PROVIDER
  // 本地 Ollama：base_url 缺省 http://localhost:11434，无需 API key。
  const isOllama = provider === "ollama"

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)

    // params JSON 解析（空 = 不带）；非法 → 本地拦下（不打后端）。
    let params: Record<string, unknown> | undefined
    const text = values.paramsText.trim()
    if (text !== "") {
      try {
        const parsed: unknown = JSON.parse(text)
        if (typeof parsed !== "object" || parsed == null || Array.isArray(parsed)) {
          setSubmitError("参数必须是 JSON 对象")
          return
        }
        params = parsed as Record<string, unknown>
      } catch {
        setSubmitError("参数不是合法 JSON")
        return
      }
    }

    // 空 base_url / apiKey → 省略（undefined），避免发成 ""。
    const baseUrl = values.baseUrl.trim() || undefined
    const apiKey = values.apiKey || undefined

    try {
      const mc = await onCreate({
        kind: values.kind,
        provider: values.provider,
        model: values.model.trim(),
        baseUrl,
        apiKey,
        enabled: values.enabled,
        isDefault: values.isDefault,
        params,
      })
      onSuccess?.(mc)
    } catch (err) {
      // 后端 400（缺密钥加密 / 缺 provider·model / 含密钥型 param）等 → 调用方 toast；此处兜底文案。
      setSubmitError(err instanceof Error ? "保存失败，请检查参数" : "保存失败")
    }
  })

  const selectClass =
    "rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="mc-provider">Provider</Label>
          <select id="mc-provider" {...register("provider")} className={selectClass}>
            {providers.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
            <option value={COMPATIBLE_PROVIDER}>{COMPATIBLE_LABEL}</option>
          </select>
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="mc-kind">类型</Label>
          <select id="mc-kind" {...register("kind")} className={selectClass}>
            {FORM_KINDS.map((k) => (
              <option key={k} value={k}>
                {KIND_LABELS[k]}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="mc-model">模型 (model)</Label>
        <input
          id="mc-model"
          list={suggestions.length > 0 ? "mc-model-suggestions" : undefined}
          aria-invalid={errors.model != null}
          placeholder={isCompatible ? "如 deepseek-chat" : "如 gpt-4o-mini"}
          {...register("model")}
          className={selectClass}
        />
        {suggestions.length > 0 && (
          <datalist id="mc-model-suggestions">
            {suggestions.map((e) => (
              <option key={e.model} value={e.model}>
                {e.label}
              </option>
            ))}
          </datalist>
        )}
        {suggestions.length > 0 && (
          <p className="flex flex-wrap gap-1.5 text-[11.5px] text-text-3">
            常用：
            {suggestions.map((e) => (
              <button
                key={e.model}
                type="button"
                onClick={() => setValue("model", e.model, { shouldValidate: true })}
                className="rounded border border-line px-1.5 py-0.5 text-text-2 hover:text-text-1"
              >
                {e.model}
              </button>
            ))}
          </p>
        )}
        {errors.model && (
          <p className="text-[12px] text-danger">{errors.model.message}</p>
        )}
        {hasUnavailableSuggestion && (
          <p className="text-[11.5px] text-text-3">
            该 provider 未配置服务端密钥，请在下方填写 API key。
          </p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="mc-baseurl">
          {isCompatible ? "Base URL（必填）" : "Base URL（可选）"}
        </Label>
        <input
          id="mc-baseurl"
          aria-invalid={errors.baseUrl != null}
          placeholder={isOllama ? "http://localhost:11434（缺省）" : "https://api.example.com/v1"}
          {...register("baseUrl")}
          className={selectClass}
        />
        {isOllama && (
          <p className="text-[11.5px] text-text-3">
            本地 Ollama：留空则用 http://localhost:11434，无需 API key。
          </p>
        )}
        {errors.baseUrl && (
          <p className="text-[12px] text-danger">{errors.baseUrl.message}</p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="mc-apikey">API Key（可选）</Label>
        <input
          id="mc-apikey"
          type="password"
          autoComplete="off"
          placeholder="sk-..."
          {...register("apiKey")}
          className={selectClass}
        />
        <p className="text-[11.5px] text-text-3">
          {isEdit && initial?.hasApiKey
            ? "留空保持不变（已配置密钥）；填写则替换为新密钥。"
            : "密钥仅写入、加密存储，不会回显；留空则回退服务端 env 密钥。"}
        </p>
      </div>

      <label className="flex items-center gap-2 text-[13px] text-text-1">
        <Checkbox
          checked={enabled}
          onCheckedChange={(v) => setValue("enabled", v === true)}
        />
        启用该模型
      </label>

      <label className="flex items-center gap-2 text-[13px] text-text-1">
        <Checkbox
          checked={isDefault}
          onCheckedChange={(v) => setValue("isDefault", v === true)}
        />
        设为该类型默认
      </label>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="mc-params">参数（可选 JSON）</Label>
        <Textarea
          id="mc-params"
          rows={3}
          placeholder={'例如 {"size":"1024x1024"}'}
          className="font-mono text-[12px]"
          {...register("paramsText")}
        />
        <p className="text-[11.5px] text-text-3">
          参数中请勿包含 API key / secret 等密钥字段，密钥请填上方密钥字段。
        </p>
      </div>

      {submitError && (
        <p role="alert" className="text-[12px] text-danger">
          {submitError}
        </p>
      )}

      <DialogFooter>
        <Button type="submit" variant="amber" disabled={isSubmitting}>
          {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
          保存
        </Button>
      </DialogFooter>
    </form>
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
      <DialogContent>
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
}

// 编辑弹窗：复用创建表单，预填既有配置；提交走 onUpdate(id, input)。
export function EditModelConfigDialog({
  config,
  catalog,
  onUpdate,
  trigger,
  onSuccess,
}: EditModelConfigDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent>
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
          onSuccess={(mc) => {
            setOpen(false)
            onSuccess?.(mc)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
