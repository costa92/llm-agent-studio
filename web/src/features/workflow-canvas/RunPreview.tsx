import { useCallback, useEffect, useState } from "react"
import { ChevronLeft, ChevronRight, Play } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/studio/Button"
import type { GraphNode } from "@/lib/projectState"
import { useShots, useProjectAssets } from "@/features/workflow/api"
import { AssetThumb } from "@/features/workflow/AssetThumb"
import {
  classifyPreviewMode,
  extractStoryDoc,
  pairPages,
  type PreviewMode,
  type StoryDoc,
  type PreviewPage,
} from "./runPreviewModel"

export interface RunPreviewProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectId: string
  planId: string
  nodes: GraphNode[]
  workflowName?: string
  mode?: PreviewMode
  onModeChange?: (mode: PreviewMode) => void
  // Phase 2 预留缝：歌词朗读 TTS 音频资产 id。本期未接线（transport 为禁用占位）。
  audioAssetId?: string
}

// 成品预览：全屏 Dialog。按模式渲 READER（图文翻页）或 MUSIC（专辑歌词）。
//   纯前端——数据全来自已有的 /state（nodes）+ shots + assets 公共端点。
export function RunPreview({
  open,
  onOpenChange,
  projectId,
  planId,
  nodes,
  workflowName,
  mode,
  onModeChange,
  audioAssetId,
}: RunPreviewProps) {
  // 模式：受控优先，否则内部启发式态。头部切换可覆盖。
  const detected = classifyPreviewMode(nodes, workflowName)
  const [internalMode, setInternalMode] = useState<PreviewMode>(detected)
  const activeMode = mode ?? internalMode
  const setMode = useCallback(
    (m: PreviewMode) => {
      if (onModeChange) onModeChange(m)
      else setInternalMode(m)
    },
    [onModeChange],
  )

  // 打开时用启发式重置内部模式（下次打开跟随当前工作流）。仅非受控时生效。
  const [trackedOpen, setTrackedOpen] = useState(open)
  if (trackedOpen !== open) {
    setTrackedOpen(open)
    if (open && !mode) setInternalMode(detected)
  }

  // 成品数据：仅在打开时拉取（关闭传 "" 不发请求）。
  const shotsQuery = useShots(open ? projectId : "", planId)
  const assetsQuery = useProjectAssets(open ? projectId : "", undefined, planId)
  const doc = extractStoryDoc(nodes)
  const pages = pairPages(shotsQuery.data ?? [], assetsQuery.data ?? [])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[92vh] max-h-[92vh] w-full max-w-[min(96vw,1080px)] flex-col gap-4 bg-bg-surface sm:max-w-[min(96vw,1080px)]">
        <DialogHeader className="flex-row items-center justify-between pr-8">
          <DialogTitle>成品预览</DialogTitle>
          {/* 模式切换：启发式默认，用户可覆盖。 */}
          <div className="flex items-center gap-1 rounded-md border border-line bg-bg-base p-0.5">
            {(["reader", "music"] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setMode(m)}
                aria-pressed={activeMode === m}
                className={
                  activeMode === m
                    ? "rounded px-2.5 py-1 text-[12px] text-primary-foreground bg-amber"
                    : "rounded px-2.5 py-1 text-[12px] text-text-2 hover:text-text-1"
                }
              >
                {m === "reader" ? "阅读" : "音乐"}
              </button>
            ))}
          </div>
        </DialogHeader>

        {activeMode === "music" ? (
          <MusicView doc={doc} pages={pages} audioAssetId={audioAssetId} />
        ) : (
          <ReaderView doc={doc} pages={pages} />
        )}
      </DialogContent>
    </Dialog>
  )
}

