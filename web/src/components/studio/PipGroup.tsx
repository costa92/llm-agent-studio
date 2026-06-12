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
  // T3：已完成（done 且有 assetId）的 pip 可点 → 右栏预览该工件。
  onSelectPip?: (pip: Pip) => void
  className?: string
}

export function PipGroup({ pips, onSelectPip, className }: PipGroupProps) {
  return (
    <div
      role="list"
      aria-label="素材生成进度"
      className={cn("mt-2.5 flex flex-wrap gap-[7px]", className)}
    >
      {pips.map((pip) => {
        const title = `${pip.todoId} · ${PIP_LABEL[pip.status]}`
        // done 且有 assetId → 渲染为按钮（可点预览 / cursor / 键盘可达）；否则惰性方块。
        const selectable = onSelectPip != null && pip.status === "done" && pip.assetId != null
        if (selectable) {
          return (
            <button
              key={pip.todoId}
              type="button"
              aria-label={title}
              onClick={() => onSelectPip(pip)}
              data-slot="pip"
              data-status={pip.status}
              title={title}
              className={cn(pipVariants({ status: pip.status }), "cursor-pointer")}
            />
          )
        }
        return (
          <span
            key={pip.todoId}
            role="listitem"
            data-slot="pip"
            data-status={pip.status}
            title={title}
            className={pipVariants({ status: pip.status })}
          />
        )
      })}
    </div>
  )
}
