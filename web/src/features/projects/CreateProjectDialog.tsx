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
  CreateProjectInput,
  ModelConfig,
  Project,
  StorageConfig,
  Style,
} from "@/lib/types"
import { ProjectFields } from "./ProjectFields"
import {
  createProjectFormSchema,
  defaultsFor,
  type ProjectFormValues,
} from "./ProjectFields.schema"

// 表单值 → CreateProjectInput。空模型不带（= 后端无 override）。项目工作流化后
// 不再区分类型：创建即工作流项目，不带 kind（后端默认 custom）。
function toCreateInput(values: ProjectFormValues): CreateProjectInput {
  return {
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
    ...(values.storageConfigId
      ? { storageConfigId: values.storageConfigId }
      : {}),
  }
}

export interface CreateProjectFormProps {
  styles: Style[]
  /** M5.1: org 下 kind=text 的启用模型列表（供规划模型下拉）。空 = 不显示该下拉。 */
  textModels?: ModelConfig[]
  /** M9: org 下 kind=image 的启用模型列表（供图片模型下拉）。空 = 不显示该下拉。
   *  注：当前「新建项目」入口刻意不传 imageModels（图片模型选择仅编辑时暴露，M9 范围）；
   *  prop 仍透传给 ProjectFields 以备将来。 */
  imageModels?: ModelConfig[]
  /** M10: org 存储配置列表（供存储下拉）。空 = 不显示存储下拉（= 用组织默认）。 */
  storageConfigs?: StorageConfig[]
  /** 提交表单——返回 Promise<Project>（成功）或 reject（失败）。 */
  onSubmit: (input: CreateProjectInput) => Promise<Project>
  /** 创建成功后回调（关闭 Dialog / 跳工作台）。 */
  onSuccess?: (project: Project) => void
}

// 无 Dialog 壳的纯表单（测试直接渲染）。提交调 onSubmit，成功调 onSuccess。
export function CreateProjectForm({
  styles,
  textModels,
  imageModels,
  storageConfigs,
  onSubmit,
  onSuccess,
}: CreateProjectFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  // Create 专用 schema（brief 必填）；强转避免 zod infer 类型宽窄不对齐。
  const resolver = zodResolver(createProjectFormSchema) as unknown as Resolver<ProjectFormValues>
  const form = useForm<ProjectFormValues>({
    resolver,
    // 不再预填风格/内容类型/平台——留空 = 不指定，由工作流决定（内容类型/风格解耦）。
    defaultValues: defaultsFor(),
  })

  const submit = form.handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const project = await onSubmit(toCreateInput(values))
      onSuccess?.(project)
    } catch {
      setSubmitError("创建失败，请重试")
    }
  })

  return (
    <FormProvider {...form}>
      <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <ProjectFields
          styles={styles}
          fieldIdPrefix="create"
          briefFieldName="brief"
          briefRequired
          textModels={textModels}
          imageModels={imageModels}
          storageConfigs={storageConfigs}
        />
        {submitError && (
          <p role="alert" className="text-[12px] text-danger">
            {submitError}
          </p>
        )}
        <DialogFooter>
          <Button type="submit" variant="amber" disabled={form.formState.isSubmitting}>
            {form.formState.isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            创建
          </Button>
        </DialogFooter>
      </form>
    </FormProvider>
  )
}

export interface CreateProjectDialogProps extends CreateProjectFormProps {
  trigger: React.ReactNode
}

// Dialog 壳：trigger 打开，创建成功后自动关闭并透传 onSuccess。
// 保留自有 <Dialog open trigger>（公共 API 不变），内部渲染 CreateProjectForm——不引入 FormDialog（避免双 Dialog）。
export function CreateProjectDialog({
  trigger,
  styles,
  textModels,
  imageModels,
  storageConfigs,
  onSubmit,
  onSuccess,
}: CreateProjectDialogProps) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[90vh] w-[95vw] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>新建项目</DialogTitle>
          <DialogDescription>用一句创意需求开始你的第一支作品。</DialogDescription>
        </DialogHeader>
        <CreateProjectForm
          styles={styles}
          textModels={textModels}
          imageModels={imageModels}
          storageConfigs={storageConfigs}
          onSubmit={onSubmit}
          onSuccess={(project) => {
            setOpen(false)
            onSuccess?.(project)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
