import { NODE_COLOR } from "./nodeColor"
import { useBuiltinNodeTypes } from "@/features/builtin-node-types/api"

// 节点面板（Phase 2）：列出可拖入的节点类型 chips。dragstart 写入节点类型，
// 画布的 onDrop 读取后在落点创建节点。Phase 3：标准管线一键填充。
// 内置类型由后端目录 hook 数据驱动（staleTime Infinity，加载期为空可接受）。

// 与画布 onDrop 约定的 dataTransfer key。
export const PALETTE_DND_TYPE = "application/studio-node-type"

// typed 节点拖拽时额外携带 typeId（存到 dataTransfer 里，onDrop 读取并写入 display.typeId）。
export const PALETTE_DND_TYPEID = "application/studio-node-typeid"

export interface PaletteCustomType {
  type: string
  label: string
  color: string
  // typeId 非空 = org 注册表 typed 节点（可运行）；无 = annotation（Phase 1 草图）。
  typeId?: string
}

export interface NodePaletteProps {
  // 点「标准管线」一键把画布填充为 脚本→分镜（由画布层实现，含确认替换）。
  onStandardPipeline: () => void
  // 点「自动整理」按分层种子坐标重排现有节点（由画布层实现，可撤销 + fitView）。
  onAutoTidy?: () => void
  customTypes?: PaletteCustomType[]
  onAddCustomType?: () => void
  onEditCustomType?: (type: string) => void
}

export function NodePalette({ onStandardPipeline, onAutoTidy, customTypes, onAddCustomType, onEditCustomType }: NodePaletteProps) {
  const { data: builtins = [] } = useBuiltinNodeTypes()
  return (
    <aside className="flex w-44 shrink-0 flex-col gap-3 border-r border-line bg-bg-surface p-3">
      <h4 className="text-[11px] font-semibold uppercase tracking-wider text-text-3">
        节点
      </h4>
      <div className="flex flex-col gap-2">
        {builtins.map((b) => (
          <div
            key={b.type}
            data-slot="palette-chip"
            draggable
            onDragStart={(e) => {
              e.dataTransfer.setData(PALETTE_DND_TYPE, b.type)
              e.dataTransfer.effectAllowed = "move"
            }}
            className="flex cursor-grab items-center gap-2 rounded-md border border-line bg-bg-base px-2.5 py-1.5 hover:border-text-3 active:cursor-grabbing"
            title="拖入画布添加"
          >
            <span
              aria-hidden
              className="h-2.5 w-2.5 rounded-full"
              style={{ backgroundColor: NODE_COLOR[b.type] }}
            />
            <span className="text-[12px] text-text-1">{b.label}</span>
          </div>
        ))}
        {(customTypes ?? []).map((c) => (
          <div
            key={c.typeId ?? c.type}
            data-slot="palette-chip-custom"
            data-typeid={c.typeId}
            draggable
            onDragStart={(e) => {
              e.dataTransfer.setData(PALETTE_DND_TYPE, c.type)
              if (c.typeId) {
                e.dataTransfer.setData(PALETTE_DND_TYPEID, c.typeId)
              }
              e.dataTransfer.effectAllowed = "move"
            }}
            className="group flex cursor-grab items-center gap-2 rounded-md border border-line bg-bg-base px-2.5 py-1.5 hover:border-text-3 active:cursor-grabbing"
            title="拖入画布添加"
          >
            <span aria-hidden className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: c.color }} />
            <span className="flex-1 text-[12px] text-text-1">{c.label}</span>
            {c.typeId && (
              <span
                aria-label="typed"
                className="rounded bg-amber/20 px-1 text-[10px] font-medium text-amber"
              >
                T
              </span>
            )}
            {!c.typeId && onEditCustomType && (
              <button
                type="button"
                onClick={(e) => { e.stopPropagation(); onEditCustomType(c.type) }}
                className="text-[11px] text-text-3 opacity-0 group-hover:opacity-100 hover:text-text-1"
              >
                编辑
              </button>
            )}
          </div>
        ))}
        {onAddCustomType && (
          <button
            type="button"
            onClick={onAddCustomType}
            className="rounded-md border border-dashed border-line px-2.5 py-1.5 text-left text-[12px] text-text-3 hover:border-text-3 hover:text-text-1"
          >
            + 自定义类型
          </button>
        )}
      </div>
      <button
        type="button"
        onClick={onStandardPipeline}
        className="mt-1 rounded-md border border-amber/30 px-2.5 py-1.5 text-[12px] font-medium text-amber hover:border-amber"
        title="一键填充标准管线（脚本 → 分镜）"
      >
        标准管线
      </button>
      {onAutoTidy && (
        <button
          type="button"
          onClick={onAutoTidy}
          className="rounded-md border border-line px-2.5 py-1.5 text-[12px] font-medium text-text-2 hover:border-text-3 hover:text-text-1"
          title="按分层重新排列画布节点"
        >
          自动整理
        </button>
      )}
    </aside>
  )
}
