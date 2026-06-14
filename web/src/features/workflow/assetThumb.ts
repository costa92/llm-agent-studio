import { useEffect, useState } from "react"
import { getAccessToken } from "@/lib/apiClient"

// 缩略图来源（计划 Task 10 Step 4 / §已知缺口 1）：
//   Asset 无 signedUrl 字段。可显示图一律走 GET /api/assets/{id}/content。
//   该端点在 auth middleware 后（asset(roleViewer)，需 Authorization: Bearer），
//   此处为避免跨域重定向时的 CORS / 预检问题（浏览器在 302 重定向到 GitHub raw 时会因
//   Authorization 自定义头部而触发 Options 预检导致失败），通过 query parameter 传递 token
//   并采用原生 fetch（无自定义 Header），从而能以 Simple Request 安全且无痛地跟随 302 重定向。
export async function resolveAssetUrl(assetId: string, type?: string): Promise<string | null> {
  const token = getAccessToken()
  const url = `/api/assets/${assetId}/content` + (token ? `?token=${encodeURIComponent(token)}` : "")
  const res = await fetch(url, {
    redirect: "follow",
  })
  if (!res.ok) return null
  let blob = await res.blob()
  if (blob.type === "text/plain" || blob.type === "application/octet-stream" || !blob.type) {
    let targetMime = "image/png"
    if (type === "video") {
      targetMime = "video/mp4"
    } else if (type === "audio") {
      targetMime = "audio/mpeg"
    }
    blob = new Blob([blob], { type: targetMime })
  }
  return URL.createObjectURL(blob)
}

// 解析资产内容为 blob object URL 的共享 hook（图片缩略图 + 视频/音频共用）：
//   走 resolveAssetUrl（authed fetch + redirect:"follow"），卸载/重拉/换 reloadKey 时 revoke。
//   reloadKey 自增可触发重新解析（签名过期刷新）。返回 { url, loading }；url == null 即失败/未就绪。
export function useResolvedAssetUrl(
  assetId: string,
  reloadKey = 0,
  type?: string,
): { url: string | null; loading: boolean } {
  // resolved 标识"当前 (assetId,reloadKey) 已完成解析"——只在 await 之后 setState
  //   （避免 effect 体内同步 setState 触发级联渲染）。loading = 尚未完成解析。
  const [resolved, setResolved] = useState<{ key: string; url: string | null } | null>(null)

  useEffect(() => {
    let cancelled = false
    let objectUrl: string | null = null
    const key = `${assetId}::${reloadKey}`
    void (async () => {
      const next = await resolveAssetUrl(assetId, type)
      if (cancelled) {
        // 已卸载/重拉：当前结果作废，立即释放避免泄漏。
        if (next) URL.revokeObjectURL(next)
        return
      }
      objectUrl = next
      setResolved({ key, url: next })
    })()
    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [assetId, reloadKey, type])

  const key = `${assetId}::${reloadKey}`
  const loading = resolved?.key !== key
  return { url: loading ? null : (resolved?.url ?? null), loading }
}
