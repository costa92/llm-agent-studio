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
import { Button as UiButton } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

export interface DeleteProjectDialogProps {
  /** 待删项目（确认需逐字输入其名称）。 */
  project: { id: string; name: string }
  trigger: React.ReactNode
  /** 发 DELETE（useDeleteProject.mutateAsync）。 */
  onSubmit: () => Promise<unknown>
  /** 删除成功且弹窗已关后调用（导航/toast 由消费方接）。 */
  onSuccess?: () => void
}

// 删除项目的破坏性确认弹窗：输入项目名逐字匹配才启用「确认删除」。
// 软删语义：项目从列表/详情消失、在途 run 被取消；历史消费账单保留。
export function DeleteProjectDialog({
  project,
  trigger,
  onSubmit,
  onSuccess,
}: DeleteProjectDialogProps) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // 名称必须逐字匹配（破坏性操作惯例）。
  const nameMatches = name === project.name

  // 开关统一走这里（含「取消」按钮——直接 setOpen 不会触发受控 Dialog 的
  // onOpenChange），关闭即清态：下次打开是干净的确认框。
  function setDialogOpen(o: boolean) {
    setOpen(o)
    if (!o) {
      setName("")
      setError(null)
    }
  }

  async function confirm() {
    if (!nameMatches || pending) return
    setPending(true)
    setError(null)
    try {
      await onSubmit()
      setDialogOpen(false)
      onSuccess?.()
    } catch {
      setError("删除失败，请重试")
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setDialogOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>删除项目「{project.name}」？</DialogTitle>
          <DialogDescription>
            项目将从列表中消失，正在进行的运行会被取消。历史消费账单保留。
            此操作不可在产品内撤销。
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-2">
          <Label htmlFor="delete-project-name">
            输入项目名称 <b className="text-text-1">{project.name}</b> 以确认
          </Label>
          <Input
            id="delete-project-name"
            value={name}
            autoComplete="off"
            onChange={(e) => setName(e.target.value)}
            placeholder={project.name}
          />
        </div>
        {error && (
          <p role="alert" className="text-[12px] text-danger">
            {error}
          </p>
        )}
        <DialogFooter>
          <UiButton variant="outline" onClick={() => setDialogOpen(false)}>
            取消
          </UiButton>
          <UiButton
            variant="destructive"
            disabled={!nameMatches || pending}
            onClick={() => void confirm()}
          >
            {pending && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
            确认删除
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
