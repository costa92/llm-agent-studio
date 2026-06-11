import { useEffect, useState } from "react"
import { apiFetch } from "@/lib/apiClient"

// 缩略图来源（计划 Task 10 Step 4 决策 / §已知缺口 1）：
//   Asset 无 signedUrl 字段。可显示图一律走 GET /api/assets/{id}/content。
//   该端点在 auth middleware 后（asset(roleViewer)，需 Authorization: Bearer），
//   命中后 302 重定向到 HMAC 签名的 blob URL（/api/blob/{key}?sig=...，无需 auth）。
//
//   旧实现用 redirect:"manual" 读 302 的 Location —— 这在真实浏览器里是错的：
//   按 WHATWG Fetch，manual 重定向返回 opaque-redirect 过滤响应，header 列表为空，
//   res.headers.get("Location") 恒为 null，于是缩略图全部降级为占位。
//
//   现改用 redirect:"follow"：apiFetch 注入内存 Bearer（withAuth），fetch 自动跟随 302
//   到签名 URL 并下载字节（仅一次、已鉴权），再用 blob 生成 object URL 塞 <img src>。
//   调用方负责在卸载/重拉时 revokeObjectURL（见 AssetThumb 的 effect 清理）。
//   签名过期或加载失败（img onError）→ 重拉一次刷新（UI-spec §11 默认决策 3）。
export async function resolveAssetUrl(assetId: string): Promise<string | null> {
  const res = await apiFetch(`/api/assets/${assetId}/content`, {
    redirect: "follow",
  })
  if (!res.ok) return null
  const blob = await res.blob()
  return URL.createObjectURL(blob)
}

// 解析资产内容为 blob object URL 的共享 hook（图片缩略图 + 视频/音频共用）：
//   走 resolveAssetUrl（authed fetch + redirect:"follow"），卸载/重拉/换 reloadKey 时 revoke。
//   reloadKey 自增可触发重新解析（签名过期刷新）。返回 { url, loading }；url == null 即失败/未就绪。
export function useResolvedAssetUrl(
  assetId: string,
  reloadKey = 0,
): { url: string | null; loading: boolean } {
  // resolved 标识"当前 (assetId,reloadKey) 已完成解析"——只在 await 之后 setState
  //   （避免 effect 体内同步 setState 触发级联渲染）。loading = 尚未完成解析。
  const [resolved, setResolved] = useState<{ key: string; url: string | null } | null>(null)

  useEffect(() => {
    let cancelled = false
    let objectUrl: string | null = null
    const key = `${assetId}::${reloadKey}`
    void (async () => {
      const next = await resolveAssetUrl(assetId)
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
  }, [assetId, reloadKey])

  const key = `${assetId}::${reloadKey}`
  const loading = resolved?.key !== key
  return { url: loading ? null : (resolved?.url ?? null), loading }
}
