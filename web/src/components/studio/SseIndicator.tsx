import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

// 原型 .sse-dot: flex; gap 6px; 11px; text-3; i = 7px 圆点 review 色 pulse 2s
// 连接态扩展: connected(绿 pulse) / reconnecting(琥珀 pulse) / disconnected(danger 静止)
export type SseStatus = "connected" | "reconnecting" | "disconnected"

const dotVariants = cva("h-[7px] w-[7px] rounded-full", {
  variants: {
    status: {
      connected: "bg-review motion-safe:animate-pulse",
      reconnecting: "bg-amber motion-safe:animate-pulse",
      disconnected: "bg-danger",
    },
  },
  defaultVariants: { status: "connected" },
})

const LABELS: Record<SseStatus, string> = {
  connected: "实时连接",
  reconnecting: "重连中…",
  disconnected: "已断开",
}

export interface SseIndicatorProps extends VariantProps<typeof dotVariants> {
  status: SseStatus
  className?: string
}

export function SseIndicator({ status, className }: SseIndicatorProps) {
  return (
    <span className={cn("flex items-center gap-1.5 text-[11px] text-text-3", className)}>
      <i className={dotVariants({ status })} />
      {LABELS[status]}
    </span>
  )
}
