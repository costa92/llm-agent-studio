import { cn } from "@/lib/utils"
import { SelectedAssetPanel } from "@/features/workflow/SelectedAssetPanel"
import { AssetAudio } from "@/features/workflow/AssetAudio"
import { STATUS_VAR, STATUS_LABEL } from "./statusColor"
import type { GroupCell, RunGroup } from "./runFanout"
import type { AssetDetail } from "@/lib/types"
import type { GraphNodeStatus } from "@/lib/projectState"

// 运行态右栏 Run Matrix（选中大功能容器时）：逐页状态格矩阵 + 图例 + 选中产物。
//   矩阵：每页一格按状态着色，点选 → 下方渲该页产物（图→SelectedAssetPanel，音→AssetAudio 播放器）。
//   这取代了 storyboard 节点旧的 ItemInspector（OQ-1：Run Matrix 全面接管逐页产物视图）。

const LEGEND: GraphNodeStatus[] = ["done", "running", "failed", "pending"]

export function RunMatrix({
  group,
  selectedTodoId,
  onSelectCell,
  org,
  isAdmin,
  assetDetail,
}: {
  group?: RunGroup
  selectedTodoId?: string
  onSelectCell: (cell: GroupCell) => void
  org: string
  isAdmin: boolean
  // 选中页若为 image：其 useAsset 详情（容器拉取），传给 SelectedAssetPanel。
  assetDetail?: AssetDetail
}) {
  if (!group || group.cells.length === 0) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-1.5 py-16 text-center">
        <p className="text-[13px] text-text-2">该分镜暂无逐页产物</p>
        <p className="text-[12px] text-text-3">分镜扇出逐页图/音后在此查看</p>
      </div>
    )
  }
  const { cells, counts } = group
  const selected = cells.find((c) => c.todoId === selectedTodoId)

  return (
    <div className="flex flex-col gap-3">
      {/* 汇总。 */}
      <p data-slot="run-matrix-summary" className="text-[12px] text-text-2">
        {counts.done}/{counts.total} 完成
        {counts.running > 0 && (
          <>
            {" · "}
            <span className="text-amber">{counts.running} 运行中</span>
          </>
        )}
        {counts.failed > 0 && (
          <>
            {" · "}
            <span className="text-danger">{counts.failed} 失败</span>
          </>
        )}
      </p>

      {/* 图例。 */}
      <div data-slot="run-matrix-legend" className="flex flex-wrap gap-x-3 gap-y-1">
        {LEGEND.map((s) => (
          <span key={s} className="flex items-center gap-1 text-[10px] text-text-3">
            <span
              aria-hidden
              className="h-2.5 w-2.5 rounded-sm"
              style={{ background: STATUS_VAR[s] }}
            />
            {STATUS_LABEL[s]}
          </span>
        ))}
      </div>

      {/* 状态格矩阵。 */}
      <div data-slot="run-matrix" className="grid grid-cols-6 gap-1">
        {cells.map((c) => {
          const dim = c.status === "pending" || c.status === "blocked"
          return (
            <button
              key={c.todoId}
              type="button"
              data-slot="run-matrix-cell"
              data-status={c.status}
              title={`第${c.pageOrdinal}页 · ${STATUS_LABEL[c.status]}`}
              onClick={() => onSelectCell(c)}
              className={cn(
                "grid aspect-square place-items-center rounded text-[9px] font-medium",
                c.todoId === selectedTodoId && "ring-2 ring-amber ring-offset-1 ring-offset-bg-surface",
                dim ? "text-text-2" : "text-[var(--bg-base)]",
              )}
              style={{ background: STATUS_VAR[c.status] }}
            >
              {c.pageOrdinal}
            </button>
          )
        })}
      </div>

      {/* 选中产物。 */}
      <div data-slot="run-matrix-artifact" className="border-t border-line pt-3">
        {selected && selected.assetId && selected.kind === "image" ? (
          <SelectedAssetPanel
            org={org}
            assetId={selected.assetId}
            isAdmin={isAdmin}
            detail={assetDetail}
          />
        ) : selected && selected.assetId && selected.kind === "audio" ? (
          <div className="flex flex-col gap-2">
            <p className="text-[11px] text-text-3">第{selected.pageOrdinal}页 · 配音</p>
            <AssetAudio assetId={selected.assetId} />
          </div>
        ) : selected ? (
          <p className="text-[12px] text-text-3">
            第{selected.pageOrdinal}页 · {STATUS_LABEL[selected.status]}（暂无产物）
          </p>
        ) : (
          <p className="text-[12px] text-text-3">选择一格查看该页产物</p>
        )}
      </div>
    </div>
  )
}
