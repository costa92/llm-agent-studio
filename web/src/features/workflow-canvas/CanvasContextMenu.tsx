// 通用右键上下文菜单（Phase D）：透明遮罩点外关闭 + fixed 定位浮层。
// 菜单项由画布层按右键目标（pane/node/edge）构建并下发；选项点击后自动关菜单。
export interface ContextMenuItem {
  label: string
  onClick: () => void
  danger?: boolean
  disabled?: boolean
}

export interface CanvasContextMenuProps {
  open: boolean
  screenX: number
  screenY: number
  items: ContextMenuItem[]
  onClose: () => void
}

export function CanvasContextMenu({
  open,
  screenX,
  screenY,
  items,
  onClose,
}: CanvasContextMenuProps) {
  if (!open) return null
  return (
    <>
      <div
        data-testid="context-menu-overlay"
        className="fixed inset-0 z-40"
        onClick={onClose}
      />
      <div
        data-slot="canvas-context-menu"
        role="menu"
        className="fixed z-50 min-w-[140px] rounded-md border border-line bg-bg-raised p-1 shadow-lg"
        style={{ left: screenX, top: screenY }}
      >
        {items.map((item) => (
          <button
            key={item.label}
            type="button"
            role="menuitem"
            disabled={item.disabled}
            onClick={() => {
              if (item.disabled) return
              item.onClick()
              onClose()
            }}
            className={
              "flex w-full items-center rounded px-2.5 py-1.5 text-left text-[12px] hover:bg-bg-surface disabled:cursor-not-allowed disabled:opacity-40 " +
              (item.danger ? "text-danger" : "text-text-1")
            }
          >
            {item.label}
          </button>
        ))}
      </div>
    </>
  )
}
