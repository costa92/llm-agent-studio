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
import { usePrompts } from "@/features/prompt/api"

// M5.1/M9: 项目详情页"编辑模型配置"入口——弹框。
// 允许修改 plannerProvider / plannerModel 以及 imageProvider / imageModel 字段。
// 空 = 走 org 默认。

const formSchema = z.object({
  plannerProvider: z.string(),
  plannerModel: z.string(),
  imageProvider: z.string(),
  imageModel: z.string(),
  storageMode: z.string(),
  customWorkflowEnabled: z.boolean(),
})

type FormValues = z.infer<typeof formSchema>

export interface EditProjectFormProps {
  project: Project
  textModels?: ModelConfig[]
  imageModels?: ModelConfig[]
  onSubmit: (input: {
    plannerProvider: string
    plannerModel: string
    imageProvider: string
    imageModel: string
    storageMode: string
    customWorkflowEnabled: boolean
    workflowNodes: string
  }) => Promise<Project>
  onSuccess?: (project: Project) => void
}

interface WorkflowNode {
  id: string
  type: string
  promptId: string
  dependsOn: string[]
}

export function EditProjectForm({
  project,
  textModels,
  imageModels,
  onSubmit,
  onSuccess,
}: EditProjectFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null)
  
  const { data: prompts } = usePrompts(project.orgId)

  const [nodes, setNodes] = useState<WorkflowNode[]>(() => {
    if (project.workflowNodes) {
      try {
        const parsed = typeof project.workflowNodes === 'string'
          ? JSON.parse(project.workflowNodes)
          : (project.workflowNodes as any);
        if (Array.isArray(parsed)) return parsed;
      } catch (e) {
        console.error(e)
      }
    }
    return []
  })

  const {
    handleSubmit,
    control,
    watch,
    formState: { isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      plannerProvider: project.plannerProvider ?? "",
      plannerModel: project.plannerModel ?? "",
      imageProvider: project.imageProvider ?? "",
      imageModel: project.imageModel ?? "",
      storageMode: project.storageMode ?? "",
      customWorkflowEnabled: project.customWorkflowEnabled ?? false,
    },
  })

  const customWorkflowEnabled = watch("customWorkflowEnabled")

  const submit = handleSubmit(async (values) => {
    setSubmitError(null)

    if (values.customWorkflowEnabled) {
      if (nodes.length === 0) {
        setSubmitError("自定义工作流必须包含至少一个节点")
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
    }

    try {
      const updated = await onSubmit({
        plannerProvider: values.plannerProvider,
        plannerModel: values.plannerModel,
        imageProvider: values.imageProvider,
        imageModel: values.imageModel,
        storageMode: values.storageMode,
        customWorkflowEnabled: values.customWorkflowEnabled,
        workflowNodes: values.customWorkflowEnabled ? JSON.stringify(nodes) : "",
      })
      onSuccess?.(updated)
    } catch {
      setSubmitError("更新失败，请重试")
    }
  })

  return (
    <form onSubmit={submit} className="flex flex-col gap-4 max-h-[75vh] overflow-y-auto pr-2" noValidate>
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
        <Label htmlFor="edit-storageMode">存储方式</Label>
        <Controller
          control={control}
          name="storageMode"
          render={({ field }) => (
            <Select
              value={field.value || "__default__"}
              onValueChange={(v) => {
                field.onChange(v === "__default__" ? "" : v)
              }}
            >
              <SelectTrigger id="edit-storageMode" aria-invalid={false}>
                <SelectValue placeholder="使用组织默认" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="__default__">使用组织默认</SelectItem>
                <SelectItem value="localfs">本地磁盘 (localfs)</SelectItem>
                <SelectItem value="s3">Amazon S3 / S3 兼容 (s3)</SelectItem>
                <SelectItem value="oss">阿里云 OSS (oss)</SelectItem>
                <SelectItem value="cos">腾讯云 COS (cos)</SelectItem>
                <SelectItem value="github">GitHub 仓库 (github)</SelectItem>
              </SelectContent>
            </Select>
          )}
        />
        <p className="text-[11.5px] text-text-3">
          当前：{project.storageMode ? project.storageMode : "组织默认"}。保存后下一次资源生成或加载起生效。
        </p>
      </div>

      <div className="flex items-center justify-between border-t border-border pt-4 mt-2">
        <div className="flex flex-col gap-0.5 max-w-[80%]">
          <Label htmlFor="edit-customWorkflow" className="font-semibold text-text-1">开启自定义工作流</Label>
          <span className="text-[11px] text-text-3">启用后将绕过 LLM 规划器，按手动配置的 DAG 节点和依赖关系执行。</span>
        </div>
        <Controller
          control={control}
          name="customWorkflowEnabled"
          render={({ field }) => (
            <input
              id="edit-customWorkflow"
              type="checkbox"
              className="w-4 h-4 text-primary bg-input border-input rounded focus:ring-ring cursor-pointer"
              checked={field.value}
              onChange={(e) => {
                const checked = e.target.checked
                field.onChange(checked)
                if (checked && nodes.length === 0) {
                  setNodes([
                    { id: "script-1", type: "script", promptId: "", dependsOn: [] },
                    { id: "storyboard-1", type: "storyboard", promptId: "", dependsOn: ["script-1"] },
                  ])
                }
              }}
            />
          )}
        />
      </div>

      {customWorkflowEnabled && (
        <div className="flex flex-col gap-3 border border-border rounded-lg p-3 bg-muted/20">
          <div className="flex items-center justify-between border-b border-border pb-2">
            <h4 className="text-[13px] font-semibold text-text-1">工作流节点配置</h4>
            <button
              type="button"
              className="text-[12px] text-amber hover:text-amber/80 font-medium border border-amber/30 hover:border-amber px-2.5 py-1 rounded transition-colors cursor-pointer"
              onClick={() => {
                const newId = `node-${nodes.length + 1}`
                setNodes([...nodes, { id: newId, type: "script", promptId: "", dependsOn: [] }])
              }}
            >
              + 添加节点
            </button>
          </div>

          {nodes.length === 0 ? (
            <p className="text-[11.5px] text-text-3 text-center py-2">暂无步骤，点击上方“添加节点”开始配置</p>
          ) : (
            <div className="flex flex-col gap-4 max-h-[300px] overflow-y-auto pr-1">
              {nodes.map((node, index) => (
                <div key={index} className="flex flex-col gap-2.5 p-3 border border-border rounded bg-card/40 relative">
                  <button
                    type="button"
                    className="absolute top-2 right-2 text-text-3 hover:text-danger text-[11px] cursor-pointer"
                    onClick={() => {
                      const updated = nodes.filter((_, i) => i !== index)
                      const cleaned = updated.map(n => ({
                        ...n,
                        dependsOn: n.dependsOn.filter(d => d !== node.id)
                      }))
                      setNodes(cleaned)
                    }}
                  >
                    删除
                  </button>

                  <div className="grid grid-cols-2 gap-2 mt-1">
                    <div className="flex flex-col gap-1">
                      <Label className="text-[11px] text-text-2">节点 ID (英文标识)</Label>
                      <input
                        type="text"
                        className="h-8 rounded border border-input px-2 text-[12px] bg-background text-text-1 focus:ring-1 focus:ring-ring"
                        value={node.id}
                        onChange={(e) => {
                          const oldId = node.id
                          const newId = e.target.value.trim()
                          const updated = [...nodes]
                          updated[index] = { ...node, id: newId }
                          const renamed = updated.map(n => ({
                            ...n,
                            dependsOn: n.dependsOn.map(d => d === oldId ? newId : d)
                          }))
                          setNodes(renamed)
                        }}
                        placeholder="e.g. script-1"
                      />
                    </div>

                    <div className="flex flex-col gap-1">
                      <Label className="text-[11px] text-text-2">任务类型</Label>
                      <Select
                        value={node.type}
                        onValueChange={(val) => {
                          const updated = [...nodes]
                          updated[index] = { ...node, type: val }
                          setNodes(updated)
                        }}
                      >
                        <SelectTrigger className="h-8 text-[12px]">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent className="text-[12px]">
                          <SelectItem value="script">剧本生成 (script)</SelectItem>
                          <SelectItem value="storyboard">分镜拆解 (storyboard)</SelectItem>
                          <SelectItem value="asset">生成资源 (asset)</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                  </div>

                  {(node.type === "script" || node.type === "storyboard") && (
                    <div className="flex flex-col gap-1">
                      <Label className="text-[11px] text-text-2">系统提示词 (Prompt Library)</Label>
                      <Select
                        value={node.promptId || "__default__"}
                        onValueChange={(val) => {
                          const updated = [...nodes]
                          updated[index] = { ...node, promptId: val === "__default__" ? "" : val }
                          setNodes(updated)
                        }}
                      >
                        <SelectTrigger className="h-8 text-[12px]">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent className="text-[12px]">
                          <SelectItem value="__default__">使用系统内置默认提示词</SelectItem>
                          {prompts?.map((p) => (
                            <SelectItem key={p.id} value={p.id}>
                              {p.name} ({p.style || "通用"})
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                  )}

                  <div className="flex flex-col gap-1">
                    <Label className="text-[11px] text-text-2">依赖节点 (在这些节点完成后执行)</Label>
                    <div className="flex flex-wrap gap-x-3 gap-y-1 p-2 border border-border rounded bg-background/50">
                      {nodes
                        .filter((n) => n.id !== node.id && n.id !== "")
                        .map((otherNode) => {
                          const isChecked = node.dependsOn.includes(otherNode.id)
                          return (
                            <label key={otherNode.id} className="flex items-center gap-1.5 text-[11.5px] text-text-2 cursor-pointer select-none">
                              <input
                                type="checkbox"
                                className="rounded text-primary border-input focus:ring-0 cursor-pointer"
                                checked={isChecked}
                                onChange={(e) => {
                                  const checked = e.target.checked
                                  const updated = [...nodes]
                                  if (checked) {
                                    updated[index] = {
                                      ...node,
                                      dependsOn: [...node.dependsOn, otherNode.id],
                                    }
                                  } else {
                                    updated[index] = {
                                      ...node,
                                      dependsOn: node.dependsOn.filter((d) => d !== otherNode.id),
                                    }
                                  }
                                  setNodes(updated)
                                }}
                              />
                              {otherNode.id || "未命名节点"}
                            </label>
                          )
                        })}
                      {nodes.filter((n) => n.id !== node.id && n.id !== "").length === 0 && (
                        <span className="text-[11px] text-text-3">无其他节点可作为依赖</span>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {submitError && (
        <p role="alert" className="text-[12px] text-danger">
          {submitError}
        </p>
      )}

      <DialogFooter className="mt-4 pb-2">
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
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>编辑模型与工作流配置</DialogTitle>
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
