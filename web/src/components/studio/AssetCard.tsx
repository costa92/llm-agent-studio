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
  // 是否有可取的字节。缺省 true（既有调用方不变）。传 false（blobKey 与 url 皆空，
  // 如生成 failed/canceled 的空资产）时，图片不走 AssetThumb——否则 /content 必 404
  // 并刷 console。此时显中性占位，状态语义由调用方叠加的 Badge 承担。
  hasContent?: boolean
  // 角标（如 version vtag / shot 编号）。
  caption?: string
  // 详情高亮态（当前打开抽屉的资产）。与批量勾选（checked）互相独立。
  selected?: boolean
  onSelect?: () => void
  // 批量勾选：selectable 时在卡角叠加复选框（用于审核台批量采纳）；
  // checked=勾选态，onToggleCheck=切换。语义独立于 selected/onSelect。
  selectable?: boolean
  checked?: boolean
  onToggleCheck?: () => void
  className?: string
}

export function AssetCard({
  assetId,
  alt = "",
  type,
  hasContent = true,
  caption,
  selected,
  onSelect,
  selectable,
  checked,
  onToggleCheck,
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
      {isImage && !hasContent ? (
        <span className="grid aspect-square w-full place-items-center bg-bg-raised text-[10px] text-text-3">
          暂无内容
        </span>
      ) : isImage ? (
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
  const card = isAudio ? (
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
  ) : (
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

  if (!selectable) return card

  // 批量勾选复选框：作为卡片的兄弟节点绝对定位在左上角（不嵌进 <button>，避免无效嵌套），
  //   点击只切换勾选、不触发详情选中。
  return (
    <div className="relative">
      {card}
      <input
        type="checkbox"
        aria-label={checked ? "取消选择该资产" : "选择该资产"}
        checked={checked ?? false}
        onChange={onToggleCheck}
        onClick={(e) => e.stopPropagation()}
        className="absolute left-2 top-2 z-10 size-4 cursor-pointer accent-amber"
      />
    </div>
  )
}
