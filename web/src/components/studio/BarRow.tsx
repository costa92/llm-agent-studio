import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

// 原型 .bar-row: grid 130px 1fr 70px; gap 12px; 12px
// .bar-bg: h10 radius5 bg-base; .bar-fill: h100% radius5（条色轮换 agent 色，由 color 注入）
export interface BarRowProps {
  label: ReactNode
  // 0..1 占比
  ratio: number
  value: ReactNode
  // 条填充色（CSS color，默认琥珀）—— 调用方按 agent 色轮换
  color?: string
  className?: string
}

export function BarRow({ label, ratio, value, color = "var(--amber)", className }: BarRowProps) {
  const pct = Math.max(0, Math.min(1, ratio)) * 100
  return (
    <div
      className={cn(
        "grid grid-cols-[130px_1fr_70px] items-center gap-3 py-[7px] text-[12px]",
        className,
      )}
    >
      <span className="truncate text-text-2">{label}</span>
      <span className="h-2.5 overflow-hidden rounded-[5px] bg-bg-base">
        <span
          className="block h-full rounded-[5px]"
          style={{ width: `${pct}%`, background: color }}
        />
      </span>
      <span className="text-right font-mono text-text-1">{value}</span>
    </div>
  )
}
