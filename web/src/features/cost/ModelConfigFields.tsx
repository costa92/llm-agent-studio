import { useState } from "react"
import { Controller, useFormContext } from "react-hook-form"
import { z } from "zod"
import { Loader2 } from "lucide-react"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox } from "@/components/ui/checkbox"
import { Button as UiButton } from "@/components/ui/button"
import { RevealSecretInput } from "@/features/common/crud"
import type { CatalogEntry, ModelConfig } from "@/lib/types"
import type { ListModelsInput, ListModelsResult } from "@/features/cost/api"

// kind 标签：text（文本/对话）+ image/video/audio。
export const KIND_LABELS: Record<string, string> = {
  text: "文本",
  image: "图像",
  video: "视频",
  audio: "音频",
  chat: "对话",
}
export const DEFERRED_KINDS = new Set(["video", "audio"])

// 表单可选的 kind 顺序（chat 仅为兼容旧配置分组展示，不在创建表单暴露）。
const FORM_KINDS = ["image", "video", "audio", "text"] as const

// 自定义 OpenAI 兼容 provider：纯自由 model + 必填 base_url。
export const COMPATIBLE_PROVIDER = "openai-compatible"
const COMPATIBLE_LABEL = "OpenAI 兼容 (自定义 base_url)"

// rhf+zod 创建表单（BYO key）：provider（含 openai-compatible）+ kind + 自由 model
// + 可选 base_url + 可选 API key（写入即加密、永不回显）+ enabled/isDefault + params JSON。
// 校验：openai-compatible 必填 base_url + model（兼容端点离不开 base_url）。
export const formSchema = z
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

export type FormValues = z.infer<typeof formSchema>

// catalog 里出现过的不重复 provider（保序）。
export function providersFor(catalog: CatalogEntry[]): string[] {
  return [...new Set(catalog.map((e) => e.provider))]
}

// initial 配置 → 表单默认值。
export function defaultsFor(
  initial: ModelConfig | null | undefined,
  providers: string[],
): FormValues {
  return {
    provider: initial?.provider ?? providers[0] ?? COMPATIBLE_PROVIDER,
    kind: initial?.kind ?? "image",
    model: initial?.model ?? "",
    baseUrl: initial?.baseUrl ?? "",
    apiKey: initial?.apiKey ?? "",
    enabled: initial?.enabled ?? true,
    isDefault: initial?.isDefault ?? false,
    paramsText: initial?.params ? JSON.stringify(initial.params) : "",
  }
}

// catalog 里某 provider 下某 kind 的「常用模型」建议（快速填充 model 输入）。
function suggestionsFor(
  catalog: CatalogEntry[],
  provider: string,
  kind: string,
): CatalogEntry[] {
  if (provider === COMPATIBLE_PROVIDER) return []
  return catalog.filter((e) => e.provider === provider && e.kind === kind)
}

// paramsText → params 对象。空 = undefined（不带）；非法 JSON / 非对象 → 抛出含文案的 Error。
export function parseParamsText(text: string): Record<string, unknown> | undefined {
  const trimmed = text.trim()
  if (trimmed === "") return undefined
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    throw new Error("参数不是合法 JSON")
  }
  if (typeof parsed !== "object" || parsed == null || Array.isArray(parsed)) {
    throw new Error("参数必须是 JSON 对象")
  }
  return parsed as Record<string, unknown>
}

export interface ModelConfigFieldsProps {
  catalog: CatalogEntry[]
  // 编辑模式：传入既有配置 → hasApiKey 提示「显示已存」/「留空保持不变」。
  initial?: ModelConfig
  // 拉取 provider 官方模型列表（可选；不传则不显示「拉取模型」按钮）。
  onListModels?: (input: ListModelsInput) => Promise<ListModelsResult>
  // 查看（解密回显）已存密钥（可选；仅编辑模式且配置含密钥时显示「显示已存」按钮）。
  onRevealKey?: (id: string) => Promise<string>
}

// 模型配置表单字段块（通过 useFormContext 读写）。
// 包含：provider/kind/model（含拉取模型）+ base_url + apiKey（RevealSecretInput）
// + enabled/isDefault + params（高级折叠）。
export function ModelConfigFields({
  catalog,
  initial,
  onListModels,
  onRevealKey,
}: ModelConfigFieldsProps) {
  const {
    register,
    setValue,
    getValues,
    control,
    watch,
    formState: { errors },
  } = useFormContext<FormValues>()

  // 拉取到的官方模型列表（含拉取时的 provider，用于切 provider 后判失效）。
  const [live, setLive] = useState<(ListModelsResult & { provider: string }) | null>(null)
  const [listing, setListing] = useState(false)

  const isEdit = initial != null
  const providers = providersFor(catalog)

  const provider = watch("provider")
  const kind = watch("kind")
  const enabled = watch("enabled")
  const isDefault = watch("isDefault")

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

  // 切 provider 后旧的拉取结果失效（按 render 派生，避免 effect 里 setState）。
  const activeLive = live && live.provider === provider ? live : null
  // 下拉/快捷填充的候选：只有 live 成功（source="live"）才用拉到的列表；
  // 失败/不支持时回退到按 kind 过滤过的静态建议，避免把 image/video 漏到 text 下拉。
  const isLive = activeLive?.source === "live"
  const liveModels = activeLive?.models ?? []
  const optionModels = isLive && liveModels.length > 0
    ? liveModels
    : suggestions.map((s) => s.model)

  const selectClass =
    "rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"
  // 分区小标题：靠分隔线+字距把表单切成「基础 / 凭证与端点 / 选项 / 高级」四段。
  const headingClass =
    "text-[11px] font-semibold tracking-[0.08em] text-text-3"

  return (
    <>
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
        <Controller
          name="apiKey"
          control={control}
          render={({ field }) => (
            <RevealSecretInput
              id="mc-apikey"
              value={field.value}
              onChange={field.onChange}
              placeholder="sk-..."
              onReveal={
                isEdit && initial?.hasApiKey && onRevealKey
                  ? () => onRevealKey(initial.id)
                  : undefined
              }
            />
          )}
        />
        <p className="text-[11.5px] text-text-3">
          {isEdit && initial?.hasApiKey
            ? "留空保持不变（已配置密钥）；填写则替换为新密钥；点「显示已存」可解密回显。"
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
    </>
  )
}
