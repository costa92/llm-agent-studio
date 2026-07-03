import type { ProjectAsset } from "./api"

// 成品预览（成品预览层）用的宽松状态集：运行态 fan-out 资产多为 pending_acceptance
//   （尚未走审核 accept），accepted-only 的映射会漏掉它们。此处放宽到
//   pending_acceptance / done / accepted，让「成品预览」在审核前也能看到已生成插图。
const PREVIEW_IMAGE_STATUSES = new Set(["accepted", "pending_acceptance", "done"])

// 按 shotId 归集最新的可预览 image 资产（放宽状态，最新 version 胜）。
function latestPreviewImageByShot(assets: ProjectAsset[]): Map<string, ProjectAsset> {
  const byShot = new Map<string, ProjectAsset>()
  for (const a of assets) {
    if (a.type !== "image" || !a.shotId || !PREVIEW_IMAGE_STATUSES.has(a.status)) continue
    const prev = byShot.get(a.shotId)
    if (!prev || (a.version ?? 0) > (prev.version ?? 0)) byShot.set(a.shotId, a)
  }
  return byShot
}

// shotId → 最新可预览 image 资产 id（宽松状态版，供 RunPreview 成品预览用）。
export function previewImageAssetIdByShotId(assets: ProjectAsset[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [shotId, asset] of latestPreviewImageByShot(assets)) {
    out[shotId] = asset.id
  }
  return out
}
