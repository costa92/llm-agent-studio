import { useState } from "react"
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
import type {
  BasicPrompt,
  CreateWorkflowInput,
  Prompt,
  Workflow,
  WorkflowNode,
} from "@/lib/types"

// 新建/编辑工作流。name 文本框 + WorkflowNodesEditor。
// 校验：name 非空、≥1 节点、节点 id 非空且唯一（与原 EditProjectDialog 一致）。

// 返回第一处问题的中文描述,无问题返回 null。前端与后端 ValidateCustomGraph 同义,
// 让用户在保存前就看到「循环依赖」,而不是运行时才 400。
export function findGraphError(nodes: { id: string; dependsOn: string[] }[]): string | null {
  const ids = new Set(nodes.map((n) => n.id))
  for (const n of nodes) {
    for (const dep of n.dependsOn) {
      if (!ids.has(dep)) return `节点「${n.id}」依赖了不存在的节点「${dep}」`
    }
  }
  // DFS 三色环检测
  const deps = new Map(nodes.map((n) => [n.id, n.dependsOn]))
  const color = new Map<string, number>() // 0 white,1 gray,2 black
  let cycleMsg: string | null = null
  const visit = (id: string): boolean => {
    color.set(id, 1)
    for (const dep of deps.get(id) ?? []) {
      const c = color.get(dep) ?? 0
      if (c === 1) {
        cycleMsg = `工作流存在循环依赖:「${id}」→「${dep}」`
        return true
      }
      if (c === 0 && visit(dep)) return true
    }
    color.set(id, 2)
    return false
  }
  for (const n of nodes) {
    if ((color.get(n.id) ?? 0) === 0 && visit(n.id)) return cycleMsg
  }
  return null
}

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
  const [name, setName] = useState(initial?.name ?? "")
  const [nodes, setNodes] = useState<WorkflowNode[]>(
    () => initial?.nodes ?? DEFAULT_NODES,
  )
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setSubmitError(null)

    if (!name.trim()) {
      setSubmitError("请输入工作流名称")
      return
    }
    if (nodes.length === 0) {
      setSubmitError("工作流必须包含至少一个节点")
      return
    }
    const ids = new Set<string>()
    for (const n of nodes) {
      if (!n.id) {
        setSubmitError("所有节点 ID 不能为空")
        return
      }
      if (ids.has(n.id)) {
        setSubmitError(`存在重复的节点 ID: ${n.id}`)
        return
      }
      ids.add(n.id)
    }

    const graphErr = findGraphError(nodes)
    if (graphErr) {
      setSubmitError(graphErr)
      return
    }

    setIsSubmitting(true)
    try {
      const saved = await onSubmit({ name: name.trim(), nodes })
      onSuccess?.(saved)
    } catch {
      setSubmitError("保存失败，请重试")
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
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
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. 默认管线"
        />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto pr-1">
        <WorkflowNodesEditor
          nodes={nodes}
          onChange={setNodes}
          prompts={prompts}
          basics={basics}
          org={org}
        />
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
