import { useCallback, useEffect, useRef, useState } from "react"
import { ChevronLeft, ChevronRight } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/studio/Button"
import { AssetThumb } from "./AssetThumb"
import { AssetAudio } from "./AssetAudio"
import { PromptPanel } from "./PromptPanel"

// 绘本一页：封面/内容/结尾。封面与结尾通常只有插图+标题，内容页带旁白+音频+提示词元信息。
export interface PicturePage {
  kind: "cover" | "content" | "ending"
  title?: string
  illustrationAssetId?: string
  audioAssetId?: string
  narration?: string
  prompt?: string
  provider?: string
  model?: string
  voice?: string
}

export interface PictureBookReaderProps {
  pages: PicturePage[]
  open: boolean
  onOpenChange: (open: boolean) => void
  initialIndex?: number
  // 单页重生成入口：重新生成该页插图 / 用新文本重配旁白。运行页接 mutation。
  onRegenIllustration?: (page: PicturePage) => void
  onEditNarration?: (page: PicturePage, newText: string) => void
}

// 绘本阅读器：居中模态，按页 kind 渲染封面/内容/结尾。
//   内容页大插图 object-contain + 旁白 + 音频 + 翻页 + 页码 + 自动朗读 + 提示词 +
//   单页重生成/编辑旁白入口。自动朗读开启时该页 audio 播放结束自动翻到下一页（缺音频则停）。
//   键盘：← / → 翻页，Esc 关闭。
export function PictureBookReader({
  pages,
  open,
  onOpenChange,
  initialIndex = 0,
  onRegenIllustration,
  onEditNarration,
}: PictureBookReaderProps) {
  const total = pages.length
  const [index, setIndex] = useState(initialIndex)
  const [autoRead, setAutoRead] = useState(false)
  // 编辑旁白小 Dialog 的草稿（null = 未打开）。
  const [editDraft, setEditDraft] = useState<string | null>(null)
  const audioRef = useRef<HTMLAudioElement>(null)

  // 打开时复位到 initialIndex（下次打开从指定页开始）。
  const [trackedOpen, setTrackedOpen] = useState(open)
  if (trackedOpen !== open) {
    setTrackedOpen(open)
    if (open) setIndex(initialIndex)
  }

  const clamp = useCallback(
    (i: number) => Math.max(0, Math.min(total - 1, i)),
    [total],
  )
  const goTo = useCallback((i: number) => setIndex(clamp(i)), [clamp])
  const next = useCallback(() => setIndex((i) => clamp(i + 1)), [clamp])
  const prev = useCallback(() => setIndex((i) => clamp(i - 1)), [clamp])

  const page = pages[index]
  const hasPrev = index > 0
  const hasNext = index < total - 1

  // 键盘：← / → 翻页（编辑旁白弹窗打开时让位给输入）。
  useEffect(() => {
    if (!open || editDraft != null) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "ArrowLeft") prev()
      else if (e.key === "ArrowRight") next()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [open, editDraft, prev, next])

  // 自动朗读：开关开 + 该页有音频时播放当前页音频；换页/关闭时由 key 重挂的 <audio> 接管。
  useEffect(() => {
    if (!autoRead || !page?.audioAssetId) return
    const el = audioRef.current
    if (el) void el.play().catch(() => {})
  }, [autoRead, index, page?.audioAssetId])

  // 音频播放结束 → 自动翻下一页（到末页停）。仅自动朗读开启时生效。
  const handleEnded = useCallback(() => {
    if (autoRead && hasNext) next()
  }, [autoRead, hasNext, next])

  const handleSaveNarration = () => {
    if (editDraft != null && page) {
      onEditNarration?.(page, editDraft)
      setEditDraft(null)
    }
  }

  function renderCover() {
    return (
      <div className="flex flex-col items-center gap-5 py-4">
        {page.illustrationAssetId && (
          <AssetThumb
            assetId={page.illustrationAssetId}
            alt={page.title ?? "封面"}
            className="max-h-[58vh] w-auto border-0 object-contain"
          />
        )}
        <h2 className="text-center text-[22px] font-semibold text-text-1">
          {page.title ?? "绘本"}
        </h2>
        <Button variant="amber" onClick={() => goTo(1)}>
          ▶ 开始阅读
        </Button>
      </div>
    )
  }

  function renderEnding() {
    return (
      <div className="flex flex-col items-center gap-5 rounded-[12px] bg-bg-raised/40 py-8">
        {page.illustrationAssetId && (
          <AssetThumb
            assetId={page.illustrationAssetId}
            alt={page.title ?? "结尾"}
            className="max-h-[50vh] w-auto border-0 object-contain"
          />
        )}
        <h2 className="text-center text-[20px] font-semibold text-text-1">
          {page.title ?? "全剧终"}
        </h2>
        <Button variant="ghost" onClick={() => goTo(0)}>
          ↺ 重新阅读
        </Button>
      </div>
    )
  }

  function renderContent() {
    return (
      <div className="flex min-h-0 flex-1 flex-col gap-3">
        <div className="relative flex min-h-0 flex-1 items-center justify-center">
          {hasPrev && (
            <button
              type="button"
              aria-label="上一页"
              onClick={prev}
              className="absolute left-0 z-10 grid h-9 w-9 place-items-center rounded-full bg-bg-raised/80 text-text-1 transition-colors hover:bg-bg-raised"
            >
              <ChevronLeft className="h-5 w-5" />
            </button>
          )}
          {page.illustrationAssetId ? (
            <AssetThumb
              assetId={page.illustrationAssetId}
              alt={`第 ${index} 页`}
              className="max-h-[48vh] w-auto border-0 object-contain"
            />
          ) : (
            <div className="grid h-40 place-items-center text-text-3">插图缺失</div>
          )}
          {hasNext && (
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

        {page.narration && (
          <p className="whitespace-pre-wrap break-words text-center text-[15px] leading-relaxed text-text-1">
            {page.narration}
          </p>
        )}

        {/* 缺音频时隐藏播放器（且不自动推进）。 */}
        {page.audioAssetId && (
          <AssetAudio
            key={`${page.audioAssetId}`}
            ref={audioRef}
            assetId={page.audioAssetId}
            onEnded={handleEnded}
          />
        )}

        <div className="flex flex-wrap items-center justify-between gap-2 text-[12px]">
          <label className="inline-flex items-center gap-1.5 text-text-2">
            <input
              type="checkbox"
              checked={autoRead}
              onChange={(e) => setAutoRead(e.target.checked)}
            />
            自动朗读
          </label>
          <div className="flex items-center gap-3">
            {onRegenIllustration && (
              <button
                type="button"
                onClick={() => onRegenIllustration(page)}
                className="text-text-3 underline-offset-2 transition-colors hover:text-text-1 hover:underline"
              >
                ↻ 重新生成插图
              </button>
            )}
            {onEditNarration && (
              <button
                type="button"
                onClick={() => setEditDraft(page.narration ?? "")}
                className="text-text-3 underline-offset-2 transition-colors hover:text-text-1 hover:underline"
              >
                ✎ 编辑旁白
              </button>
            )}
          </div>
        </div>
      </div>
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[88vh] w-full max-w-[min(94vw,960px)] flex-col gap-4 bg-bg-surface sm:max-w-[min(94vw,960px)]">
        <DialogHeader className="flex-row items-center justify-between pr-8">
          <DialogTitle>
            {page.kind === "cover"
              ? "绘本封面"
              : page.kind === "ending"
                ? "绘本结尾"
                : `第 ${index} / ${total - 2 > 0 ? total - 2 : total} 页`}
          </DialogTitle>
          {/* 右上：内容页才挂提示词面板（封面/结尾无 prompt 元信息）。 */}
          {page.kind === "content" && (
            <PromptPanel
              illustrationPrompt={page.prompt}
              narration={page.narration}
              provider={page.provider}
              model={page.model}
              voice={page.voice}
            />
          )}
        </DialogHeader>

        {page.kind === "cover"
          ? renderCover()
          : page.kind === "ending"
            ? renderEnding()
            : renderContent()}

        {/* 编辑旁白小 Dialog：textarea 预填当前旁白，保存调 onEditNarration。 */}
        <Dialog open={editDraft != null} onOpenChange={(o) => !o && setEditDraft(null)}>
          <DialogContent className="bg-bg-surface sm:max-w-md">
            <DialogHeader>
              <DialogTitle>编辑旁白</DialogTitle>
            </DialogHeader>
            <textarea
              value={editDraft ?? ""}
              onChange={(e) => setEditDraft(e.target.value)}
              rows={4}
              className="w-full rounded-[10px] border border-line bg-bg-raised p-2 text-[13px] text-text-1"
            />
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={() => setEditDraft(null)}>
                取消
              </Button>
              <Button variant="amber" onClick={handleSaveNarration}>
                保存并重配音
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </DialogContent>
    </Dialog>
  )
}
