import type { ComponentPropsWithoutRef } from "react"
import { cn } from "@/lib/utils"

// 原型 .prompt-box：mono 11px，展示 prompt 文本（只读）。
// 可编辑态（重生成）用 <textarea> 由调用方组合，本组件只负责只读展示。
export interface PromptBoxProps extends ComponentPropsWithoutRef<"div"> {
  prompt: string
}

export function PromptBox({ prompt, className, ...props }: PromptBoxProps) {
  return (
    <div
      data-slot="prompt-box"
      className={cn(
        "whitespace-pre-wrap break-words rounded-md border border-line bg-bg-base px-3 py-2.5 font-mono text-[11px] leading-relaxed text-text-2",
        className,
      )}
      {...props}
    >
      {prompt || "（无 prompt）"}
    </div>
  )
}
