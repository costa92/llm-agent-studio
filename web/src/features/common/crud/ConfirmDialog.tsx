import type { ReactNode } from "react"
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Button as UiButton } from "@/components/ui/button"

export interface ConfirmDialogProps {
  open: boolean
  title: string
  description?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  variant?: "danger" | "default"
  confirming?: boolean
  onConfirm: () => void
  onCancel: () => void
}

// 通用二次确认。删除/移除/撤销/重置共用；danger 用 destructive 按钮。
export function ConfirmDialog({
  open, title, description, confirmLabel = "确认", cancelLabel = "取消",
  variant = "danger", confirming = false, onConfirm, onCancel,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onCancel() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description != null && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        <DialogFooter>
          <UiButton variant="outline" onClick={onCancel}>{cancelLabel}</UiButton>
          <UiButton
            variant={variant === "danger" ? "destructive" : "default"}
            disabled={confirming}
            onClick={onConfirm}
          >
            {confirmLabel}
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
