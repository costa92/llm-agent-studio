import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button as UiButton } from "@/components/ui/button"

// 批量退回确认弹窗（审核看板）：与单张 ConfirmRejectDialog 同款终态守卫，标题带张数。
// 退回后这些资产被标记为 rejected，且无法撤销（后端无 un-reject 端点），故仅
// 「确认退回」才提交；「取消」/关闭零副作用。
export interface ConfirmRejectManyDialogProps {
  count: number
  open: boolean
  onOpenChange: (open: boolean) => void
  // 「确认退回」点击回调；「取消」/关闭零副作用。
  onConfirm: () => void
}

export function ConfirmRejectManyDialog({
  count,
  open,
  onOpenChange,
  onConfirm,
}: ConfirmRejectManyDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>确认退回选中的 {count} 张资产？</DialogTitle>
          <DialogDescription>
            退回后这些资产将被标记为 rejected，且无法撤销。确认要退回吗？
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <UiButton variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </UiButton>
          <UiButton variant="destructive" onClick={onConfirm}>
            确认退回
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
