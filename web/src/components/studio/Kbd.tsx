import type { ComponentPropsWithoutRef } from "react"
import { cn } from "@/lib/utils"

// 原型 kbd: font:600 10px mono; bg raised; border line; radius 4px; padding 1px 5px; text-2; margin-left 6px
export function Kbd({ className, ...props }: ComponentPropsWithoutRef<"kbd">) {
  return (
    <kbd
      className={cn(
        "ml-1.5 inline-block rounded-[4px] border border-line bg-bg-raised px-[5px] py-px font-mono text-[10px] font-semibold text-text-2",
        className,
      )}
      {...props}
    />
  )
}
