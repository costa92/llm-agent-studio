import { useState } from "react"
import { toast } from "sonner"
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Button as UiButton } from "@/components/ui/button"
import { useSaveTemplate } from "@/features/projects/workflowTemplateApi"

// 「存为模板」对话框：把当前已保存的工作流快照存为组织级模板（后端 editor+）。
// 受控挂载（open/onOpenChange）；本文件只 export 组件 + props（lint react-refresh 铁律）。
export interface SaveTemplateDialogProps {
  org: string
  projectId: string
  workflowId: string
  // 名称默认值（当前工作流名）。
  defaultName?: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function SaveTemplateDialog({
  org,
  projectId,
  workflowId,
  defaultName = "",
  open,
  onOpenChange,
}: SaveTemplateDialogProps) {
  const [name, setName] = useState(defaultName)
  const [description, setDescription] = useState("")
  const save = useSaveTemplate(org)
  const valid = name.trim().length > 0

  const submit = () => {
    if (!valid || save.isPending) return
    save.mutate(
      { name: name.trim(), description: description.trim(), projectId, workflowId },
      {
        onSuccess: () => {
          toast.success("已存为模板")
          onOpenChange(false)
        },
        onError: () => toast.error("存为模板失败，请重试"),
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>存为模板</DialogTitle>
          <DialogDescription>
            把当前工作流快照存为组织模板，之后可在「从模板开始」里一键复用。
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3 py-2">
          <label className="flex flex-col gap-1 text-[12px] text-text-2">
            模板名
            <input
              aria-label="模板名"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="如：科普短视频管线"
              className="rounded-md border border-line bg-bg-base px-2 py-1.5 text-[13px] text-text-1 focus:border-amber focus:outline-none"
            />
          </label>
          <label className="flex flex-col gap-1 text-[12px] text-text-2">
            描述
            <textarea
              aria-label="描述"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="简述这个模板做什么（可选）"
              rows={3}
              className="resize-none rounded-md border border-line bg-bg-base px-2 py-1.5 text-[13px] text-text-1 focus:border-amber focus:outline-none"
            />
          </label>
        </div>
        <DialogFooter>
          <UiButton variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </UiButton>
          <UiButton disabled={!valid || save.isPending} onClick={submit}>
            {save.isPending ? "保存中…" : "确认"}
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
