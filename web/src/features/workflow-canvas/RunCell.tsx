import { Image as ImageIcon, Music2, Film, Play } from "lucide-react"
import { cn } from "@/lib/utils"
import { AssetThumb } from "@/features/workflow/AssetThumb"
import type { GroupCell } from "./runFanout"

// 大功能容器展开后的逐页子卡（纯展示，非 RF 节点）：状态点 + 图/音图标 + 页序 + 缩略图/波形。
// image+done → AssetThumb；audio+done → 波形 + 播放占位（实际播放在右栏 Run Matrix 的 AssetAudio）；
// running/无 assetId → 生成中；failed → 失败。点击 → onSelect（容器把它路由到 Run Matrix 选中该页）。

function kindLabel(kind: GroupCell["kind"]): string {
  if (kind === "audio") return "配音"
  if (kind === "image") return "配图"
  return "素材"
}

function KindIcon({ kind }: { kind: GroupCell["kind"] }) {
  const className = "h-3 w-3 text-text-3"
  if (kind === "audio") return <Music2 aria-hidden className={className} />
  if (kind === "video") return <Film aria-hidden className={className} />
  return <ImageIcon aria-hidden className={className} />
}

// 静态波形条（确定性高度，无随机——随机会破测试且本环境禁用 Math.random）。
const WAVE_BARS = [5, 11, 7, 14, 9, 13, 6, 12, 8, 10, 6, 13, 7]

function AudioWaveform() {
  return (
    <div
      data-slot="run-cell-audio"
      className="relative grid h-14 w-full place-items-center rounded-[6px] bg-bg-raised"
    >
      <svg
        aria-hidden
        viewBox="0 0 104 16"
        className="h-6 w-[80%]"
        preserveAspectRatio="none"
      >
        {WAVE_BARS.map((h, i) => (
          <rect
            key={i}
            x={i * 8}
            y={(16 - h) / 2}
            width={4}
            height={h}
            rx={1}
            fill="var(--review)"
          />
        ))}
      </svg>
      {/* 播放占位（装饰；真实播放在右栏 Run Matrix）。 */}
      <span className="absolute grid h-5 w-5 place-items-center rounded-full bg-bg-base/80 shadow-sm">
        <Play aria-hidden className="h-3 w-3 text-text-1" />
      </span>
    </div>
  )
}

export function RunCell({
  cell,
  selected,
  onSelect,
}: {
  cell: GroupCell
  selected?: boolean
  onSelect?: () => void
}) {
  const { status, kind, pageOrdinal, assetId } = cell
  const isDone = status === "done"
  const isRunning = status === "running"
  const isFailed = status === "failed"

  return (
    <button
      type="button"
      data-slot="run-cell"
      data-status={status}
      // stopPropagation：容器是 RF 节点，子卡点击不应冒泡触发 onNodeClick 的整体选中。
      onClick={(e) => {
        e.stopPropagation()
        onSelect?.()
      }}
      className={cn(
        "flex cursor-pointer flex-col gap-1 rounded-lg border bg-bg-surface p-1.5 text-left transition-colors",
        selected
          ? "border-amber"
          : isFailed
            ? "border-danger"
            : "border-line hover:border-text-3",
      )}
    >
      <div className="flex items-center gap-1">
        <RunCellDot status={status} />
        <KindIcon kind={kind} />
        <span className="truncate text-[10px] font-medium text-text-1">
          第{pageOrdinal}页·{kindLabel(kind)}
        </span>
      </div>
      <div className="overflow-hidden rounded-[6px]">
        {isFailed ? (
          <div className="grid h-14 w-full place-items-center rounded-[6px] border border-danger bg-bg-raised text-[10px] text-danger">
            {kind === "audio" ? "配音失败" : "生成失败"}
          </div>
        ) : kind === "image" && isDone && assetId ? (
          <AssetThumb assetId={assetId} className="h-14 w-full" />
        ) : kind === "audio" && isDone ? (
          <AudioWaveform />
        ) : isRunning || !assetId ? (
          <div className="grid h-14 w-full place-items-center rounded-[6px] bg-bg-raised text-[10px] text-text-3">
            {kind === "audio" ? "配音中…" : "生成中…"}
          </div>
        ) : (
          // done 但非 image/audio（如 video）或无缩略图 → 通用占位。
          <div className="grid h-14 w-full place-items-center rounded-[6px] bg-bg-raised text-text-3">
            <KindIcon kind={kind} />
          </div>
        )}
      </div>
    </button>
  )
}

// 子卡状态点：done 填 --review（绿，与状态条/Run Matrix 一致，避开 --asset===--amber 撞色）。
function RunCellDot({ status }: { status: GroupCell["status"] }) {
  const isDone = status === "done"
  const isRunning = status === "running"
  const isFailed = status === "failed"
  return (
    <span
      aria-hidden
      data-slot="run-cell-status"
      className={cn(
        "relative grid h-3.5 w-3.5 shrink-0 place-items-center rounded-full border-2 bg-bg-base",
        isDone && "border-[var(--review)] bg-[var(--review)]",
        isRunning && "border-amber",
        isFailed && "border-danger bg-danger/15",
        !isDone && !isRunning && !isFailed && "border-line",
      )}
    >
      {isRunning && (
        <span
          aria-hidden
          className="absolute -inset-1 rounded-full border-2 border-dashed border-amber motion-safe:animate-[spin_3s_linear_infinite]"
        />
      )}
      <span
        className={cn(
          "font-sans text-[7px] font-bold leading-none",
          isDone ? "text-bg-base" : isFailed ? "text-danger" : "text-text-3",
        )}
      >
        {isDone ? "✓" : ""}
      </span>
    </span>
  )
}
