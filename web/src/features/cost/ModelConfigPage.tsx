import { useState } from "react"
import { useForm, useWatch } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"
import { z } from "zod"
import { Loader2, Eye, EyeOff } from "lucide-react"
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
import type { ListModelsInput, ListModelsResult } from "@/features/cost/api"

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
  // 拉取 provider 官方模型列表（可选；不传则不显示「拉取模型」按钮）。
  onListModels?: (input: ListModelsInput) => Promise<ListModelsResult>
  // 查看（解密回显）已存密钥（可选；仅编辑模式且配置含密钥时显示「查看密钥」按钮）。
  onRevealKey?: (id: string) => Promise<string>
}

// 模型配置（admin-only）：按 kind 分组配置表 + 创建表单。
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
      onListModels={onListModels}
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
            onListModels={onListModels}
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
                      onListModels={onListModels}
                      onRevealKey={onRevealKey}
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
  // 拉取 provider 官方模型列表（可选；不传则不显示「拉取模型」按钮）。
  onListModels?: (input: ListModelsInput) => Promise<ListModelsResult>
  // 查看（解密回显）已存密钥（可选；仅编辑模式且配置含密钥时显示「查看密钥」按钮）。
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
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [showApiKey, setShowApiKey] = useState(false)
  // 「查看密钥」：解密回显已存密钥到密钥框。revealing=请求中；revealError=失败/无密钥提示。
  const [revealing, setRevealing] = useState(false)
  const [revealError, setRevealError] = useState<string | null>(null)
  // 拉取到的官方模型列表（含拉取时的 provider，用于切 provider 后判失效）。
  const [live, setLive] = useState<(ListModelsResult & { provider: string }) | null>(null)
  const [listing, setListing] = useState(false)
  const isEdit = initial != null
  // catalog 里出现过的不重复 provider（保序）+ 末尾的 openai-compatible。
  const providers = [...new Set(catalog.map((e) => e.provider))]
  const {
    register,
    handleSubmit,
    setValue,
    getValues,
    control,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      provider: initial?.provider ?? providers[0] ?? COMPATIBLE_PROVIDER,
      kind: initial?.kind ?? "image",
      model: initial?.model ?? "",
      baseUrl: initial?.baseUrl ?? "",
      apiKey: initial?.apiKey ?? "",
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

  // 「拉取模型」：用当前表单的 provider/baseUrl/apiKey 调官方接口；编辑时传 configId
  // 让后端复用已存密钥（apiKey 留空）。失败/不支持时后端回退静态目录并带 error。
  async function fetchModels() {
    if (!onListModels) return
    const v = getValues()
    setListing(true)
    try {
      const res = await onListModels({
        provider: v.provider,
        baseUrl: v.baseUrl.trim() || undefined,
        apiKey: v.apiKey || undefined,
        configId: initial?.id,
      })
      setLive({ ...res, provider: v.provider })
    } catch {
      setLive({
        models: [],
        source: "catalog",
        error: "拉取失败，请检查 base_url / API key",
        provider: v.provider,
      })
    } finally {
      setListing(false)
    }
  }

  // 「查看密钥」：调 reveal 接口解密回显已存密钥，写入密钥框并切为明文显示。
  // 仅编辑模式且配置含密钥时可用；空 key（未存独立密钥）给出提示而非静默。
  async function revealKey() {
    if (!onRevealKey || !initial) return
    setRevealing(true)
    setRevealError(null)
    try {
      const key = await onRevealKey(initial.id)
      if (key) {
        setValue("apiKey", key)
        setShowApiKey(true)
      } else {
        setRevealError("该配置未存独立密钥（回退服务端 env 密钥）。")
      }
    } catch {
      setRevealError("查看密钥失败，请重试。")
    } finally {
      setRevealing(false)
    }
  }

  // 切 provider 后旧的拉取结果失效（按 render 派生，避免 effect 里 setState）。
  const activeLive = live && live.provider === provider ? live : null
  // 下拉/快捷填充的候选：只有 live 成功（source="live"）才用拉到的列表；
  // 失败/不支持时回退到按 kind 过滤过的静态建议，避免把 image/video 漏到 text 下拉。
  const isLive = activeLive?.source === "live"
  const liveModels = activeLive?.models ?? []
  const optionModels = isLive && liveModels.length > 0
    ? liveModels
    : suggestions.map((s) => s.model)

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
  // 分区小标题：靠分隔线+字距把表单切成「基础 / 凭证与端点 / 选项 / 高级」四段。
  const headingClass =
    "text-[11px] font-semibold tracking-[0.08em] text-text-3"

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
      <p className={headingClass}>基础</p>
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
        <div className="flex items-center gap-2">
          <input
            id="mc-model"
            list={optionModels.length > 0 ? "mc-model-suggestions" : undefined}
            aria-invalid={errors.model != null}
            placeholder={isCompatible ? "如 deepseek-chat" : "如 gpt-4o-mini"}
            {...register("model")}
            className={selectClass + " flex-1"}
          />
          {onListModels && (
            <UiButton
              type="button"
              variant="outline"
              size="sm"
              disabled={listing}
              onClick={() => void fetchModels()}
              aria-label="从官方接口拉取模型列表"
            >
              {listing ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                "拉取模型"
              )}
            </UiButton>
          )}
        </div>
        {optionModels.length > 0 && (
          <datalist id="mc-model-suggestions">
            {optionModels.map((m) => (
              <option key={m} value={m} />
            ))}
          </datalist>
        )}
        {optionModels.length > 0 && (
          <p className="flex flex-wrap gap-1.5 text-[11.5px] text-text-3">
            {isLive ? "官方模型：" : "常用："}
            {optionModels.slice(0, 24).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setValue("model", m, { shouldValidate: true })}
                className="rounded border border-line px-1.5 py-0.5 text-text-2 hover:text-text-1"
              >
                {m}
              </button>
            ))}
          </p>
        )}
        {activeLive?.source === "live" && (
          <p className="text-[11.5px] text-review">
            已从官方接口拉取 {liveModels.length} 个模型。
          </p>
        )}
        {activeLive && activeLive.source === "catalog" && (
          <div className="flex flex-col gap-0.5 text-[11.5px] text-text-3">
            <span>
              {activeLive.message ?? activeLive.error ?? "未能从官方接口拉取"}，已回退建议列表。
            </span>
            {activeLive.hint && (
              <span className="text-text-2">{activeLive.hint}</span>
            )}
          </div>
        )}
        {errors.model && (
          <p className="text-[12px] text-danger">{errors.model.message}</p>
        )}
        {hasUnavailableSuggestion && !activeLive && (
          <p className="text-[11.5px] text-text-3">
            该 provider 未配置服务端密钥，请在下方填写 API key。
          </p>
        )}
      </div>

      <p className={`${headingClass} mt-1 border-t border-line pt-4`}>
        凭证与端点
      </p>

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
        <div className="relative flex items-center w-full">
          <input
            id="mc-apikey"
            type={showApiKey ? "text" : "password"}
            autoComplete="off"
            placeholder="sk-..."
            {...register("apiKey")}
            className={`${selectClass} w-full pr-10`}
          />
          <button
            type="button"
            onClick={() => setShowApiKey(!showApiKey)}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-text-3 hover:text-text-1 focus:outline-none"
          >
            {showApiKey ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </button>
        </div>
        {isEdit && initial?.hasApiKey && onRevealKey && (
          <div className="flex items-center gap-2">
            <UiButton
              type="button"
              variant="outline"
              size="sm"
              disabled={revealing}
              onClick={() => void revealKey()}
              aria-label="查看密钥（解密回显已存密钥）"
            >
              {revealing ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                "查看密钥"
              )}
            </UiButton>
            {revealError && (
              <span className="text-[11.5px] text-text-3">{revealError}</span>
            )}
          </div>
        )}
        <p className="text-[11.5px] text-text-3">
          {isEdit && initial?.hasApiKey
            ? "留空保持不变（已配置密钥）；填写则替换为新密钥；点「查看密钥」可解密回显。"
            : "密钥仅写入、加密存储，不会回显；留空则回退服务端 env 密钥。"}
        </p>
      </div>

      <p className={`${headingClass} mt-1 border-t border-line pt-4`}>选项</p>

      <div className="grid grid-cols-2 gap-3">
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
      </div>

      {/* 高级（默认折叠）：params 等不常用项收进来，让默认视图只剩基础/凭证/选项。
          用原生 <details> 而非条件渲染——折叠态内容仍在 DOM，校验与测试不受影响。 */}
      <details className="mt-1 border-t border-line pt-4">
        <summary className={`${headingClass} cursor-pointer select-none`}>
          高级设置
        </summary>
        <div className="mt-3 flex flex-col gap-1.5">
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
      </details>

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
