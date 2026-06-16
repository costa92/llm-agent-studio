import { useState } from "react"
import { useForm, Controller } from "react-hook-form"
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/studio/Button"
import type { ModelConfig, Project, StorageConfig, Style } from "@/lib/types"
import { MODE_LABELS } from "@/features/storage/StorageConfigPage"

// 项目详情页"编辑项目信息"入口——弹框。
// 允许修改基本信息（名称/创意需求/内容类型/目标平台/风格）以及
// plannerProvider / plannerModel / imageProvider / imageModel / storageConfigId。
// 模型/存储留空 = 走 org 默认。

// 内容类型 / 目标平台与 CreateProjectDialog 保持一致（后端只存字符串，无白名单）。
const CONTENT_TYPES = ["短视频", "广告片", "动画", "宣传片"] as const
const TARGET_PLATFORMS = ["抖音", "视频号", "B 站", "小红书", "通用"] as const

const formSchema = z.object({
  name: z.string().min(1, "请输入项目名称"),
  description: z.string(),
  contentType: z.string().min(1, "请选择内容类型"),
  targetPlatform: z.string().min(1, "请选择目标平台"),
  style: z.string().min(1, "请选择风格"),
  plannerProvider: z.string(),
  plannerModel: z.string(),
  imageProvider: z.string(),
  imageModel: z.string(),
  storageConfigId: z.string(),
})

type FormValues = z.infer<typeof formSchema>

export interface EditProjectFormProps {
  project: Project
  textModels?: ModelConfig[]
  imageModels?: ModelConfig[]
  /** GET /api/prompt-styles 的风格列表，供风格下拉。 */
  styles?: Style[]
  /** org 下的存储配置列表，供存储配置下拉（继承默认 or 指定配置）。 */
  storageConfigs?: StorageConfig[]
  onSubmit: (input: {
    name: string
    description: string
    contentType: string
    targetPlatform: string
    style: string
    plannerProvider: string
    plannerModel: string
    imageProvider: string
    imageModel: string
    storageConfigId: string
  }) => Promise<Project>
  onSuccess?: (project: Project) => void
}

