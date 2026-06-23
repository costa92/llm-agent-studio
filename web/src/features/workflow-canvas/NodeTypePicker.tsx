import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"

const PICKER_TYPES = ["script", "storyboard", "asset"] as const

export interface PickerCustomType {
  type: string
  label: string
  color: string
}

export interface NodeTypePickerProps {
  open: boolean
  screenX: number
  screenY: number
  customTypes?: PickerCustomType[]
  // 内置项：onPick(type)；自定义项：onPick(type, {label,color})。
  onPick: (type: string, display?: { label: string; color: string }) => void
  onClose: () => void
}

export function NodeTypePicker({
  open, screenX, screenY, customTypes = [], onPick, onClose,
}: NodeTypePickerProps) {
  if (!open) return null
  return (
    <>
      <div data-slot="picker-overlay" className="fixed inset-0 z-40" onClick={onClose} />
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
            <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ backgroundColor: NODE_COLOR[t] }} />
            {TYPE_LABEL[t]}
          </button>
        ))}
        {customTypes.length > 0 && <div className="my-1 border-t border-line" />}
        {customTypes.map((c) => (
          <button
            key={c.type}
            type="button"
            role="menuitem"
            onClick={() => onPick(c.type, { label: c.label, color: c.color })}
            className="flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-[12px] text-text-1 hover:bg-bg-surface"
          >
            <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ backgroundColor: c.color }} />
            {c.label}
          </button>
        ))}
      </div>
    </>
  )
}
