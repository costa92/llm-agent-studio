import type { ComponentPropsWithoutRef, ReactNode } from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Kbd } from "./Kbd"

// 原型 .btn: padding 7px 16px; radius 8px; font 13px/500
// 变体: btn-amber / btn-ghost / btn-green / btn-red
const buttonVariants = cva(
  "inline-flex items-center justify-center rounded-md px-4 py-[7px] text-[13px] font-medium transition-[0.15s] disabled:pointer-events-none disabled:opacity-50",
  {
    variants: {
      variant: {
        amber: "bg-amber text-[#1a1408] hover:bg-[#f0b257]",
        ghost: "border border-line text-text-2 hover:border-text-3 hover:text-text-1",
        green: "border border-review/40 bg-review/15 text-review",
        red: "border border-danger/35 bg-danger/12 text-danger",
      },
    },
    defaultVariants: { variant: "ghost" },
  },
)

export interface StudioButtonProps
  extends ComponentPropsWithoutRef<"button">,
    VariantProps<typeof buttonVariants> {
  kbd?: ReactNode
}

export function Button({ className, variant, kbd, children, ...props }: StudioButtonProps) {
  return (
    <button className={cn(buttonVariants({ variant }), className)} {...props}>
      {children}
      {kbd != null && <Kbd>{kbd}</Kbd>}
    </button>
  )
}
