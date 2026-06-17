import type { Shot, ProjectAsset } from "./api"
import type { PicturePage } from "./PictureBookReader"

// 把分镜（按 ordering 已排序）+ 项目资产组装成绘本页序列。
//   - 旁白取该 shot 的 action；插图/音频取 shotId 配对的 done image/audio asset。
//   - 封面/结尾判定：按头尾位置——首 shot=cover，末 shot=ending，其余=content。
//     （封面/wordless 页 action 为空、无 audio，与后端 fan-out 一致。）
//   - prompt/provider/model 取该页 image asset。
//   shots 为空时返回空数组（调用方据此不渲染阅读器）。
export interface AssemblePagesArgs {
  shots: Shot[]
  assets: ProjectAsset[]
  title?: string
}

export function assemblePages({ shots, assets, title }: AssemblePagesArgs): PicturePage[] {
  if (shots.length === 0) return []

  // 按 shotId 归集 done 资产：同一页同类型可能多版本，取最新（version 最大）。
  const pickLatest = (a: ProjectAsset, b: ProjectAsset) =>
    (b.version ?? 0) - (a.version ?? 0)
  const imageByShot = new Map<string, ProjectAsset>()
  const audioByShot = new Map<string, ProjectAsset>()
  for (const a of assets) {
    if (a.status !== "done" || !a.shotId) continue
    const map = a.type === "audio" ? audioByShot : a.type === "image" ? imageByShot : null
    if (!map) continue
    const prev = map.get(a.shotId)
    if (!prev || pickLatest(a, prev) < 0) map.set(a.shotId, a)
  }

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

// 成书阈值：done image 数 ≥ 内容页数的一半（向上取整），且至少 1 张。
//   内容页 = 去掉首尾封面/结尾后的 shots 数（少于 3 页时按 shots 总数兜底）。
export function isBookReady(shots: Shot[], assets: ProjectAsset[]): boolean {
  if (shots.length === 0) return false
  const doneImages = assets.filter((a) => a.type === "image" && a.status === "done").length
  const contentCount = shots.length >= 3 ? shots.length - 2 : shots.length
  if (contentCount <= 0) return false
  return doneImages >= 1 && doneImages >= Math.ceil(contentCount / 2)
}
