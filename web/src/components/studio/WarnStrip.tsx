import type { ComponentPropsWithoutRef } from "react"
import { cn } from "@/lib/utils"

// 原型 .warn-strip: bg amber/10; border amber/30; radius 8px; padding 8px 10px; 11.5px; text amber
export function WarnStrip({ className, ...props }: ComponentPropsWithoutRef<"div">) {
  return (
    <div
      role="status"
      className={cn(
        "rounded-md border border-amber/30 bg-amber/10 px-2.5 py-2 text-[11.5px] text-amber",
        className,
      )}
      {...props}
    />
  )
}
