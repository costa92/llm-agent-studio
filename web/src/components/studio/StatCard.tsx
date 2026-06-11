import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

// 原型 .stat: bg-surface; border line; radius 12px; padding 18px
// .lab 11.5px text-3; .num 700/30px display; .num small 14px text-3/500
export interface StatCardProps {
  label: ReactNode
  value: ReactNode
  unit?: ReactNode
  className?: string
}

export function StatCard({ label, value, unit, className }: StatCardProps) {
  return (
    <div className={cn("rounded-xl border border-line bg-bg-surface p-[18px]", className)}>
      <div className="mb-1.5 text-[11.5px] text-text-3">{label}</div>
      <div className="font-sans text-[30px] font-bold leading-none">
        {value}
        {unit != null && <small className="ml-1 text-[14px] font-medium text-text-3">{unit}</small>}
      </div>
    </div>
  )
}
