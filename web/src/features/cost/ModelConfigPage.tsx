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
import { Badge } from "@/components/studio/Badge"
import type {
  CatalogEntry,
  CreateModelConfigInput,
  ModelConfig,
} from "@/lib/types"

// 一期开放 kind：chat 当前后端 catalog 未含（仅 image/video/audio），故按 kind 分组展示已有配置；
// video/audio 标"二期"（生成走后端异步引擎，前端只配置/展示）。
const KIND_LABELS: Record<string, string> = {
  image: "图像",
  video: "视频",
  audio: "音频",
  chat: "对话",
}
const DEFERRED_KINDS = new Set(["video", "audio"])

export interface ModelConfigViewProps {
  configs: ModelConfig[] | undefined
  catalog: CatalogEntry[] | undefined
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  // 创建（提交 → Promise<ModelConfig>；密钥型 param → 400 由调用方 toast）。
  onCreate: (input: CreateModelConfigInput) => Promise<ModelConfig>
}

// 模型配置（admin-only）：按 kind 分组配置表 + 创建表单。表单绝不含 API key 字段。
export function ModelConfigView({
  configs,
  catalog,
  isLoading,
  isError,
  onRetry,
  onCreate,
}: ModelConfigViewProps) {
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
    <div className="flex flex-col gap-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="font-heading text-[22px] font-bold text-text-1">模型配置</h1>
        {!isLoading && (configs?.length ?? 0) > 0 && addButton}
      </header>

      <p className="text-[12px] text-text-3">
        模型密钥由服务端环境变量统一管理，配置中不包含也不下发任何 API key。
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
                  <span className="text-text-1">
                    {c.provider} · {c.model}
                  </span>
                  <span className="flex items-center gap-2">
                    {c.isDefault && <Badge variant="done">默认</Badge>}
                    <Badge variant={c.enabled ? "running" : "pending"}>
                      {c.enabled ? "已启用" : "已停用"}
                    </Badge>
                  </span>
                </div>
              ))}
            </div>
          </section>
        ))
      )}
    </div>
  )
}

// rhf+zod 创建表单（catalog 下拉选 provider·model·kind；enabled/isDefault 开关；params JSON）。
// 绝不含 API key 字段；含密钥型 param 由后端 400 拒绝（ErrSecretParam）。
const formSchema = z.object({
  // catalog 索引（选中条目带出 provider/model/kind）。
  catalogKey: z.string().min(1, "请选择模型"),
  enabled: z.boolean(),
  isDefault: z.boolean(),
  // 可选 params JSON 文本。空 = 不带 params；非法 JSON 校验报错。
  paramsText: z.string(),
})

type FormValues = z.infer<typeof formSchema>

function catalogKey(e: CatalogEntry): string {
  return `${e.provider}/${e.model}`
}

export interface CreateModelConfigFormProps {
  catalog: CatalogEntry[]
  onCreate: (input: CreateModelConfigInput) => Promise<ModelConfig>
  onSuccess?: (mc: ModelConfig) => void
}

export function CreateModelConfigForm({
  catalog,
  onCreate,
  onSuccess,
}: CreateModelConfigFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const {
    register,
    handleSubmit,
    setValue,
    control,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      catalogKey: catalog[0] ? catalogKey(catalog[0]) : "",
      enabled: true,
      isDefault: false,
      paramsText: "",
    },
  })

  const enabled = useWatch({ control, name: "enabled" })
  const isDefault = useWatch({ control, name: "isDefault" })

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)
    const entry = catalog.find((e) => catalogKey(e) === values.catalogKey)
    if (entry == null) {
      setSubmitError("请选择有效的模型")
      return
    }

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

    try {
      const mc = await onCreate({
        kind: entry.kind,
        provider: entry.provider,
        model: entry.model,
        enabled: values.enabled,
        isDefault: values.isDefault,
        params,
      })
      onSuccess?.(mc)
    } catch (err) {
      // 后端 400（含密钥型 param / 缺 provider·model）等 → 调用方 toast；此处兜底文案。
      setSubmitError(err instanceof Error ? "保存失败，请检查参数" : "保存失败")
    }
  })

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="mc-model">模型</Label>
        <select
          id="mc-model"
          aria-invalid={errors.catalogKey != null}
          {...register("catalogKey")}
          className="rounded-md border border-line bg-bg-base px-2.5 py-2 text-[13px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"
        >
          {catalog.map((e) => (
            <option key={catalogKey(e)} value={catalogKey(e)}>
              {e.label}（{e.provider} · {e.model}）
            </option>
          ))}
        </select>
        {errors.catalogKey && (
          <p className="text-[12px] text-danger">{errors.catalogKey.message}</p>
        )}
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
          参数中请勿包含 API key / secret 等密钥字段，密钥由服务端管理。
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
            从模型目录选择 provider/model；密钥由服务端管理，无需填写。
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
