import { cn } from "@/lib/utils"

// 原型 .slate-bar：场记板运行条——absolute 底部 3px 琥珀斜条纹 slide 动画。
// planner_started 显示 / run_done 隐藏（由 timeline.slateVisible 驱动）。
// prefers-reduced-motion 下停动画（index.css 全局已停 animation）。
export interface SlateBarProps {
  visible: boolean
  className?: string
}

export function SlateBar({ visible, className }: SlateBarProps) {
  if (!visible) return null
  return (
    <div
      role="progressbar"
      aria-label="生产中"
      data-slot="slate-bar"
      className={cn(
        "absolute inset-x-0 -bottom-px h-[3px] bg-[length:28px_3px]",
        "bg-[repeating-linear-gradient(115deg,var(--amber)_0_10px,transparent_10px_20px)]",
        "motion-safe:animate-[slide_1s_linear_infinite]",
        className,
      )}
    />
  )
}
