import type { ProjectAsset } from "./api"

// 按 shotId 归集某类型的 accepted 资产：同一页同类型可能多版本，取最新（version 最大）。
//   accepted-only、忽略其它类型；同版本保留先到者。
function latestAcceptedByShot(assets: ProjectAsset[], type: string): Map<string, ProjectAsset> {
  const byShot = new Map<string, ProjectAsset>()
  for (const a of assets) {
    if (a.status !== "accepted" || !a.shotId || a.type !== type) continue
    const prev = byShot.get(a.shotId)
    if (!prev || (a.version ?? 0) > (prev.version ?? 0)) byShot.set(a.shotId, a)
  }
  return byShot
}

// shotId → 该页最新 accepted image 资产 id 的映射（accepted-only，最新 version 胜，忽略 audio）。
//   分镜栅格据此把插图叠加到对应 shot 卡片。
export function imageAssetIdByShotId(assets: ProjectAsset[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [shotId, asset] of latestAcceptedByShot(assets, "image")) {
    out[shotId] = asset.id
  }
  return out
}
