import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button as UiButton } from "@/components/ui/button"

// 退回确认弹窗（共享）：审核看板 + 工作台选中面板共用，消除「就地退回无确认」的不一致。
// owner 推翻 UI-spec §11 默认的可撤销 toast——「关闭 undo toast 即提交」是静默退回陷阱，
// 改为模态确认，仅「确认退回」才提交。后端无 un-reject 端点，故确认即终态。
export interface ConfirmRejectDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  // 「确认退回」点击回调；「取消」/关闭零副作用。
  onConfirm: () => void
}

export function ConfirmRejectDialog({
  open,
  onOpenChange,
  onConfirm,
}: ConfirmRejectDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>确认退回该资产？</DialogTitle>
          <DialogDescription>
            退回后该资产将被标记为 rejected，且无法撤销。确认要退回吗？
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
