import { forwardRef } from "react"
import { cn } from "@/lib/utils"
import { useResolvedAssetUrl } from "./assetThumb"

// 通用音频播放器（成品阅读旁白/TTS 朗读）：复用 useResolvedAssetUrl 扩 "audio"，
//   走 GET /api/assets/{id}/content 下载字节生成 blob object URL 喂 <audio controls src>。
//   加载/失败降级文案。ref 透传给 <audio>，供阅读器自动朗读时 play()/监听 onEnded。
export interface AssetAudioProps {
  assetId: string
  className?: string
  // 自动朗读：该页 audio 播放结束回调（阅读器据此翻到下一页）。
  onEnded?: () => void
}

export const AssetAudio = forwardRef<HTMLAudioElement, AssetAudioProps>(
  function AssetAudio({ assetId, className, onEnded }, ref) {
    const { url, loading } = useResolvedAssetUrl(assetId, 0, "audio")

    if (url == null) {
      return (
        <div
          className={cn(
            "grid place-items-center rounded-[10px] border border-line bg-bg-raised px-3 py-2 text-[11px] text-text-3",
            className,
          )}
        >
          {loading ? "音频加载中…" : "音频不可用"}
        </div>
      )
    }

    return (
      <audio
        ref={ref}
        src={url}
        controls
        onEnded={onEnded}
        className={cn("w-full", className)}
      />
    )
  },
)
