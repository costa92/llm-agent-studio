import { cn } from "@/lib/utils"
import { AssetThumb } from "@/features/workflow/AssetThumb.tsx"
import { AudioCard } from "@/features/workflow/AudioCard"

// 原型 .asset-card：default / hover / sel（amber 描边）。
// 图走 /api/assets/{id}/content（302→签名 URL，AssetThumb，§已知缺口 1）。
// audio 直接在卡内懒加载可播放（AudioCard，点「试听」才拉字节）；其余非 image（video）
// 不拉缩略图，显类型徽标占位（避免破图，Phase 3 T4）。
export interface AssetCardProps {
  assetId: string
  alt?: string
  // 资产类型；缺省按 image 处理（走 AssetThumb）。非 image 显类型占位。
  type?: string
  // 角标（如 version vtag / shot 编号）。
  caption?: string
  selected?: boolean
  onSelect?: () => void
  className?: string
}

export function AssetCard({
  assetId,
  alt = "",
  type,
  caption,
  selected,
  onSelect,
  className,
}: AssetCardProps) {
  const isImage = type == null || type === "image"
  const isAudio = type === "audio"

  const outerClass = cn(
    "group flex flex-col w-full overflow-hidden rounded-[10px] border bg-bg-surface text-left transition-colors",
    selected ? "border-amber" : "border-line hover:border-text-3",
    className,
  )

  const inner = (
    <>
      {isImage ? (
        <AssetThumb assetId={assetId} alt={alt} className="aspect-square w-full" />
      ) : isAudio ? (
        <AudioCard assetId={assetId} />
      ) : (
        <span className="grid aspect-square w-full place-items-center bg-bg-raised text-[10px] font-mono uppercase tracking-wider text-text-3">
          {type}
        </span>
      )}
      {caption != null && (
        <span className="truncate px-2 py-1.5 text-[11px] text-text-3">
          {caption}
        </span>
      )}
    </>
  )

  // audio 卡内含 <button>「试听」+ <audio controls>，不能嵌在原生 <button> 内（无效嵌套）。
  //   故 audio 路径外层改用 div[role=button] + 键盘可达（Enter/Space），行为等价于原 <button>。
  if (isAudio) {
    return (
      <div
        role="button"
        tabIndex={0}
        data-slot="asset-card"
        data-selected={selected ? "" : undefined}
        onClick={onSelect}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault()
            onSelect?.()
          }
        }}
        className={outerClass}
      >
        {inner}
      </div>
    )
  }

  return (
    <button
      type="button"
      data-slot="asset-card"
      data-selected={selected ? "" : undefined}
      onClick={onSelect}
      className={outerClass}
    >
      {inner}
    </button>
  )
}
