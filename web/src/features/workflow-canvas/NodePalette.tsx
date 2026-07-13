import { useMemo, useState } from "react"
import { LayoutGrid } from "lucide-react"
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

// 快建 chip 的 kind（→ 打开类型化创建对话框预置该 kind）。这些都是注册表支撑的
// 可运行类型（最终生成带 typeId 的 custom:<slug> chip），与「+ 自定义类型」注释草图不同。
export type QuickCreateKind = "llm" | "http" | "script"

// 可运行快建 chip 定义。脚本明确标注 Starlark，区别于内置「剧本」(bare script)。
const QUICK_CREATE_CHIPS: { kind: QuickCreateKind; label: string; title: string }[] = [
  { kind: "llm", label: "+ LLM 节点", title: "新建一个 LLM 类型（注册表支撑，可运行）" },
  { kind: "http", label: "+ HTTP 节点", title: "新建一个 HTTP 类型（注册表支撑，可运行）" },
  { kind: "script", label: "+ 脚本节点(Starlark)", title: "新建一个 Starlark 脚本变换类型（注册表支撑，可运行；区别于「剧本」）" },
]

export interface NodePaletteProps {
  // 点「标准管线」一键把画布填充为 脚本→分镜（由画布层实现，含确认替换）。
  onStandardPipeline: () => void
  // 点「从模板开始」打开案例模板选择器（由画布层实现，选中后新建工作流并跳转）。
  onOpenTemplates?: () => void
  // 点「自动整理」按分层种子坐标重排现有节点（由画布层实现，可撤销 + fitView）。
  onAutoTidy?: () => void
  customTypes?: PaletteCustomType[]
  // 快建可运行类型：打开类型化创建对话框并预置 kind（llm/http/script）。
  // 保存后经注册表创建 → 新的 typed chip 自动出现 → 用户拖入画布。
  onQuickCreate?: (kind: QuickCreateKind) => void
  onAddCustomType?: () => void
  onEditCustomType?: (type: string) => void
  // 打开「节点管理」模态（系统节点只读目录 + 用户自定义节点 CRUD）。
  onOpenManager?: () => void
}

