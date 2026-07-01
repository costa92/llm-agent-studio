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

// 成品预览（成品预览层）用的宽松状态集：运行态 fan-out 资产多为 pending_acceptance
//   （尚未走审核 accept），accepted-only 的映射会漏掉它们。此处放宽到
//   pending_acceptance / done / accepted，让「成品预览」在审核前也能看到已生成插图。
//   仅供预览层使用，不改动 accepted-only 版本以免破坏分镜栅格/成书判定的既有语义。
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
