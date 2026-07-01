import { useState } from "react"
import { useForm, FormProvider, type Resolver } from "react-hook-form"
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
import { Button } from "@/components/studio/Button"
import type {
  ModelConfig,
  Project,
  StorageConfig,
  Style,
} from "@/lib/types"
import { ProjectFields } from "./ProjectFields"
import {
  projectFormSchema,
  defaultsFor,
  type ProjectFormValues,
} from "./ProjectFields.schema"

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
    kind: string
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
  // Edit 用 base schema（brief 放宽），避免 defaultsFor 产出 brief:"" 时 resolver 失败（Edit 变哑弹）。
  const resolver = zodResolver(projectFormSchema) as unknown as Resolver<ProjectFormValues>
  const form = useForm<ProjectFormValues>({
    resolver,
    defaultValues: defaultsFor(project),
  })

  const submit = form.handleSubmit(async (values) => {
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
        // 项目工作流化后类型固定：回传现有 kind（缺省 custom），编辑不改类型。
        kind: project.kind ?? "custom",
      })
      onSuccess?.(updated)
    } catch {
      setSubmitError("更新失败，请重试")
    }
  })

  return (
    <FormProvider {...form}>
      <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <ProjectFields
          styles={styles ?? []}
          fieldIdPrefix="edit"
          briefFieldName="description"
          briefRequired={false}
          alwaysShowPlanner
          textModels={textModels}
          imageModels={imageModels}
          storageConfigs={storageConfigs}
          project={project}
        />
        {submitError && (
          <p role="alert" className="text-[12px] text-danger">
            {submitError}
          </p>
        )}
        <DialogFooter className="mt-2">
          <Button type="submit" variant="amber" disabled={form.formState.isSubmitting}>
            {form.formState.isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            保存
          </Button>
        </DialogFooter>
      </form>
    </FormProvider>
  )
}

export interface EditProjectDialogProps extends EditProjectFormProps {
  trigger: React.ReactNode
}

// 项目详情页"编辑项目信息"入口——弹框。
// Dialog 壳保留自有 trigger+open（公共 API 不变），内部渲染 EditProjectForm——
// 不引入 FormDialog（避免双 Dialog；消费方依赖 trigger）。
export function EditProjectDialog({ trigger, onSuccess, ...formProps }: EditProjectDialogProps) {
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
