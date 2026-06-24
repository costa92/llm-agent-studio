import { NODE_COLOR } from "./nodeColor"
import { useBuiltinNodeTypes } from "@/features/builtin-node-types/api"

export interface PickerCustomType {
  type: string
  label: string
  color: string
  // typeId 非空 = org 注册表 typed 节点（可运行）；无 = annotation（Phase 1 草图）。
  typeId?: string
}

export interface NodeTypePickerProps {
  open: boolean
  screenX: number
  screenY: number
  customTypes?: PickerCustomType[]
  // 内置项：onPick(type)；annotation 自定义项：onPick(type, {label,color})；
  // typed 自定义项：onPick(type, {label,color,typeId})。
  onPick: (type: string, display?: { label: string; color: string; typeId?: string }) => void
  onClose: () => void
}

export function NodeTypePicker({
  open, screenX, screenY, customTypes = [], onPick, onClose,
}: NodeTypePickerProps) {
  const { data: builtins = [] } = useBuiltinNodeTypes()
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
        {builtins.map((b) => (
          <button
            key={b.type}
            type="button"
            role="menuitem"
            onClick={() => onPick(b.type)}
            className="flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-[12px] text-text-1 hover:bg-bg-surface"
          >
            <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ backgroundColor: NODE_COLOR[b.type] }} />
            {b.label}
          </button>
        ))}
        {customTypes.length > 0 && <div className="my-1 border-t border-line" />}
        {customTypes.map((c) => (
          <button
            key={c.typeId ?? c.type}
            type="button"
            role="menuitem"
            data-typed={c.typeId ? "true" : undefined}
            onClick={() => onPick(c.type, { label: c.label, color: c.color, ...(c.typeId ? { typeId: c.typeId } : {}) })}
            className="flex w-full items-center gap-2 rounded px-2.5 py-1.5 text-left text-[12px] text-text-1 hover:bg-bg-surface"
          >
            <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ backgroundColor: c.color }} />
            <span className="flex-1">{c.label}</span>
            {c.typeId && (
              <span
                aria-label="typed"
                className="rounded bg-amber/20 px-1 text-[10px] font-medium text-amber"
              >
                typed
              </span>
            )}
          </button>
        ))}
      </div>
    </>
  )
}
