import { cn } from "@/lib/utils"
import { AssetThumb } from "@/features/workflow/AssetThumb"

// 原型 .asset-card：default / hover / sel（amber 描边）。
// 图走 /api/assets/{id}/content（302→签名 URL，AssetThumb，§已知缺口 1）。
export interface AssetCardProps {
  assetId: string
  alt?: string
  // 角标（如 version vtag / shot 编号）。
  caption?: string
  selected?: boolean
  onSelect?: () => void
  className?: string
}

export function AssetCard({
  assetId,
  alt = "",
  caption,
  selected,
  onSelect,
  className,
}: AssetCardProps) {
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
      <AssetThumb assetId={assetId} alt={alt} className="aspect-square w-full" />
      {caption != null && (
        <span className="truncate px-2 py-1.5 text-[11px] text-text-3">
          {caption}
        </span>
      )}
    </button>
  )
}
