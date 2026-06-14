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
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { Button } from "@/components/studio/Button"
import type { CreateProjectInput, ModelConfig, Project, Style } from "@/lib/types"

// 内容类型 / 目标平台为前端枚举（后端只存字符串，无白名单约束）；风格取自 GET /api/prompt-styles 的 name。
const CONTENT_TYPES = ["短视频", "广告片", "动画", "宣传片"] as const
const TARGET_PLATFORMS = ["抖音", "视频号", "B 站", "小红书", "通用"] as const

// name 必填（后端缺则 400）；brief 必填（创意需求驱动 planner）；其余给默认值。
// M5.1/M9: plannerProvider / plannerModel, imageProvider / imageModel 可选（空 = 走 org 默认）。
const formSchema = z.object({
  name: z.string().min(1, "请输入项目名称"),
  brief: z.string().min(1, "请输入创意需求"),
  contentType: z.string().min(1, "请选择内容类型"),
  targetPlatform: z.string().min(1, "请选择目标平台"),
  style: z.string().min(1, "请选择风格"),
  plannerProvider: z.string(),
  plannerModel: z.string(),
  imageProvider: z.string(),
  imageModel: z.string(),
})

type FormValues = z.infer<typeof formSchema>

export interface CreateProjectFormProps {
  styles: Style[]
  /** M5.1: org 下 kind=text 的启用模型列表（供规划模型下拉）。空 = 不显示该下拉。 */
  textModels?: ModelConfig[]
  /** M9: org 下 kind=image 的启用模型列表（供图片模型下拉）。空 = 不显示该下拉。 */
  imageModels?: ModelConfig[]
  /** 提交表单——返回 Promise<Project>（成功）或 reject（失败）。 */
  onSubmit: (input: CreateProjectInput) => Promise<Project>
  /** 创建成功后回调（关闭 Dialog / 跳工作台）。 */
  onSuccess?: (project: Project) => void
}

// 无 Dialog 壳的纯表单，便于单测。提交调 onSubmit，成功调 onSuccess。
export function CreateProjectForm({
  styles,
  textModels,
  imageModels,
  onSubmit,
  onSuccess,
}: CreateProjectFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const {
    register,
    handleSubmit,
    control,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      name: "",
      brief: "",
      contentType: CONTENT_TYPES[0],
      targetPlatform: TARGET_PLATFORMS[0],
      // 默认选中首个风格（若已加载）——免去必填空态，且为合理 UX。
      style: styles[0]?.name ?? "",
      // M5.1: 规划模型默认空 = 走 org 默认。下拉只显示 org 启用的 text 模型。
      plannerProvider: "",
      plannerModel: "",
      // M9: 图片模型默认空 = 走 org 默认。下拉只显示 org 启用的 image 模型。
      imageProvider: "",
      imageModel: "",
    },
  })

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      // 空 = 走 org 默认 → 不传给后端（让后端语义上 = "无 override"）。
      const input: CreateProjectInput = {
        name: values.name,
        brief: values.brief,
        contentType: values.contentType,
        targetPlatform: values.targetPlatform,
        style: values.style,
        ...(values.plannerProvider && values.plannerModel
          ? { plannerProvider: values.plannerProvider, plannerModel: values.plannerModel }
          : {}),
        ...(values.imageProvider && values.imageModel
          ? { imageProvider: values.imageProvider, imageModel: values.imageModel }
          : {}),
      }
      const project = await onSubmit(input)
      onSuccess?.(project)
    } catch {
      setSubmitError("创建失败，请重试")
    }
  })

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="name">项目名称</Label>
        <Input id="name" aria-invalid={errors.name != null} {...register("name")} />
        {errors.name && (
          <p className="text-[12px] text-danger">{errors.name.message}</p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="brief">创意需求</Label>
        <Textarea
          id="brief"
          rows={3}
          placeholder="用一句话描述你想要的作品"
          aria-invalid={errors.brief != null}
          {...register("brief")}
        />
        {errors.brief && (
          <p className="text-[12px] text-danger">{errors.brief.message}</p>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="contentType">内容类型</Label>
        <Controller
          control={control}
          name="contentType"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id="contentType">
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
        <Label htmlFor="targetPlatform">目标平台</Label>
        <Controller
          control={control}
          name="targetPlatform"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id="targetPlatform">
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
        <Label htmlFor="style">风格</Label>
        <Controller
          control={control}
          name="style"
          render={({ field }) => (
            <Select value={field.value} onValueChange={field.onChange}>
              <SelectTrigger id="style" aria-invalid={errors.style != null}>
                <SelectValue placeholder="选择风格" />
              </SelectTrigger>
              <SelectContent>
                {styles.map((s) => (
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

      {/* M5.1: per-project 规划模型。空 = org 默认；下拉只显示 org 真启用的
          text 模型。org 还没配 text 模型时整个区段隐藏（避免无意义空下拉）。
          Radix Select.Item 不允许空字符串 value，所以用 "__default__" 哨兵。 */}
      {textModels && textModels.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="plannerModel">规划用模型（可选）</Label>
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
                    <SelectTrigger id="plannerModel" aria-invalid={false}>
                      <SelectValue placeholder="使用组织默认" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="__default__">使用组织默认</SelectItem>
                      {textModels.map((m) => {
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
            留空 = 走组织默认；选某个模型则本次及后续 run 都用该模型。
          </p>
        </div>
      )}

      {imageModels && imageModels.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="imageModel">图片生成模型（可选）</Label>
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
                    <SelectTrigger id="imageModel" aria-invalid={false}>
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
            留空 = 走组织默认；选某个模型则本次及后续 run 都用该模型生成图片。
          </p>
        </div>
      )}

      {submitError && (
        <p role="alert" className="text-[12px] text-danger">
          {submitError}
        </p>
      )}

      <DialogFooter>
        <Button type="submit" variant="amber" disabled={isSubmitting}>
          {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
          创建
        </Button>
      </DialogFooter>
    </form>
  )
}

export interface CreateProjectDialogProps extends CreateProjectFormProps {
  trigger: React.ReactNode
}

// Dialog 壳：trigger 打开，创建成功后自动关闭并透传 onSuccess。
export function CreateProjectDialog({
  trigger,
  onSuccess,
  ...formProps
}: CreateProjectDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>新建项目</DialogTitle>
          <DialogDescription>用一句创意需求开始你的第一支作品。</DialogDescription>
        </DialogHeader>
        <CreateProjectForm
          {...formProps}
          onSuccess={(project) => {
            setOpen(false)
            onSuccess?.(project)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