export function NodePalette({ onStandardPipeline, onOpenTemplates, onAutoTidy, customTypes, onQuickCreate, onAddCustomType, onEditCustomType, onOpenManager }: NodePaletteProps) {
  const { data: builtins = [] } = useBuiltinNodeTypes()
  // 搜索过滤（系统节点按 label/type/职责描述；自定义节点按 label/type），纯派生（禁 effect setState）。
  const [query, setQuery] = useState("")
  const q = query.trim().toLowerCase()
  const filteredBuiltins = useMemo(
    () =>
      q
        ? builtins.filter(
            (b) =>
              b.label.toLowerCase().includes(q) ||
              b.type.toLowerCase().includes(q) ||
              (b.description ?? "").toLowerCase().includes(q),
          )
        : builtins,
    [builtins, q],
  )
  const filteredCustom = useMemo(() => {
    const list = customTypes ?? []
    return q
      ? list.filter(
          (c) => c.label.toLowerCase().includes(q) || c.type.toLowerCase().includes(q),
        )
      : list
  }, [customTypes, q])
  const hasCustom = (customTypes?.length ?? 0) > 0

  return (
    <aside className="flex w-48 shrink-0 flex-col gap-3 overflow-y-auto border-r border-line bg-bg-surface p-3">
      <h4 className="text-[11px] font-semibold uppercase tracking-wider text-text-3">
        节点
      </h4>
      <input
        type="search"
        data-slot="palette-search"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder="搜索节点…"
        aria-label="搜索节点"
        className="h-7 rounded-md border border-line bg-bg-base px-2 text-[12px] text-text-1 placeholder:text-text-3 focus:border-amber focus:outline-none"
      />

      {/* 节点管理入口：打开模态查看系统节点只读目录 + 管理用户自定义节点。 */}
      {onOpenManager && (
        <button
          type="button"
          data-slot="palette-manage"
          onClick={onOpenManager}
          className="flex items-center justify-center gap-1.5 rounded-md border border-line bg-bg-base px-2.5 py-1.5 text-[12px] text-text-2 hover:border-text-3 hover:text-text-1"
          title="打开节点管理（系统节点目录 + 自定义节点增删改）"
        >
          <LayoutGrid className="h-3.5 w-3.5" />
          节点管理
        </button>
      )}

      {/* 系统节点：后端目录数据驱动，含一句话职责（description）。一个节点做一件事。 */}
      <section className="flex flex-col gap-2">
        <h5 className="text-[10px] font-semibold uppercase tracking-wider text-text-3">
          系统节点
        </h5>
        {filteredBuiltins.map((b) => (
          <div
            key={b.type}
            data-slot="palette-chip"
            draggable
            onDragStart={(e) => {
              e.dataTransfer.setData(PALETTE_DND_TYPE, b.type)
              e.dataTransfer.effectAllowed = "move"
            }}
            className="flex cursor-grab items-start gap-2 rounded-md border border-line bg-bg-base px-2.5 py-1.5 hover:border-text-3 active:cursor-grabbing"
            title={b.description || "拖入画布添加"}
          >
            <span
              aria-hidden
              className="mt-1 h-2.5 w-2.5 shrink-0 rounded-full"
              style={{ backgroundColor: NODE_COLOR[b.type] }}
            />
            <div className="flex min-w-0 flex-col">
              <span className="text-[12px] text-text-1">{b.label}</span>
              {b.description && (
                <span className="line-clamp-2 text-[10.5px] leading-snug text-text-3">
                  {b.description}
                </span>
              )}
            </div>
          </div>
        ))}
        {q && filteredBuiltins.length === 0 && (
          <p className="px-1 text-[11px] text-text-3">无匹配系统节点</p>
        )}
      </section>

      {/* 用户自定义节点：org 注册表 typed 节点（带 T）+ 画布注释草图。一句话职责暂无（自定义类型未存描述）。 */}
      {hasCustom && (
        <section className="flex flex-col gap-2">
          <h5 className="text-[10px] font-semibold uppercase tracking-wider text-text-3">
            用户自定义节点
          </h5>
          {filteredCustom.map((c) => (
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
              <span aria-hidden className="h-2.5 w-2.5 shrink-0 rounded-full" style={{ backgroundColor: c.color }} />
              <span className="flex-1 truncate text-[12px] text-text-1">{c.label}</span>
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
          {q && filteredCustom.length === 0 && (
            <p className="px-1 text-[11px] text-text-3">无匹配自定义节点</p>
          )}
        </section>
      )}

      {/* 可运行类型：快建 LLM / HTTP / 脚本(Starlark) 类型。打开类型化创建对话框预置
          kind，保存后经注册表生成带 typeId 的可拖拽 chip（可运行）。与下方「注释」区分。 */}
      {onQuickCreate && (
        <div className="flex flex-col gap-2">
          <h4 className="text-[11px] font-semibold uppercase tracking-wider text-text-3">
            可运行类型
          </h4>
          {QUICK_CREATE_CHIPS.map((chip) => (
            <button
              key={chip.kind}
              type="button"
              onClick={() => onQuickCreate(chip.kind)}
              title={chip.title}
              className="rounded-md border border-dashed border-amber/30 px-2.5 py-1.5 text-left text-[12px] text-amber hover:border-amber hover:text-amber"
            >
              {chip.label}
            </button>
          ))}
        </div>
      )}

      {/* 注释：仅作画布草图标记，无 typeId、不可运行。与上方可运行类型刻意分组区分。 */}
      {onAddCustomType && (
        <div className="flex flex-col gap-2">
          <h4 className="text-[11px] font-semibold uppercase tracking-wider text-text-3">
            注释
          </h4>
          <button
            type="button"
            onClick={onAddCustomType}
            title="添加一个不可运行的注释/草图节点（无类型参数）"
            className="rounded-md border border-dashed border-line px-2.5 py-1.5 text-left text-[12px] text-text-3 hover:border-text-3 hover:text-text-1"
          >
            + 自定义类型
          </button>
        </div>
      )}
      <button
        type="button"
        onClick={onStandardPipeline}
        className="mt-1 rounded-md border border-amber/30 px-2.5 py-1.5 text-[12px] font-medium text-amber hover:border-amber"
        title="一键填充标准管线（脚本 → 分镜）"
      >
        标准管线
      </button>
      {onOpenTemplates && (
        <button
          type="button"
          onClick={onOpenTemplates}
          className="rounded-md border border-amber/30 px-2.5 py-1.5 text-[12px] font-medium text-amber hover:border-amber"
          title="从案例模板快速创建一条工作流"
        >
          从模板开始
        </button>
      )}
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
