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
import type { CreateProjectInput, Project, Style } from "@/lib/types"

// 内容类型 / 目标平台为前端枚举（后端只存字符串，无白名单约束）；风格取自 GET /api/prompt-styles 的 name。
const CONTENT_TYPES = ["短视频", "广告片", "动画", "宣传片"] as const
const TARGET_PLATFORMS = ["抖音", "视频号", "B 站", "小红书", "通用"] as const

// name 必填（后端缺则 400）；brief 必填（创意需求驱动 planner）；其余给默认值。
const formSchema = z.object({
  name: z.string().min(1, "请输入项目名称"),
  brief: z.string().min(1, "请输入创意需求"),
  contentType: z.string().min(1, "请选择内容类型"),
  targetPlatform: z.string().min(1, "请选择目标平台"),
  style: z.string().min(1, "请选择风格"),
})

type FormValues = z.infer<typeof formSchema>

export interface CreateProjectFormProps {
  styles: Style[]
  /** 提交表单——返回 Promise<Project>（成功）或 reject（失败）。 */
  onSubmit: (input: CreateProjectInput) => Promise<Project>
  /** 创建成功后回调（关闭 Dialog / 跳工作台）。 */
  onSuccess?: (project: Project) => void
}

// 无 Dialog 壳的纯表单，便于单测。提交调 onSubmit，成功调 onSuccess。
export function CreateProjectForm({
  styles,
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
    },
  })

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const project = await onSubmit(values)
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
