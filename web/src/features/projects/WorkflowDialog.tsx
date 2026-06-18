import { useState } from "react"
import { useForm, FormProvider, Controller, type Resolver } from "react-hook-form"
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/studio/Button"
import { useBasicPrompts, usePrompts } from "@/features/prompt/api"
import { WorkflowNodesEditor } from "./WorkflowNodesEditor"
import { useCreateWorkflow, useUpdateWorkflow } from "./workflowApi"
import { workflowFormSchema, type WorkflowFormValues } from "./WorkflowDialog.schema"
import type {
  BasicPrompt,
  CreateWorkflowInput,
  Prompt,
  Workflow,
  WorkflowNode,
} from "@/lib/types"

// 新建/编辑工作流。name 文本框 + WorkflowNodesEditor。
// 校验：name 非空、≥1 节点、节点 id 非空且唯一（与原 EditProjectDialog 一致）。

const DEFAULT_NODES: WorkflowNode[] = [
  { id: "script-1", type: "script", promptId: "", dependsOn: [] },
  { id: "storyboard-1", type: "storyboard", promptId: "", dependsOn: ["script-1"] },
]

export interface WorkflowFormProps {
  initial?: Workflow
  prompts?: Prompt[]
  basics?: BasicPrompt[]
  org?: string
  onSubmit: (input: CreateWorkflowInput) => Promise<Workflow>
  onSuccess?: (workflow: Workflow) => void
}

// 无 Dialog 壳的纯表单，便于单测。
export function WorkflowForm({
  initial,
  prompts,
  basics,
  org,
  onSubmit,
  onSuccess,
}: WorkflowFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  // superRefine 让 schema 的输入/输出类型收窄，zodResolver 推出的 Resolver 泛型与
  // useForm 期望的 Resolver<WorkflowFormValues> 不严格匹配，需显式 cast。
  const resolver = zodResolver(
    workflowFormSchema,
  ) as unknown as Resolver<WorkflowFormValues>
  const form = useForm<WorkflowFormValues>({
    resolver,
    defaultValues: {
      name: initial?.name ?? "",
      nodes: initial?.nodes ?? DEFAULT_NODES,
    },
  })
  const {
    register,
    control,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = form

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)
    try {
      const saved = await onSubmit({ name: values.name.trim(), nodes: values.nodes })
      onSuccess?.(saved)
    } catch {
      setSubmitError("保存失败，请重试")
    }
  })

  // superRefine 首个问题即停 → name 与 nodes 至多一条错误。单条 alert 显示首个文案
  //（保留现状「一个 role=alert 显示首个错误」呈现与测试断言）。
  const validationError = errors.name?.message ?? errors.nodes?.message ?? null
  const shownError = validationError ?? submitError

  return (
    <FormProvider {...form}>
      <form
        onSubmit={submit}
        className="flex min-h-0 flex-1 flex-col gap-4"
        noValidate
      >
        {/* 名称固定在顶部；节点编辑区随对话框高度自适应滚动；底部按钮固定。 */}
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="workflow-name">工作流名称</Label>
          <Input
            id="workflow-name"
            placeholder="e.g. 默认管线"
            {...register("name")}
          />
        </div>

        <div className="min-h-0 flex-1 overflow-y-auto pr-1">
          <Controller
            control={control}
            name="nodes"
            render={({ field }) => (
              <WorkflowNodesEditor
                nodes={field.value}
                onChange={field.onChange}
                prompts={prompts}
                basics={basics}
                org={org}
              />
            )}
          />
        </div>

        {shownError && (
          <p role="alert" className="text-[12px] text-danger">
            {shownError}
          </p>
        )}

        <DialogFooter>
          <Button type="submit" variant="amber" disabled={isSubmitting}>
            {isSubmitting && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            保存
          </Button>
        </DialogFooter>
      </form>
    </FormProvider>
  )
}

export interface WorkflowDialogProps {
  projectId: string
  orgId: string
  initial?: Workflow
  trigger: React.ReactNode
  onSuccess?: (workflow: Workflow) => void
}

// Dialog 壳：trigger 打开，保存成功后自动关闭并透传 onSuccess。
// 自身接线 prompts 与 create/update mutation。
export function WorkflowDialog({
  projectId,
  orgId,
  initial,
  trigger,
  onSuccess,
}: WorkflowDialogProps) {
  const [open, setOpen] = useState(false)
  const { data: prompts } = usePrompts(orgId)
  const { data: basics } = useBasicPrompts()
  const createWorkflow = useCreateWorkflow(projectId)
  const updateWorkflow = useUpdateWorkflow(projectId)

  const handleSubmit = (input: CreateWorkflowInput) =>
    initial
      ? updateWorkflow.mutateAsync({ wfId: initial.id, input })
      : createWorkflow.mutateAsync(input)

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[90vh] w-[95vw] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{initial ? "编辑工作流" : "新建工作流"}</DialogTitle>
          <DialogDescription>
            按手动配置的 DAG 节点和依赖关系执行；运行后产物可在运行详情查看。
          </DialogDescription>
        </DialogHeader>
        {/* 每次开关重建表单（key），避免 initial 切换后残留旧 state。 */}
        <WorkflowForm
          key={open ? `${initial?.id ?? "new"}-open` : "closed"}
          initial={initial}
          prompts={prompts}
          basics={basics}
          org={orgId}
          onSubmit={handleSubmit}
          onSuccess={(wf) => {
            setOpen(false)
            onSuccess?.(wf)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
