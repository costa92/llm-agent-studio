import { useState } from "react"
import type { InspectorItem } from "@/lib/projectState"
import { BinaryItemView } from "./BinaryItemView"

// ItemInspector：运行视图右栏的 per-item inspector（workflow-v2 P5d）。
// 逐条渲染某节点的 node_outputs.items：
//   - item.json 形如 {text:"..."} → 纯文本（与今天的文本面板一致）。
//   - item.json 带 _parseError → 「原始内容解析失败」轻提示（历史解析失败行）。
//   - 其余对象 → pretty-print JSON（复用 RunCanvas 的 <pre> 风格）。
//   - item.binary → 受控资产 chip（仅展示 ref 字段，绝不直拉字节）。
// N>1 时给 prev/next 索引切换器；N==1 不给。
// 仅当 items?.length 时由调用方渲染本组件；空/缺省由调用方回落标量面板。
export interface ItemInspectorProps {
  items: InspectorItem[]
}

export function ItemInspector({ items }: ItemInspectorProps) {
  const [index, setIndex] = useState(0)
  // safeIndex 在渲染期钳位越界（切换节点后 items 变短时无需 effect 回写 state）——
  // 显示 / 禁用态 / 上下条导航都基于 safeIndex，存储的 index 永不外露越界值。
  const safeIndex = Math.min(index, Math.max(0, items.length - 1))
  const item = items[safeIndex]
  const multi = items.length > 1

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <p className="text-[11px] text-text-3">
          {items.length} 项{multi ? ` · 第 ${safeIndex + 1} 项` : ""}
        </p>
        {multi && (
          <div className="flex items-center gap-1">
            <button
              type="button"
              aria-label="上一项"
              disabled={safeIndex === 0}
              onClick={() => setIndex(Math.max(0, safeIndex - 1))}
              className="rounded border border-line bg-bg-base px-1.5 py-0.5 text-[11px] text-text-2 disabled:opacity-40 hover:text-text-1"
            >
              ‹
            </button>
            <button
              type="button"
              aria-label="下一项"
              disabled={safeIndex === items.length - 1}
              onClick={() => setIndex(Math.min(items.length - 1, safeIndex + 1))}
              className="rounded border border-line bg-bg-base px-1.5 py-0.5 text-[11px] text-text-2 disabled:opacity-40 hover:text-text-1"
            >
              ›
            </button>
          </div>
        )}
      </div>

      {item && <ItemBody item={item} />}
    </div>
  )
}

// 单条 item 的主体渲染：text → 纯文本；_parseError → 提示；其余 → JSON。
function ItemBody({ item }: { item: InspectorItem }) {
  const json = item.json
  const obj = isPlainObject(json) ? json : null
  const text = obj && typeof obj.text === "string" ? obj.text : null
  const parseError = obj ? "_parseError" in obj : false

  return (
    <div className="flex flex-col gap-2">
      {parseError ? (
        <p className="text-[11px] text-text-3">原始内容解析失败（展示原始 JSON）</p>
      ) : null}
      {text !== null ? (
        <p className="whitespace-pre-wrap break-words rounded-md border border-line bg-bg-base p-2 text-[11px] leading-relaxed text-text-1">
          {text}
        </p>
      ) : (
        <pre className="overflow-auto whitespace-pre-wrap break-words rounded-md border border-line bg-bg-base p-2 text-[11px] leading-relaxed text-text-1">
          {safeStringify(json)}
        </pre>
      )}

      {item.binary && Object.keys(item.binary).length > 0 && (
        <div className="flex flex-col gap-1.5">
          <p className="text-[11px] text-text-3">二进制产物</p>
          {Object.entries(item.binary).map(([key, ref]) => (
            <BinaryItemView key={key} name={key} ref_={ref} />
          ))}
        </div>
      )}
    </div>
  )
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

// JSON.stringify 防御循环引用 / 不可序列化值。
function safeStringify(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}
