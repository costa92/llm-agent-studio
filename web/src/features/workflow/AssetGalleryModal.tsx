import { useCallback, useEffect, useState } from "react"
import { ChevronLeft, ChevronRight } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { AssetThumb } from "./AssetThumb"
import { AssetPreviewActions } from "./AssetPreviewActions"
import { PromptPanel } from "./PromptPanel"

// 灯箱大图下的提示词元信息（按 assetId 取）。缺省则该图不展示提示词面板。
export interface AssetMeta {
  prompt?: string
  provider?: string
  model?: string
}

export interface AssetGalleryModalProps {
  // 该 run 已生成（done 且有 assetId）的素材 id，按生成顺序。
  assetIds: string[]
  open: boolean
  onOpenChange: (open: boolean) => void
  // 可选：按 assetId 提供 prompt/provider/model，灯箱大图下展示提示词面板。
  metaById?: Record<string, AssetMeta>
}

// 素材相册：居中模态 + 灯箱。
//   网格态：缩略图铺满模态，点图进灯箱。
//   灯箱态：大图 object-contain 不裁切；左右翻页 / ESC 回网格 / 打开·复制。
export function AssetGalleryModal({ assetIds, open, onOpenChange, metaById }: AssetGalleryModalProps) {
  const count = assetIds.length
  // 灯箱索引：null = 网格态；数字 = 看大图。
  const [lightbox, setLightbox] = useState<number | null>(null)

  // 关模态时复位灯箱（下次打开从网格开始）。reset-on-close 是惯用同步：仅在弹窗已关闭
  // (off-screen) 时触发，cascade 无可见重绘；改写进 onOpenChange 会丢失「任意关闭路径都复位」
  // 保证且无测试覆盖，故就地保留并抑制该 stricter 规则。
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (!open) setLightbox(null)
  }, [open])

  const go = useCallback(
    (delta: number) =>
      setLightbox((i) => (i == null ? i : (i + delta + count) % count)),
    [count],
  )

  // 灯箱键盘左右翻页（ESC 由 Dialog onEscapeKeyDown 接管：回网格而非关相册）。
  useEffect(() => {
    if (lightbox == null) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "ArrowLeft") go(-1)
      else if (e.key === "ArrowRight") go(1)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [lightbox, go])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="flex max-h-[88vh] w-full max-w-[min(92vw,1040px)] flex-col gap-4 bg-bg-surface sm:max-w-[min(92vw,1040px)]"
        onEscapeKeyDown={(e) => {
          // 灯箱态：ESC 回网格，不关整个相册。
          if (lightbox != null) {
            e.preventDefault()
            setLightbox(null)
          }
        }}
      >
        {lightbox == null ? (
          <>
            <DialogHeader>
              <DialogTitle>全部素材 · {count}</DialogTitle>
            </DialogHeader>
            {count === 0 ? (
              <div className="grid place-items-center py-16 text-text-3">
                <p>暂无已生成素材</p>
              </div>
            ) : (
              <div
                data-slot="gallery-grid"
                className="grid grid-cols-[repeat(auto-fill,minmax(132px,1fr))] gap-3 overflow-y-auto"
              >
                {assetIds.map((id, i) => (
                  <button
                    key={id}
                    type="button"
                    data-slot="gallery-thumb"
                    aria-label={`素材 ${i + 1}`}
                    onClick={() => setLightbox(i)}
                    className="overflow-hidden rounded-[10px] border border-line transition-colors hover:border-amber"
                  >
                    <AssetThumb assetId={id} alt={`素材 ${i + 1}`} className="aspect-square w-full" />
                  </button>
                ))}
              </div>
            )}
          </>
        ) : (
          <>
            <DialogHeader className="flex-row items-center justify-between pr-8">
              <DialogTitle>
                素材 {lightbox + 1} / {count}
              </DialogTitle>
              <button
                type="button"
                onClick={() => setLightbox(null)}
                className="text-[12px] text-text-3 underline-offset-2 hover:text-text-1 hover:underline"
              >
                ← 返回相册
              </button>
            </DialogHeader>
            <div className="relative flex min-h-0 flex-1 items-center justify-center">
              {count > 1 && (
                <button
                  type="button"
                  aria-label="上一张"
                  onClick={() => go(-1)}
                  className="absolute left-0 z-10 grid h-9 w-9 place-items-center rounded-full bg-bg-raised/80 text-text-1 transition-colors hover:bg-bg-raised"
                >
                  <ChevronLeft className="h-5 w-5" />
                </button>
              )}
              <AssetThumb
                assetId={assetIds[lightbox]}
                alt={`素材 ${lightbox + 1}`}
                className="max-h-[64vh] w-auto border-0 object-contain"
              />
              {count > 1 && (
                <button
                  type="button"
                  aria-label="下一张"
                  onClick={() => go(1)}
                  className="absolute right-0 z-10 grid h-9 w-9 place-items-center rounded-full bg-bg-raised/80 text-text-1 transition-colors hover:bg-bg-raised"
                >
                  <ChevronRight className="h-5 w-5" />
                </button>
              )}
            </div>
            <AssetPreviewActions assetId={assetIds[lightbox]} className="flex justify-center gap-2" />
            {(() => {
              const meta = metaById?.[assetIds[lightbox]]
              return meta && (meta.prompt || meta.provider || meta.model) ? (
                <PromptPanel
                  illustrationPrompt={meta.prompt}
                  provider={meta.provider}
                  model={meta.model}
                  className="self-center"
                />
              ) : null
            })()}
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
