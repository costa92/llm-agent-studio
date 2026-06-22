import { cn } from "@/lib/utils"
import type { CanvasMode } from "./WorkflowCanvas"

// 「编辑 | 运行」段切换。当前段高亮（amber token），点另一段触发 onChange。
export function ModeToggle({
  mode,
  onChange,
}: {
  mode: CanvasMode
  onChange: (next: CanvasMode) => void
}) {
  return (
    <div
      role="group"
      aria-label="画布模式"
      className="inline-flex items-center rounded-md border border-line bg-bg-base p-0.5"
    >
      <Segment label="编辑" active={mode === "edit"} onClick={() => onChange("edit")} />
      <Segment label="运行" active={mode === "run"} onClick={() => onChange("run")} />
    </div>
  )
}

function Segment({
  label,
  active,
  onClick,
}: {
  label: string
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "rounded px-2.5 py-1 text-[12px] font-medium transition-colors",
        active
          ? "bg-amber text-primary-foreground"
          : "text-text-3 hover:text-text-1",
      )}
    >
      {label}
    </button>
  )
}
