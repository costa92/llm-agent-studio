import { cn } from "@/lib/utils"
import { AssetThumb } from "@/features/workflow/AssetThumb.tsx"

// 原型 .asset-card：default / hover / sel（amber 描边）。
// 图走 /api/assets/{id}/content（302→签名 URL，AssetThumb，§已知缺口 1）。
// type 非 image（video/audio）时不拉缩略图，显类型徽标占位（避免破图，Phase 3 T4）。
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
  return (
    <button
      type="button"
      data-slot="asset-card"
      data-selected={selected ? "" : undefined}
      onClick={onSelect}
      className={cn(
        "group flex flex-col overflow-hidden rounded-[10px] border bg-bg-surface text-left transition-colors",
        selected ? "border-amber" : "border-line hover:border-text-3",
        className,
      )}
    >
      {isImage ? (
        <AssetThumb assetId={assetId} alt={alt} className="aspect-square w-full" />
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
    </button>
  )
}
