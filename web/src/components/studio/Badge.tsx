import type { ComponentPropsWithoutRef } from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

// 原型 .badge: inline-flex; gap 5px; padding 2px 9px; radius 999px; font 11px/500; 带 .dot 6px 圆点
// 变体取自原型 b-running / b-done / b-pend / b-rej
const badgeVariants = cva(
  "inline-flex shrink-0 items-center gap-[5px] whitespace-nowrap rounded-full px-[9px] py-0.5 text-[11px] font-medium",
  {
    variants: {
      variant: {
        running: "bg-amber/12 text-amber",
        done: "bg-review/12 text-review",
        pending: "bg-amber/12 text-amber",
        rejected: "bg-danger/12 text-danger",
      },
    },
    defaultVariants: { variant: "running" },
  },
)

const dotVariants = cva("h-1.5 w-1.5 rounded-full", {
  variants: {
    variant: {
      running: "bg-amber motion-safe:animate-pulse",
      done: "bg-review",
      pending: "bg-amber",
      rejected: "bg-danger",
    },
  },
  defaultVariants: { variant: "running" },
})

export interface StudioBadgeProps
  extends ComponentPropsWithoutRef<"span">,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, variant, children, ...props }: StudioBadgeProps) {
  return (
    <span className={cn(badgeVariants({ variant }), className)} {...props}>
      <span className={dotVariants({ variant })} />
      {children}
    </span>
  )
}
