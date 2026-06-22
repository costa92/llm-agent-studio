import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"

// 浮层节点类型选择器（Phase B）：从句柄拖到空白处、或点边上的「+」时弹出，
// 选中后由画布层创建（并在 B2 连接 / B4 拆边）。受控组件，画布层持有 open / 坐标。
const PICKER_TYPES = ["script", "storyboard", "asset"] as const

export interface NodeTypePickerProps {
  open: boolean
  // 浮层定位用的屏幕坐标（fixed 定位，left/top）。
  screenX: number
  screenY: number
  onPick: (type: string) => void
  onClose: () => void
}

export function NodeTypePicker({
  open,
  screenX,
  screenY,
  onPick,
  onClose,
}: NodeTypePickerProps) {
  if (!open) return null
  return (
    <>
      {/* 透明全屏遮罩：点击浮层外区域关闭。 */}
      <div
        data-slot="picker-overlay"
        className="fixed inset-0 z-40"
        onClick={onClose}
      />
      <div
        data-slot="node-type-picker"
        role="menu"
        className="fixed z-50 rounded-md border border-line bg-bg-raised p-1 shadow-lg"
        style={{ left: screenX, top: screenY }}
      >
        {PICKER_TYPES.map((t) => (
          <button
            key={t}
            type="button"
            role="menuitem"
            onClick={() => onPick(t)}
            className="flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-[12px] text-text-1 hover:bg-bg-surface"
          >
            <span
              aria-hidden
              className="h-2.5 w-2.5 shrink-0 rounded-full"
              style={{ backgroundColor: NODE_COLOR[t] }}
            />
            {TYPE_LABEL[t]}
          </button>
        ))}
      </div>
    </>
  )
}
