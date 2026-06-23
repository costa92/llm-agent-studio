import { useState } from "react"
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Button as UiButton } from "@/components/ui/button"
import { CUSTOM_PALETTE } from "./nodeColor"

export interface CustomTypePayload {
  label: string
  color: string
}

export interface CustomTypeDialogProps {
  open: boolean
  mode: "create" | "edit"
  initial?: CustomTypePayload
  onSubmit: (payload: CustomTypePayload) => void
  onCancel: () => void
}

// 新建/编辑自定义类型：显示名 + 预设调色板单选。slug/类型由画布层根据 label 生成。
export function CustomTypeDialog({
  open, mode, initial, onSubmit, onCancel,
}: CustomTypeDialogProps) {
  const [label, setLabel] = useState(initial?.label ?? "")
  const [color, setColor] = useState(initial?.color ?? CUSTOM_PALETTE[0])
  const valid = label.trim().length > 0

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onCancel() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{mode === "create" ? "新建自定义类型" : "编辑自定义类型"}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-3 py-2">
          <label className="flex flex-col gap-1 text-[12px] text-text-2">
            显示名
            <input
              aria-label="显示名"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="如：翻译 / 配音脚本"
              className="rounded-md border border-line bg-bg-base px-2 py-1.5 text-[13px] text-text-1 focus:border-amber focus:outline-none"
            />
          </label>
          <div className="flex flex-col gap-1.5 text-[12px] text-text-2">
            颜色
            <div className="flex flex-wrap gap-2">
              {CUSTOM_PALETTE.map((c) => (
                <button
                  key={c}
                  type="button"
                  aria-label={`颜色 ${c}`}
                  onClick={() => setColor(c)}
                  className={
                    "h-6 w-6 rounded-full border-2 " +
                    (color === c ? "border-text-1" : "border-transparent")
                  }
                  style={{ backgroundColor: c }}
                />
              ))}
            </div>
          </div>
        </div>
        <DialogFooter>
          <UiButton variant="outline" onClick={onCancel}>取消</UiButton>
          <UiButton
            disabled={!valid}
            onClick={() => onSubmit({ label: label.trim(), color })}
          >
            确认
          </UiButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
