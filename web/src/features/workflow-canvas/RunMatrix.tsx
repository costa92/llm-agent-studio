import { cn } from "@/lib/utils"
import { SelectedAssetPanel } from "@/features/workflow/SelectedAssetPanel"
import { AssetAudio } from "@/features/workflow/AssetAudio"
import { STATUS_VAR, STATUS_LABEL } from "./statusColor"
import { pageStatus, type RunPage, type RunGroup } from "./runFanout"
import type { AssetDetail } from "@/lib/types"
import type { GraphNodeStatus } from "@/lib/projectState"

// 运行态右栏 Run Matrix（选中大功能容器时）：逐页状态格矩阵 + 图例 + 选中页产物。
//   矩阵：每页一格按页聚合状态着色，点选 → 下方渲该页产物。
//   图音双渲染：选中页同时渲 配图（SelectedAssetPanel）+ 配音（AssetAudio 播放器）。
//   这取代了 storyboard 节点旧的 ItemInspector（OQ-1：Run Matrix 全面接管逐页产物视图）。

const LEGEND: GraphNodeStatus[] = ["done", "running", "failed", "pending"]

export function RunMatrix({
  group,
  selectedPageKey,
  onSelectPage,
  org,
  isAdmin,
  assetDetail,
}: {
  group?: RunGroup
  selectedPageKey?: string
  onSelectPage: (page: RunPage) => void
  org: string
  isAdmin: boolean
  // 选中页 image 的 useAsset 详情（容器拉取），传给 SelectedAssetPanel。
  assetDetail?: AssetDetail
}) {
  if (!group || group.pages.length === 0) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-1.5 py-16 text-center">
        <p className="text-[13px] text-text-2">该分镜暂无逐页产物</p>
        <p className="text-[12px] text-text-3">分镜扇出逐页图/音后在此查看</p>
      </div>
    )
  }
  const { pages, counts } = group
  const selected = pages.find((p) => p.key === selectedPageKey)

  return (
    <div className="flex flex-col gap-3">
      {/* 汇总（逐资产格计：图/音各算一项）。 */}
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

      {/* 逐页状态格矩阵（每格 = 一页，按页聚合状态着色）。 */}
      <div data-slot="run-matrix" className="grid grid-cols-6 gap-1">
        {pages.map((p) => {
          const st = pageStatus(p)
          const dim = st === "pending"
          return (
            <button
              key={p.key}
              type="button"
              data-slot="run-matrix-cell"
              data-status={st}
              title={`第${p.pageOrdinal}页 · ${STATUS_LABEL[st]}`}
              onClick={() => onSelectPage(p)}
              className={cn(
                "grid aspect-square place-items-center rounded text-[9px] font-medium",
                p.key === selectedPageKey && "ring-2 ring-amber ring-offset-1 ring-offset-bg-surface",
                dim ? "text-text-2" : "text-[var(--bg-base)]",
              )}
              style={{ background: STATUS_VAR[st] }}
            >
              {p.pageOrdinal}
            </button>
          )
        })}
      </div>

      {/* 选中页产物：配图 + 配音双渲染。 */}
      <div data-slot="run-matrix-artifact" className="flex flex-col gap-3 border-t border-line pt-3">
        {selected ? (
          <>
            <p className="text-[11px] text-text-3">第{selected.pageOrdinal}页</p>
            {selected.image?.assetId ? (
              <SelectedAssetPanel
                org={org}
                assetId={selected.image.assetId}
                isAdmin={isAdmin}
                detail={assetDetail}
              />
            ) : (
              <p className="text-[12px] text-text-3">
                配图 · {STATUS_LABEL[selected.image?.status ?? "pending"]}（暂无产物）
              </p>
            )}
            {selected.audio &&
              (selected.audio.assetId ? (
                <div className="flex flex-col gap-1">
                  <p className="text-[11px] text-text-3">配音</p>
                  <AssetAudio assetId={selected.audio.assetId} />
                </div>
              ) : (
                <p className="text-[12px] text-text-3">
                  配音 · {STATUS_LABEL[selected.audio.status]}（暂无产物）
                </p>
              ))}
          </>
        ) : (
          <p className="text-[12px] text-text-3">选择一格查看该页产物</p>
        )}
      </div>
    </div>
  )
}
