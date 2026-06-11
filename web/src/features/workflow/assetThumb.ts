import { apiFetch } from "@/lib/apiClient"

// 缩略图来源（计划 Task 10 Step 4 决策 / §已知缺口 1）：
//   Asset 无 signedUrl 字段。可显示图一律走 GET /api/assets/{id}/content（302→签名 URL）。
//   该端点需 Bearer auth，<img src> 不带 token —— 故 SPA 内用 apiFetch + redirect:"manual"
//   读 302 的 Location 头拿到签名 URL（无需 auth，HMAC 在 query），再塞 <img src>。
//   签名过期（图 onError）则重拉一次刷新（UI-spec §11 默认决策 3）。
//
// 注：fetch redirect:"manual" 下，跨/同源 302 返回一个 opaqueredirect（status 0、无可读 Location）。
//   apiFetch 用同源相对路径（/api/...），浏览器在 manual 模式仍把 Location 暴露在 res.headers。
//   测试通过 mock fetch 返回带 Location 头的 302 来覆盖该路径。
export async function resolveAssetUrl(assetId: string): Promise<string | null> {
  const res = await apiFetch(`/api/assets/${assetId}/content`, {
    redirect: "manual",
  })
  // 302 → 读 Location（签名 URL）。
  const loc = res.headers.get("Location")
  if (loc) return loc
  // opaqueredirect / 其他：退回直接用端点 URL（浏览器会自动跟 302，但带不上 token——保守返回 null）。
  return null
}
