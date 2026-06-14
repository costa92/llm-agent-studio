import { cn } from "@/lib/utils"
import { useResolvedAssetUrl } from "./assetThumb"

// 非图片资产（video/audio）的可播放渲染（Phase 3 T4）：
//   与 AssetThumb 共用 useResolvedAssetUrl（authed fetch + redirect:"follow" → blob object URL），
//   按 type 渲染 <video controls> / <audio controls>。未就绪/失败 → 类型占位。
//   图片仍走 AssetThumb，本组件只接 video/audio。
export interface AssetMediaProps {
  assetId: string
  type: string
  className?: string
}

export function AssetMedia({ assetId, type, className }: AssetMediaProps) {
  const { url, loading } = useResolvedAssetUrl(assetId, 0, type)

  if (loading || url == null) {
    return (
      <div
        className={cn(
          "grid place-items-center gap-1 rounded-[10px] border border-line bg-bg-raised text-[10px] text-text-3",
          className,
        )}
      >
        <span className="rounded bg-bg-base px-1.5 py-0.5 font-mono uppercase tracking-wider">
          {type}
        </span>
        {!loading && url == null ? "资源不可用" : "加载中…"}
      </div>
    )
  }

  if (type === "audio") {
    return (
      <div
        className={cn(
          "flex flex-col items-center justify-center gap-2 rounded-[10px] border border-line bg-bg-raised p-4",
          className,
        )}
      >
        <span className="rounded bg-bg-base px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-text-3">
          {type}
        </span>
        <audio controls src={url} className="w-full" />
      </div>
    )
  }

  // video（及其他可视媒体兜底）。
  return (
    <video
      controls
      src={url}
      className={cn("rounded-[10px] border border-line bg-black object-contain", className)}
    />
  )
}
