import type { Shot, ProjectAsset } from "./api"
import type { PicturePage } from "./PictureBookReader"

// 把分镜（按 ordering 已排序）+ 项目资产组装成绘本页序列。
//   - 旁白取该 shot 的 action；插图/音频取 shotId 配对的 accepted image/audio asset。
//   - 封面/结尾判定：按头尾位置——首 shot=cover，末 shot=ending，其余=content。
//     （封面/wordless 页 action 为空、无 audio，与后端 fan-out 一致。）
//   - prompt/provider/model 取该页 image asset。
//   shots 为空时返回空数组（调用方据此不渲染阅读器）。
export interface AssemblePagesArgs {
  shots: Shot[]
  assets: ProjectAsset[]
  title?: string
}

// 按 shotId 归集某类型的 accepted 资产：同一页同类型可能多版本，取最新（version 最大）。
//   accepted-only、忽略其它类型；同版本保留先到者（与原 assemblePages 语义一致）。
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
//   分镜栅格据此把插图叠加到对应 shot 卡片，与 assemblePages 的插图配对逻辑同源。
export function imageAssetIdByShotId(assets: ProjectAsset[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [shotId, asset] of latestAcceptedByShot(assets, "image")) {
    out[shotId] = asset.id
  }
  return out
}

export function assemblePages({ shots, assets, title }: AssemblePagesArgs): PicturePage[] {
  if (shots.length === 0) return []

  const imageByShot = latestAcceptedByShot(assets, "image")
  const audioByShot = latestAcceptedByShot(assets, "audio")

  const last = shots.length - 1
  return shots.map((shot, i) => {
    const shotId = shot.id ?? ""
    const image = imageByShot.get(shotId)
    const audio = audioByShot.get(shotId)
    const kind: PicturePage["kind"] =
      i === 0 ? "cover" : i === last ? "ending" : "content"
    return {
      kind,
      // 封面/结尾标题用项目/脚本标题；内容页无独立标题。
      title: kind === "content" ? undefined : title,
      illustrationAssetId: image?.id,
      audioAssetId: audio?.id,
      narration: shot.action || undefined,
      prompt: image?.prompt,
      provider: image?.provider,
      model: image?.model,
    }
  })
}

// 成书阈值：accepted image 数 ≥ 内容页数的一半（向上取整），且至少 1 张。
//   内容页 = 去掉首尾封面/结尾后的 shots 数（少于 3 页时按 shots 总数兜底）。
export function isBookReady(shots: Shot[], assets: ProjectAsset[]): boolean {
  if (shots.length === 0) return false
  const doneImages = assets.filter((a) => a.type === "image" && a.status === "accepted").length
  const contentCount = shots.length >= 3 ? shots.length - 2 : shots.length
  if (contentCount <= 0) return false
  return doneImages >= 1 && doneImages >= Math.ceil(contentCount / 2)
}
