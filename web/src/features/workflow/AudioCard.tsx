import { useCallback, useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import { useResolvedAssetUrl } from "./assetThumb"

// 网格卡片内的懒加载音频试听器（音频网格可播放）：
//   与 AssetThumb 共用 useResolvedAssetUrl（authed fetch + redirect:"follow" → blob object URL）。
//   懒加载：资产库网格禁止逐个预下载 MP3——初始只渲染「试听」按钮，点击后才挂载内层
//   播放器（hook 只能无条件调用，故置于仅在 armed 时挂载的内层组件）。
//   签名过期（audio onError）→ 重拉一次刷新（同 AssetThumb §11 默认决策 3）。
export interface AudioCardProps {
  assetId: string
  className?: string
}

export function AudioCard({ assetId, className }: AudioCardProps) {
  // armed=false：未点「试听」，不拉字节（网格懒加载）。点后挂载内层播放器。
  const [armed, setArmed] = useState(false)
  // 重拉/降级态：reloadKey 自增触发内层重解析（签名过期刷新，只一次）。
  const [reloadKey, setReloadKey] = useState(0)
  const [, setRefreshed] = useState(false)

  const onError = useCallback(() => {
    // 签名过期 → 重拉一次刷新；再失败则不再重拉（内层显"音频不可用"）。
    setRefreshed((prev) => {
      if (!prev) setReloadKey((k) => k + 1)
      return true
    })
  }, [])

  if (!armed) {
    return (
      <div
        className={cn(
          "flex aspect-square w-full flex-col items-center justify-center gap-2 bg-bg-raised",
          className,
        )}
      >
        <span className="rounded bg-bg-base px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-text-3">
          音频
        </span>
        <button
          type="button"
          onClick={(e) => {
            // 卡片外层是可点击区域（打开抽屉）；试听按钮阻止冒泡，避免既播又开抽屉。
            e.stopPropagation()
            setArmed(true)
          }}
          className="rounded-full border border-line px-3 py-1 text-[11px] text-text-2 transition-colors hover:border-text-3 hover:text-text-1"
        >
          试听
        </button>
      </div>
    )
  }

  return (
    <AudioCardPlayer
      assetId={assetId}
      reloadKey={reloadKey}
      onError={onError}
      className={className}
    />
  )
}

function AudioCardPlayer({
  assetId,
  reloadKey,
  onError,
  className,
}: {
  assetId: string
  reloadKey: number
  onError: () => void
  className?: string
}) {
  const { url, loading } = useResolvedAssetUrl(assetId, reloadKey, "audio")
  const audioRef = useRef<HTMLAudioElement>(null)

  // 解析成功后自动播放（用户已点「试听」，此即其显式意图）；play() 被拒静默忽略。
  useEffect(() => {
    if (url == null) return
    const el = audioRef.current
    if (el == null) return
    void Promise.resolve(el.play()).catch(() => {})
  }, [url])

  if (loading || url == null) {
    return (
      <div
        className={cn(
          "grid aspect-square w-full place-items-center bg-bg-raised text-[10px] text-text-3",
          className,
        )}
      >
        {loading ? "音频加载中…" : "音频不可用"}
      </div>
    )
  }

  return (
    <div
      className={cn(
        "flex aspect-square w-full flex-col items-center justify-center gap-2 bg-bg-raised p-3",
        className,
      )}
    >
      <span className="rounded bg-bg-base px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-text-3">
        音频
      </span>
      <audio
        ref={audioRef}
        controls
        src={url}
        onError={onError}
        // 原生控件点击不应冒泡到卡片外层（避免播放/拖动时误开抽屉）。
        onClick={(e) => e.stopPropagation()}
        className="w-full"
      />
    </div>
  )
}
