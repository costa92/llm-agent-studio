import { useCallback, useState } from "react"
import { cn } from "@/lib/utils"
import { useResolvedAssetUrl } from "./assetThumb"

// 资产缩略图（计划 Task 10 Step 4 / §已知缺口 1）：
//   走 GET /api/assets/{id}/content（apiFetch 注入 Bearer，redirect:"follow" 跟 302 到签名 URL），
//   下载字节生成 blob object URL 塞 <img src>。effect 清理时 revokeObjectURL 释放。
//   签名过期/加载失败（img onError）→ 重拉一次刷新（UI-spec §11 默认决策 3）。
export interface AssetThumbProps {
  assetId: string
  alt?: string
  className?: string
}

export function AssetThumb({ assetId, alt = "", className }: AssetThumbProps) {
  // 重拉/降级态：reloadKey 自增触发重拉（签名过期刷新，只一次）；imgFailed 标记最终降级。
  //   assetId 变化时用 React 官方 "adjusting state on prop change" 模式（渲染期 setState，
  //   非 effect 内、非 ref 变更）复位，避免 lint 告警。
  const [reloadKey, setReloadKey] = useState(0)
  const [, setRefreshed] = useState(false)
  const [imgFailed, setImgFailed] = useState(false)
  const [trackedAssetId, setTrackedAssetId] = useState(assetId)
  if (trackedAssetId !== assetId) {
    setTrackedAssetId(assetId)
    setReloadKey(0)
    setRefreshed(false)
    setImgFailed(false)
  }

  const { url, loading } = useResolvedAssetUrl(assetId, reloadKey, "image")
  // 解析失败（url == null 且非加载中）或 img 加载失败 → 降级占位。
  const failed = imgFailed || (!loading && url == null)

  const onError = useCallback(() => {
    // 签名过期 → 重拉一次刷新；再失败则标记降级。
    setRefreshed((prev) => {
      if (prev) {
        setImgFailed(true)
      } else {
        setReloadKey((k) => k + 1)
      }
      return true
    })
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
