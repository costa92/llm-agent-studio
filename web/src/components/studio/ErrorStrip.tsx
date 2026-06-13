import type { ComponentPropsWithoutRef } from "react"
import { cn } from "@/lib/utils"

// 红色错误条：常驻显示 run 最后一条失败原因。区别于 amber WarnStrip（fallback 提示）
// ——role=alert 让屏幕阅读器主动播报，符合 ARIA live=assertive 语义。
export function ErrorStrip({ className, ...props }: ComponentPropsWithoutRef<"div">) {
  return (
    <div
      role="alert"
      className={cn(
        "rounded-md border border-danger/40 bg-danger/10 px-2.5 py-2 text-[11.5px] leading-relaxed text-danger",
        className,
      )}
      {...props}
    />
  )
}
