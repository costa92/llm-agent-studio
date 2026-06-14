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
import { Label } from "@/components/ui/label"
import { Button } from "@/components/studio/Button"
import type { ModelConfig, Project } from "@/lib/types"

// M5.1: 项目详情页"编辑规划模型"入口——弹框。当前只允许改 planner 字段
// (想改 brief / style 删了重建)。空 = 走 org 默认；非空 = 经 modelrouter
// 查 org 的 (provider, model) 拿 key 给 planner。

const formSchema = z.object({
  plannerProvider: z.string(),
  plannerModel: z.string(),
})

type FormValues = z.infer<typeof formSchema>

export interface EditProjectFormProps {
  project: Project
  textModels?: ModelConfig[]
  onSubmit: (input: { plannerProvider: string; plannerModel: string }) => Promise<Project>
  onSuccess?: (project: Project) => void
}

export function EditProjectForm({
  project,
  textModels,
  onSubmit,
  onSuccess,
}: EditProjectFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  const initialKey =
    project.plannerProvider && project.plannerModel
      ? `${project.plannerProvider}::${project.plannerModel}`
      : ""
  const {
    handleSubmit,
    control,
    formState: { isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      plannerProvider: project.plannerProvider ?? "",
      plannerModel: project.plannerModel ?? "",
    },
  })

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const updated = await onSubmit({
        plannerProvider: values.plannerProvider,
        plannerModel: values.plannerModel,
      })
      onSuccess?.(updated)
    } catch {
      setSubmitError("更新失败，请重试")
    }
  })

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
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
                  value={initialKey || "__default__"}
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
      <DialogContent>
        <DialogHeader>
          <DialogTitle>编辑规划模型</DialogTitle>
          <DialogDescription>
            改后影响后续所有 run；当前正在跑的 run 不受影响。
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
