import type { GraphNode } from "@/lib/projectState"
import type { Shot, ProjectAsset } from "@/features/workflow/api"
import { previewImageAssetIdByShotId } from "@/features/workflow/storyboardAssets"

// 成品预览模式：READER（图文翻页）或 MUSIC（专辑歌词）。
export type PreviewMode = "reader" | "music"

// 音乐/歌曲工作流命中词：节点 type/label 或工作流名含这些词 → 判为 music 模式。
const MUSIC_HINT = /词|歌|曲|音乐|song|lyric/i

// 从自定义 LLM 节点产物解析出的成品文档。lyrics（音乐）/story（故事）二选一或都缺。
export interface StoryDoc {
  title?: string
  lyrics?: string
  story?: string
  mood?: string
  coverPrompt?: string
}

// 成品预览一页：一个分镜镜头 = 配图（可缺）+ 文案（shot.action）。
export interface PreviewPage {
  shotId: string
  imageAssetId?: string
  text: string
}

// 判定成品预览模式：任一节点 type/label 或工作流名命中音乐词 → "music"，否则 "reader"。
//   启发式，用户可在头部手动切换覆盖。
export function classifyPreviewMode(nodes: GraphNode[], workflowName?: string): PreviewMode {
  if (workflowName && MUSIC_HINT.test(workflowName)) return "music"
  for (const n of nodes) {
    if (MUSIC_HINT.test(n.type) || MUSIC_HINT.test(n.label)) return "music"
  }
  return "reader"
}

// 从 custom:* LLM 节点提取成品文档：JSON.parse(output)，容错抽字段。
//   output 是一个 JSON 字符串：音乐 {title,lyrics,mood,coverPrompt}；
//   故事 {title,story,moral,coverPrompt}。解析失败 / 无 custom 节点 → 返回 null。
export function extractStoryDoc(nodes: GraphNode[]): StoryDoc | null {
  const node = nodes.find((n) => n.type.startsWith("custom:") && n.output)
  if (!node?.output) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(node.output)
  } catch {
    return null
  }
  if (typeof parsed !== "object" || parsed == null) return null
  const o = parsed as Record<string, unknown>
  const str = (v: unknown): string | undefined => (typeof v === "string" && v ? v : undefined)
  return {
    title: str(o.title),
    lyrics: str(o.lyrics),
    story: str(o.story),
    mood: str(o.mood),
    coverPrompt: str(o.coverPrompt),
  }
}

// 组装成品预览页：按分镜顺序 join 配图（宽松状态，按 shotId），文案取 shot.action。
export function pairPages(shots: Shot[], assets: ProjectAsset[]): PreviewPage[] {
  const imageByShot = previewImageAssetIdByShotId(assets)
  return shots.map((shot) => {
    const shotId = shot.id ?? ""
    return {
      shotId,
      imageAssetId: shotId ? imageByShot[shotId] : undefined,
      text: shot.action ?? "",
    }
  })
}
