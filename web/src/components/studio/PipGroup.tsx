import { cva } from "class-variance-authority"
import { cn } from "@/lib/utils"
import type { Pip, PipStatus } from "@/lib/timeline"

// 原型 .pip：14×14 radius4 1.5px 边框。
//   done=asset 琥珀实心 / running=琥珀斜条纹 / failed=danger 半透 / idle=line 空框。
const pipVariants = cva("h-3.5 w-3.5 rounded-[4px] border-[1.5px] transition-[0.2s]", {
  variants: {
    status: {
      idle: "border-line",
      running:
        "border-amber bg-[repeating-linear-gradient(115deg,rgba(232,163,61,.7)_0_4px,transparent_4px_8px)]",
      done: "border-asset bg-asset",
      failed: "border-danger bg-danger/30",
    } satisfies Record<PipStatus, string>,
  },
  defaultVariants: { status: "idle" },
})

const PIP_LABEL: Record<PipStatus, string> = {
  idle: "等待",
  running: "生成中",
  done: "已完成",
  failed: "失败",
}

// S4 并行 asset 节点组（每 shot 一个 pip）。done/N 计数由调用方在 stage-sub 展示。
export interface PipGroupProps {
  pips: Pip[]
  className?: string
}

export function PipGroup({ pips, className }: PipGroupProps) {
  return (
    <div
      role="list"
      aria-label="素材生成进度"
      className={cn("mt-2.5 flex flex-wrap gap-[7px]", className)}
    >
      {pips.map((pip) => (
        <span
          key={pip.todoId}
          role="listitem"
          data-slot="pip"
          data-status={pip.status}
          title={`${pip.todoId} · ${PIP_LABEL[pip.status]}`}
          className={pipVariants({ status: pip.status })}
        />
      ))}
    </div>
  )
}
