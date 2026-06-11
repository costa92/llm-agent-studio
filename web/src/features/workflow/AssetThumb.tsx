import { useCallback, useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import { resolveAssetUrl } from "./assetThumb"

// 资产缩略图（计划 Task 10 Step 4 / §已知缺口 1）：
//   走 GET /api/assets/{id}/content（302→签名 URL），apiFetch + redirect:"manual" 读 Location，
//   把签名 URL 塞 <img src>。签名过期（img onError）→ 重拉一次刷新（UI-spec §11 默认决策 3）。
export interface AssetThumbProps {
  assetId: string
  alt?: string
  className?: string
}

export function AssetThumb({ assetId, alt = "", className }: AssetThumbProps) {
  const [url, setUrl] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  // reloadKey 自增触发重拉（签名过期刷新）。
  const [reloadKey, setReloadKey] = useState(0)
  // 防止过期重拉死循环：只刷新一次。
  const refreshedRef = useRef(false)

  useEffect(() => {
    let cancelled = false
    // 异步取签名 URL（setState 在 await 之后，非 effect 体内同步）。
    void (async () => {
      const next = await resolveAssetUrl(assetId)
      if (cancelled) return
      setUrl(next)
      setFailed(next == null)
    })()
    return () => {
      cancelled = true
    }
  }, [assetId, reloadKey])

  // assetId 变更时复位"已刷新"标记。
  useEffect(() => {
    refreshedRef.current = false
  }, [assetId])

  const onError = useCallback(() => {
    // 签名过期 → 重拉一次刷新；再失败则标记降级。
    if (refreshedRef.current) {
      setFailed(true)
      return
    }
    refreshedRef.current = true
    setReloadKey((k) => k + 1)
  }, [])

  if (failed || url == null) {
    return (
      <div
        className={cn(
          "grid place-items-center rounded-[10px] border border-line bg-bg-raised text-[10px] text-text-3",
          className,
        )}
      >
        {failed ? "图片不可用" : "加载中…"}
      </div>
    )
  }

  return (
    <img
      src={url}
      alt={alt}
      onError={onError}
      className={cn("rounded-[10px] border border-line object-cover", className)}
    />
  )
}
