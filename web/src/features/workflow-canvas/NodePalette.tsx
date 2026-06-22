import { NODE_COLOR, TYPE_LABEL } from "./nodeColor"

// 节点面板（Phase 1 占位，纯展示）：列出可拖入的节点类型 chips + 标准管线按钮。
// 拖拽添加 / 一键填充 行为留到 Phase 2 接线，当前均不可用。
const PALETTE_TYPES = ["script", "storyboard", "asset"] as const

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
            aria-disabled
            className="flex cursor-not-allowed items-center gap-2 rounded-md border border-line bg-bg-base px-2.5 py-1.5 opacity-70"
            title="拖入添加（即将上线）"
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
