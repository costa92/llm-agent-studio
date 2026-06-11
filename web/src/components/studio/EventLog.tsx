import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

// 原型 .log-line: font 11px mono; text-3; padding 3px 0; border-bottom 1px dashed #23272e
// log-line em: text-2 不斜体（强调片段）
export interface LogLine {
  // 去重/key 用
  seq: number
  // 主文本
  text: ReactNode
  // 强调片段（原型 <em>，渲染为 text-2 非斜体）
  emphasis?: ReactNode
}

export interface EventLogProps {
  lines: LogLine[]
  className?: string
  emptyText?: ReactNode
}

export function EventLog({ lines, className, emptyText = "暂无事件" }: EventLogProps) {
  if (lines.length === 0) {
    return <div className={cn("py-1 text-[11px] text-text-3", className)}>{emptyText}</div>
  }
  return (
    <div className={className}>
      {lines.map((line) => (
        <div
          key={line.seq}
          className="border-b border-dashed border-[#23272e] py-[3px] font-mono text-[11px] text-text-3"
        >
          {line.emphasis != null && (
            <em className="not-italic text-text-2">{line.emphasis} </em>
          )}
          {line.text}
        </div>
      ))}
    </div>
  )
}
