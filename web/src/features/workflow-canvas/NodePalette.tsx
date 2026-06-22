import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"

// 节点面板（Phase 2）：列出可拖入的节点类型 chips。dragstart 写入节点类型，
// 画布的 onDrop 读取后在落点创建节点。标准管线一键填充留到 Phase 3。
const PALETTE_TYPES = ["script", "storyboard", "asset"] as const

// 与画布 onDrop 约定的 dataTransfer key。
export const PALETTE_DND_TYPE = "application/studio-node-type"

export function NodePalette() {
  return (
    <aside className="flex w-44 shrink-0 flex-col gap-3 border-r border-line bg-bg-surface p-3">
      <h4 className="text-[11px] font-semibold uppercase tracking-wider text-text-3">
        节点
      </h4>
      <div className="flex flex-col gap-2">
        {PALETTE_TYPES.map((t) => (
          <div
            key={t}
            data-slot="palette-chip"
            draggable
            onDragStart={(e) => {
              e.dataTransfer.setData(PALETTE_DND_TYPE, t)
              e.dataTransfer.effectAllowed = "move"
            }}
            className="flex cursor-grab items-center gap-2 rounded-md border border-line bg-bg-base px-2.5 py-1.5 hover:border-text-3 active:cursor-grabbing"
            title="拖入画布添加"
          >
            <span
              aria-hidden
              className="h-2.5 w-2.5 rounded-full"
              style={{ backgroundColor: NODE_COLOR[t] }}
            />
            <span className="text-[12px] text-text-1">{TYPE_LABEL[t]}</span>
          </div>
        ))}
      </div>
      <button
        type="button"
        disabled
        className="mt-1 cursor-not-allowed rounded-md border border-line px-2.5 py-1.5 text-[12px] text-text-2 opacity-60"
        title="一键填充标准管线（即将上线）"
      >
        标准管线
      </button>
    </aside>
  )
}
