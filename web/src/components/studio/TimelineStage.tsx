import type { ReactNode } from "react"
import { cva } from "class-variance-authority"
import { cn } from "@/lib/utils"
import type { StageId } from "@/lib/timeline"
import type { StageRole, StageStatus2 } from "@/lib/projectState"

// 制片轨道单阶段的表现形状（容器从权威 ProjectState 的 StageState 适配而来）。
// id = S1-S5（着色定位）；kind = 语义 role（agent 色/标签）；status = 5 态；
// linked = 连接线着色（done 时着 agent 色）。
export interface Stage {
  id: StageId
  kind: StageRole
  status: StageStatus2
  todoId?: string
  linked: boolean
}

// 原型 .stage / .node / .tchip：制片轨道单阶段。
//   node：28px 圆 2px 边框；done=填 agent 色 / running=琥珀边 + 虚线旋转环 / failed=danger。
//   连接线 .stage::before 在 linked 时着 --cur（agent 色）。
//   tchip：阶段态小药丸（done/run/pending/blocked/failed）。

// 每个 stage 的 agent 语义色（CSS 变量名）+ 展示标签。
const STAGE_META: Record<
  Stage["kind"],
  { color: string; sn: string; name: string }
> = {
  planner: { color: "var(--amber)", sn: "S1", name: "Planner 规划" },
  script: { color: "var(--script)", sn: "S2", name: "剧本生成" },
  storyboard: { color: "var(--board)", sn: "S3", name: "分镜拆解" },
  asset: { color: "var(--asset)", sn: "S4", name: "素材生成" },
  review: { color: "var(--review)", sn: "S5", name: "人工审核" },
}

const tchipVariants = cva(
  "rounded-full px-2 py-px text-[10.5px] font-medium",
  {
    variants: {
      status: {
        blocked: "border border-dashed border-line text-text-3",
        pending: "bg-bg-raised text-text-3",
        running:
          "text-primary-foreground bg-[repeating-linear-gradient(115deg,var(--amber)_0_8px,color-mix(in_srgb,var(--amber)_72%,#000)_8px_16px)]",
        done: "bg-review/13 text-review",
        failed: "bg-danger/13 text-danger",
      } satisfies Record<StageStatus2, string>,
    },
    defaultVariants: { status: "blocked" },
  },
)

const TCHIP_LABEL: Record<StageStatus2, string> = {
  blocked: "blocked",
  pending: "pending",
  running: "running",
  done: "done",
  failed: "failed",
}

export interface TimelineStageProps {
  stage: Stage
  // 副标题（agent 名 / todoId / done/N 计数等，调用方组装）。
  sub?: ReactNode
  // S4 的 pip 组等附加内容。
  children?: ReactNode
  // 是否末阶段（不画下连接线）。
  last?: boolean
  // T3：可检视阶段（S2/S3）传入回调 → 标题行渲染为按钮（hover/cursor/键盘可达），点击打开抽屉。
  onSelect?: () => void
  className?: string
}

export function TimelineStage({
  stage,
  sub,
  children,
  last = false,
  onSelect,
  className,
}: TimelineStageProps) {
  const meta = STAGE_META[stage.kind]
  const isDone = stage.status === "done"
  const isRunning = stage.status === "running"
  const isFailed = stage.status === "failed"

  return (
    <div
      data-slot="stage"
      data-status={stage.status}
      className={cn("relative flex gap-4 pb-[30px]", className)}
      style={{ ["--cur" as string]: meta.color }}
    >
      {/* 连接线（linked 时着 agent 色；pending/unlinked 时虚线灰）。 */}
      {!last && (
        <span
          aria-hidden
          data-slot="connector"
          data-linked={stage.linked ? "true" : "false"}
          className={cn(
            "absolute left-[21px] top-[30px] bottom-0 w-0.5",
            stage.linked
              ? "bg-[var(--cur)]"
              : "border-l border-dashed border-line bg-transparent",
          )}
        />
      )}
      {/* 节点。 */}
      <div
        className={cn(
          "relative z-[1] ml-[7px] grid h-7 w-7 flex-shrink-0 place-items-center rounded-full border-2 bg-bg-base transition-[0.2s]",
          isDone && "border-[var(--cur)] bg-[var(--cur)]",
          isRunning && "border-amber",
          isFailed && "border-danger bg-danger/15",
          !isDone && !isRunning && !isFailed && "border-line",
        )}
      >
        {isRunning && (
          <span
            aria-hidden
            className="absolute -inset-1.5 rounded-full border-2 border-dashed border-amber motion-safe:animate-[spin_3s_linear_infinite]"
          />
        )}
        <span
          className={cn(
            "font-sans text-[10px] font-bold",
            isDone ? "text-bg-base" : isFailed ? "text-danger" : "text-text-3",
          )}
        >
          {isDone ? "✓" : meta.sn}
        </span>
      </div>
      {/* 主体。 */}
      <div className="flex-1 pt-0.5">
        {/* 可检视阶段（S2/S3）标题行渲染为按钮：hover/cursor 提示 + 键盘可达。 */}
        {onSelect ? (
          <button
            type="button"
            onClick={onSelect}
            className="-mx-1.5 flex items-center gap-2 rounded-md px-1.5 py-0.5 text-[13.5px] font-semibold text-text-1 transition-colors hover:bg-bg-raised"
          >
            <span className="font-mono text-[10px] font-bold text-text-3">{meta.sn}</span>
            {meta.name}
            <span className={tchipVariants({ status: stage.status })}>
              {TCHIP_LABEL[stage.status]}
            </span>
          </button>
        ) : (
          <div className="flex items-center gap-2 text-[13.5px] font-semibold text-text-1">
            <span className="font-mono text-[10px] font-bold text-text-3">{meta.sn}</span>
            {meta.name}
            <span className={tchipVariants({ status: stage.status })}>
              {TCHIP_LABEL[stage.status]}
            </span>
          </div>
        )}
        {sub != null && (
          <div className="mt-0.5 text-[11.5px] text-text-3">{sub}</div>
        )}
        {children}
      </div>
    </div>
  )
}
