import {
  Handle,
  Position,
  type NodeProps,
  type Node,
} from "@xyflow/react"
import { Image as ImageIcon, Music2, Film } from "lucide-react"
import { cn } from "@/lib/utils"
import { AssetThumb } from "@/features/workflow/AssetThumb"
import type { AssetRunNodeData } from "./runFanout"

// 运行态画布的 asset 扇出子节点（独立组件，不改 WorkflowNode）：
// storyboard 扇出的逐页图/音 todo 各画一张小卡片，带状态点 + 图/音图标 + 缩略图。
// 编辑态零改：状态点保守复刻 WorkflowNode 的 RunStatusDot 同款 className（不抽公共组件）。
export type AssetRunRFNode = Node<AssetRunNodeData, "assetRun">

function kindLabel(kind: AssetRunNodeData["kind"]): string {
  if (kind === "audio") return "配音"
  if (kind === "image") return "配图"
  return "素材"
}

function KindIcon({ kind }: { kind: AssetRunNodeData["kind"] }) {
  const className = "h-3.5 w-3.5 text-text-3"
  if (kind === "audio") return <Music2 aria-hidden className={className} />
  if (kind === "video") return <Film aria-hidden className={className} />
  return <ImageIcon aria-hidden className={className} />
}

export function AssetRunNode({ data }: NodeProps<AssetRunRFNode>) {
  const { status, kind, pageOrdinal, assetId } = data
  const isDone = status === "done"
  const isRunning = status === "running"
  const isFailed = status === "failed"

  return (
    <div
      data-slot="asset-run-node"
      data-status={status}
      className={cn(
        "flex w-[120px] cursor-pointer flex-col gap-1.5 rounded-lg border bg-bg-surface px-2 py-1.5 shadow-sm",
        isFailed ? "border-danger" : "border-line",
      )}
    >
      <Handle type="target" position={Position.Top} />
      {/* 顶行：状态点 + 图/音图标 + 页序标签。 */}
      <div className="flex items-center gap-1.5">
        <AssetStatusDot status={status} />
        <KindIcon kind={kind} />
        <span className="truncate text-[11px] font-medium text-text-1">
          第{pageOrdinal}页·{kindLabel(kind)}
        </span>
      </div>
      {/* 缩略图区：image+done+有assetId → AssetThumb；audio → 音频占位（不下载）；
          running/无assetId → 生成中；failed → 生成失败。 */}
      <div className="overflow-hidden rounded-[8px]">
        {isFailed ? (
          <div className="grid h-16 w-full place-items-center rounded-[8px] border border-danger bg-bg-raised text-[10px] text-danger">
            生成失败
          </div>
        ) : kind === "image" && isDone && assetId ? (
          <AssetThumb assetId={assetId} className="h-16 w-full" />
        ) : kind === "audio" ? (
          <div className="grid h-16 w-full place-items-center rounded-[8px] bg-bg-raised text-text-3">
            <Music2 aria-hidden className="h-5 w-5" />
          </div>
        ) : isRunning || !assetId ? (
          <div className="grid h-16 w-full place-items-center rounded-[8px] bg-bg-raised text-[10px] text-text-3">
            生成中…
          </div>
        ) : (
          // done 但非 image/audio（如 video）或无缩略图 → 通用占位。
          <div className="grid h-16 w-full place-items-center rounded-[8px] bg-bg-raised text-text-3">
            <KindIcon kind={kind} />
          </div>
        )}
      </div>
    </div>
  )
}

// 状态点：保守复刻 WorkflowNode.RunStatusDot 的 className（done 填 cur✓ / running 琥珀虚线转环 /
// failed danger / pending 中性）。此处用 asset token 作 done 填充色，与 MiniMap 守卫一致。
function AssetStatusDot({ status }: { status: AssetRunNodeData["status"] }) {
  const isDone = status === "done"
  const isRunning = status === "running"
  const isFailed = status === "failed"
  return (
    <span
      aria-hidden
      data-slot="asset-run-status"
      className={cn(
        "relative grid h-4 w-4 shrink-0 place-items-center rounded-full border-2 bg-bg-base",
        isDone && "border-[var(--asset)] bg-[var(--asset)]",
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
          "font-sans text-[8px] font-bold leading-none",
          isDone ? "text-bg-base" : isFailed ? "text-danger" : "text-text-3",
        )}
      >
        {isDone ? "✓" : ""}
      </span>
    </span>
  )
}