// 阅读模式：intro 页（标题 + story/概述 + 首图）→ 每分镜一页（大图 object-contain + 文案）。
function ReaderView({ doc, pages }: { doc: StoryDoc | null; pages: PreviewPage[] }) {
  // intro 页 index 0，其后每 shot 一页。
  const total = pages.length + 1
  const [index, setIndex] = useState(0)
  const clamp = useCallback((i: number) => Math.max(0, Math.min(total - 1, i)), [total])
  const next = useCallback(() => setIndex((i) => clamp(i + 1)), [clamp])
  const prev = useCallback(() => setIndex((i) => clamp(i - 1)), [clamp])

  // 键盘：← / → 翻页（Esc 由 Dialog 处理）。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "ArrowLeft") prev()
      else if (e.key === "ArrowRight") next()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [prev, next])

  const isIntro = index === 0
  const page = isIntro ? undefined : pages[index - 1]
  const firstImage = pages.find((p) => p.imageAssetId)?.imageAssetId

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3" data-slot="reader-view">
      <div className="relative flex min-h-0 flex-1 items-center justify-center">
        {index > 0 && (
          <button
            type="button"
            aria-label="上一页"
            onClick={prev}
            className="absolute left-0 z-10 grid h-9 w-9 place-items-center rounded-full bg-bg-raised/80 text-text-1 transition-colors hover:bg-bg-raised"
          >
            <ChevronLeft className="h-5 w-5" />
          </button>
        )}

        {isIntro ? (
          <div className="flex max-h-full flex-col items-center gap-4 overflow-y-auto py-2 text-center">
            {firstImage && (
              <AssetThumb
                assetId={firstImage}
                alt={doc?.title ?? "封面"}
                className="max-h-[46vh] w-auto border-0 object-contain"
              />
            )}
            <h2 className="text-[22px] font-semibold text-text-1">{doc?.title ?? "成品预览"}</h2>
            {doc?.story && (
              <p className="max-w-[60ch] whitespace-pre-wrap break-words text-[14px] leading-relaxed text-text-2">
                {doc.story}
              </p>
            )}
          </div>
        ) : (
          <div className="flex max-h-full min-h-0 w-full flex-col items-center gap-3 overflow-y-auto py-2">
            {page?.imageAssetId ? (
              <AssetThumb
                assetId={page.imageAssetId}
                alt={`第 ${index} 页`}
                className="max-h-[52vh] w-auto border-0 object-contain"
              />
            ) : (
              <div className="grid h-40 w-full place-items-center text-[12px] text-text-3">
                配图暂无产物
              </div>
            )}
            {page?.text && (
              <p className="max-w-[60ch] whitespace-pre-wrap break-words text-center text-[15px] leading-relaxed text-text-1">
                {page.text}
              </p>
            )}
          </div>
        )}

        {index < total - 1 && (
          <button
            type="button"
            aria-label="下一页"
            onClick={next}
            className="absolute right-0 z-10 grid h-9 w-9 place-items-center rounded-full bg-bg-raised/80 text-text-1 transition-colors hover:bg-bg-raised"
          >
            <ChevronRight className="h-5 w-5" />
          </button>
        )}
      </div>

      {/* 页码 + 翻页按钮。 */}
      <div className="flex items-center justify-between text-[12px] text-text-3">
        <Button variant="ghost" onClick={prev} disabled={index === 0}>
          上一页
        </Button>
        <span data-slot="page-counter">
          {index + 1} / {total}
        </span>
        <Button variant="ghost" onClick={next} disabled={index === total - 1}>
          下一页
        </Button>
      </div>
    </div>
  )
}

// 音乐模式：专辑布局——大封面 + 标题/情绪 + 可滚动歌词 + transport bar（本期禁用占位）。
function MusicView({
  doc,
  pages,
  audioAssetId,
}: {
  doc: StoryDoc | null
  pages: PreviewPage[]
  audioAssetId?: string
}) {
  const cover = pages.find((p) => p.imageAssetId)?.imageAssetId
  const lines = (doc?.lyrics ?? "").split("\n")

  return (
    <div className="flex min-h-0 flex-1 gap-6" data-slot="music-view">
      {/* 左：封面 + 标题/情绪。 */}
      <div className="flex w-[300px] shrink-0 flex-col items-center gap-4">
        {cover ? (
          <AssetThumb
            assetId={cover}
            alt={doc?.title ?? "封面"}
            className="h-[300px] w-[300px] border-0 object-cover shadow-lg"
          />
        ) : (
          <div className="grid h-[300px] w-[300px] place-items-center rounded-[10px] border border-line bg-bg-raised text-[12px] text-text-3">
            封面暂无产物
          </div>
        )}
        <div className="text-center">
          <h2 className="text-[20px] font-semibold text-text-1">{doc?.title ?? "未命名曲目"}</h2>
          {doc?.mood && <p className="mt-1 text-[13px] text-text-3">{doc.mood}</p>}
        </div>
      </div>

      {/* 右：可滚动歌词 + transport。 */}
      <div className="flex min-h-0 flex-1 flex-col gap-3">
        <div
          data-slot="lyrics-panel"
          className="min-h-0 flex-1 overflow-y-auto rounded-lg border border-line bg-bg-base p-4"
        >
          {lines.length > 0 && doc?.lyrics ? (
            lines.map((line, i) => {
              const isChorus = /^(副歌|Chorus)/i.test(line.trim())
              return (
                <p
                  key={i}
                  data-slot="lyric-line"
                  className={
                    isChorus
                      ? "py-0.5 text-[15px] font-semibold text-amber"
                      : "py-0.5 text-[15px] leading-relaxed text-text-1"
                  }
                >
                  {line || " "}
                </p>
              )
            })
          ) : (
            <p className="text-[13px] text-text-3">暂无歌词产物</p>
          )}
        </div>

        {/* Transport bar：本期为禁用占位。Phase 2 在此接线真实 TTS 播放器
            （<AssetAudio>/<AudioCard>），用上面预留的 audioAssetId prop。 */}
        <div
          data-slot="transport-bar"
          // Phase 2 缝：audioAssetId 就绪与否记在 data 属性上，后续接线时替换本占位。
          data-audio-ready={audioAssetId ? "true" : "false"}
          className="flex items-center gap-3 rounded-lg border border-line bg-bg-surface px-4 py-2.5"
        >
          <button
            type="button"
            disabled
            title="朗读歌词 (即将支持)"
            aria-label="朗读歌词 (即将支持)"
            className="grid h-9 w-9 place-items-center rounded-full bg-amber/60 text-primary-foreground opacity-50"
          >
            <Play className="h-4 w-4" />
          </button>
          <span className="text-[12px] text-text-3">朗读歌词 (即将支持)</span>
          {/* TODO(phase-2): 用 audioAssetId 接 <AssetAudio>/<AudioCard> 走真实 TTS 播放。 */}
        </div>
      </div>
    </div>
  )
}