export function EditProjectForm({
  project,
  textModels,
  imageModels,
  styles,
  storageConfigs,
  onSubmit,
  onSuccess,
}: EditProjectFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)

  const {
    register,
    handleSubmit,
    control,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      name: project.name ?? "",
      description: project.description ?? "",
      contentType: project.contentType ?? CONTENT_TYPES[0],
      targetPlatform: project.targetPlatform ?? TARGET_PLATFORMS[0],
      style: project.style ?? "",
      plannerProvider: project.plannerProvider ?? "",
      plannerModel: project.plannerModel ?? "",
      imageProvider: project.imageProvider ?? "",
      imageModel: project.imageModel ?? "",
      storageConfigId: project.storageConfigId ?? "",
    },
  })

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)

    try {
      const updated = await onSubmit({
        name: values.name,
        description: values.description,
        contentType: values.contentType,
        targetPlatform: values.targetPlatform,
        style: values.style,
        plannerProvider: values.plannerProvider,
        plannerModel: values.plannerModel,
        imageProvider: values.imageProvider,
        imageModel: values.imageModel,
        storageConfigId: values.storageConfigId,
      })
      onSuccess?.(updated)
    } catch {
      setSubmitError("更新失败，请重试")
    }
  })

  // 风格下拉：项目当前风格若不在 styles 列表里，补一个选项避免回显丢失。
  const styleOptions = styles ?? []
  const hasCurrentStyle =
    !project.style || styleOptions.some((s) => s.name === project.style)

  return (
    <form
      onSubmit={submit}
      className="grid grid-cols-1 gap-4 sm:grid-cols-2"
      noValidate
    >
      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label htmlFor="edit-name">项目名称</Label>
        <Input
          id="edit-name"
          aria-invalid={errors.name != null}
          {...register("name")}
        />
        {errors.name && (
          <p className="text-[12px] text-danger">{errors.name.message}</p>
        )}
      </div>

      <div className="flex flex-col gap-1.5 sm:col-span-2">
        <Label htmlFor="edit-description">创意需求</Label>
        <Textarea
          id="edit-description"
          rows={2}
          placeholder="用一句话描述你想要的作品"
          {...register("description")}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="edit-contentType">内容类型</Label>
        <Controller
          control={control}
          name="contentType"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id="edit-contentType">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CONTENT_TYPES.map((ct) => (
                  <SelectItem key={ct} value={ct}>
                    {ct}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="edit-targetPlatform">目标平台</Label>
        <Controller
          control={control}
          name="targetPlatform"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id="edit-targetPlatform">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {TARGET_PLATFORMS.map((tp) => (
                  <SelectItem key={tp} value={tp}>
                    {tp}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="edit-style">风格</Label>
        <Controller
          control={control}
          name="style"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id="edit-style" aria-invalid={errors.style != null}>
                <SelectValue placeholder="选择风格" />
              </SelectTrigger>
              <SelectContent>
                {!hasCurrentStyle && (
                  <SelectItem value={project.style}>{project.style}</SelectItem>
                )}
                {styleOptions.map((s) => (
                  <SelectItem key={s.name} value={s.name}>
                    {s.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
        {errors.style && (
          <p className="text-[12px] text-danger">{errors.style.message}</p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="edit-plannerModel">规划用模型</Label>
        <Controller
          control={control}
          name="plannerProvider"
          render={({ field: provField }) => (
            <Controller
              control={control}
              name="plannerModel"
              render={({ field: modField }) => (
                <Select
                  value={
                    provField.value && modField.value
                      ? `${provField.value}::${modField.value}`
                      : "__default__"
                  }
                  onValueChange={(v) => {
                    if (v === "__default__") {
                      provField.onChange("")
                      modField.onChange("")
                      return
                    }
                    const sep = v.indexOf("::")
                    if (sep < 0) return
                    provField.onChange(v.slice(0, sep))
                    modField.onChange(v.slice(sep + 2))
                  }}
                >
                  <SelectTrigger id="edit-plannerModel" aria-invalid={false}>
                    <SelectValue placeholder="使用组织默认" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__default__">使用组织默认</SelectItem>
                    {textModels?.map((m) => {
                      const key = `${m.provider}::${m.model}`
                      return (
                        <SelectItem key={key} value={key}>
                          {m.provider} · {m.model}
                          {m.isDefault ? "（默认）" : ""}
                        </SelectItem>
                      )
                    })}
                  </SelectContent>
                </Select>
              )}
            />
          )}
        />
        <p className="text-[11.5px] text-text-3">
          当前：{project.plannerProvider && project.plannerModel
            ? `${project.plannerProvider} · ${project.plannerModel}`
            : "组织默认"}。保存后下次 run 起生效。
        </p>
      </div>

      {imageModels && imageModels.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="edit-imageModel">图片生成模型</Label>
          <Controller
            control={control}
            name="imageProvider"
            render={({ field: provField }) => (
              <Controller
                control={control}
                name="imageModel"
                render={({ field: modField }) => (
                  <Select
                    value={
                      provField.value && modField.value
                        ? `${provField.value}::${modField.value}`
                        : "__default__"
                    }
                    onValueChange={(v) => {
                      if (v === "__default__") {
                        provField.onChange("")
                        modField.onChange("")
                        return
                      }
                      const sep = v.indexOf("::")
                      if (sep < 0) return
                      provField.onChange(v.slice(0, sep))
                      modField.onChange(v.slice(sep + 2))
                    }}
                  >
                    <SelectTrigger id="edit-imageModel" aria-invalid={false}>
                      <SelectValue placeholder="使用组织默认" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__default__">使用组织默认</SelectItem>
                      {imageModels.map((m) => {
                        const key = `${m.provider}::${m.model}`
                        return (
                          <SelectItem key={key} value={key}>
                            {m.provider} · {m.model}
                            {m.isDefault ? "（默认）" : ""}
                          </SelectItem>
                        )
                      })}
                    </SelectContent>
                  </Select>
                )}
              />
            )}
          />
          <p className="text-[11.5px] text-text-3">
            当前：{project.imageProvider && project.imageModel
              ? `${project.imageProvider} · ${project.imageModel}`
              : "组织默认"}。保存后下次 run 起生效。
          </p>
        </div>
      )}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="edit-storageConfigId">存储配置</Label>
        <Controller
          control={control}
          name="storageConfigId"
          render={({ field }) => (
            <Select
              value={field.value || "__default__"}
              onValueChange={(v) => {
                field.onChange(v === "__default__" ? "" : v)
              }}
            >
              <SelectTrigger id="edit-storageConfigId" aria-invalid={false}>
                <SelectValue placeholder="继承组织默认" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">继承组织默认</SelectItem>
                {storageConfigs?.filter((c) => c.enabled).map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}（{MODE_LABELS[c.mode]}）
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        />
        <p className="text-[11.5px] text-text-3">
          当前：{(() => {
            if (!project.storageConfigId) return "继承组织默认"
            const c = storageConfigs?.find((c) => c.id === project.storageConfigId)
            return c ? `${c.name}（${MODE_LABELS[c.mode]}）` : project.storageConfigId
          })()}。保存后下一次资源生成或加载起生效。
        </p>
      </div>

      {submitError && (
        <p role="alert" className="text-[12px] text-danger sm:col-span-2">
          {submitError}
        </p>
      )}

      <DialogFooter className="mt-2 sm:col-span-2">
        <Button type="submit" variant="amber" disabled={isSubmitting}>
          {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
          保存
        </Button>
      </DialogFooter>
    </form>
  )
}

export interface EditProjectDialogProps extends EditProjectFormProps {
  trigger: React.ReactNode
}

export function EditProjectDialog({
  trigger,
  onSuccess,
  ...formProps
}: EditProjectDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[90vh] w-[95vw] flex-col overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑项目信息</DialogTitle>
          <DialogDescription>
            基本信息即时生效；模型/存储改动影响后续所有 run，当前正在跑的 run 不受影响。
          </DialogDescription>
        </DialogHeader>
        <EditProjectForm
          {...formProps}
          onSuccess={(p) => {
            setOpen(false)
            onSuccess?.(p)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
