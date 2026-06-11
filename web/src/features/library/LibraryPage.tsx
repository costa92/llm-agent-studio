import { Sheet, SheetContent } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import { AssetCard } from "@/components/studio/AssetCard"
import { AssetThumb } from "@/features/workflow/AssetThumb"
import { PromptBox } from "@/components/studio/PromptBox"
import { LineageTrail, type LineageNode } from "@/components/studio/LineageTrail"
import { cn } from "@/lib/utils"
import type { Asset, AssetDetail, Project, Style } from "@/lib/types"
import type { LibraryFilter } from "./filter"
import { STATUS_OPTIONS, TYPE_OPTIONS } from "./filter"
import { assetStatusLabel, assetStatusVariant } from "./assetStatus"

export interface LibraryViewProps {
  assets: Asset[]
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  // 加载更多（keyset 游标累积）。
  hasNextPage: boolean
  isFetchingNextPage: boolean
  onLoadMore: () => void
  // 过滤态 + 变更回调（typed search params 持有）。
  filter: LibraryFilter
  onFilterChange: (next: LibraryFilter) => void
  // 项目下拉选项（org 项目列表）+ 风格选项（prompt-styles）。
  projects: Project[]
  styles: Style[]
  // 选中资产（?asset= 控制详情 Drawer 开合）。
  selectedId: string | null
  onSelect: (id: string | null) => void
  detail: AssetDetail | undefined
  detailLoading: boolean
}

// 资产库（viewer+ 只读）：左 FilterRail（类型/状态/风格/项目 + tag 搜索）+ 右网格
//（status Badge + version vtag）+ "加载更多"（keyset）+ 详情 Drawer（版本血缘）。
export function LibraryView({
  assets,
  isLoading,
  isError,
  onRetry,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  filter,
  onFilterChange,
  projects,
  styles,
  selectedId,
  onSelect,
  detail,
  detailLoading,
}: LibraryViewProps) {
  // 单选过滤：再次点选同值 = 取消（回到"全部"）。
  function toggle(key: keyof LibraryFilter, value: string): void {
    onFilterChange({ ...filter, [key]: filter[key] === value ? undefined : value })
  }

  return (
    <div className="flex h-full">
      <FilterRail
        filter={filter}
        onToggle={toggle}
        onTagChange={(tag) => onFilterChange({ ...filter, tag: tag || undefined })}
        onProjectChange={(project) =>
          onFilterChange({ ...filter, project: project || undefined })
        }
        projects={projects}
        styles={styles}
      />

      <div className="flex min-w-0 flex-1 flex-col p-6">
        <header className="mb-5 flex items-center justify-between">
          <h1 className="font-heading text-[22px] font-bold text-text-1">资产库</h1>
          <span className="text-[12px] text-text-3">{assets.length} 个资产</span>
        </header>

        {isLoading ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
            {Array.from({ length: 12 }).map((_, i) => (
              <Skeleton key={i} className="aspect-square rounded-[10px]" />
            ))}
          </div>
        ) : isError ? (
          <div className="flex flex-col items-center gap-3 py-20 text-center">
            <p className="text-text-2">资产加载失败</p>
            <Button variant="ghost" onClick={onRetry}>
              重试
            </Button>
          </div>
        ) : assets.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-20 text-center">
            <p className="text-text-1">没有匹配的资产</p>
            <p className="text-[12.5px] text-text-3">调整筛选条件试试</p>
          </div>
        ) : (
          <>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-3">
              {assets.map((asset) => (
                <div key={asset.id} className="relative">
                  <AssetCard
                    assetId={asset.id}
                    alt={asset.prompt}
                    caption={`v${asset.version}`}
                    selected={asset.id === selectedId}
                    onSelect={() => onSelect(asset.id)}
                  />
                  <Badge
                    variant={assetStatusVariant(asset.status)}
                    className="pointer-events-none absolute left-1.5 top-1.5"
                  >
                    {assetStatusLabel(asset.status)}
                  </Badge>
                </div>
              ))}
            </div>
            {hasNextPage && (
              <div className="mt-5 flex justify-center">
                <Button
                  variant="ghost"
                  onClick={onLoadMore}
                  disabled={isFetchingNextPage}
                >
                  {isFetchingNextPage ? "加载中…" : "加载更多"}
                </Button>
              </div>
            )}
          </>
        )}
      </div>

      {/* 资产详情 Drawer（?asset= 控制开合）—— 含版本血缘、播放器/缩略图。 */}
      <Sheet
        open={selectedId != null}
        onOpenChange={(open) => {
          if (!open) onSelect(null)
        }}
      >
        <SheetContent className="w-[460px] gap-0 overflow-y-auto bg-bg-surface p-0 sm:max-w-[460px]">
          {detailLoading || detail == null ? (
            <div className="p-6">
              <Skeleton className="aspect-square w-full rounded-[10px]" />
            </div>
          ) : (
            <AssetDetailBody detail={detail} />
          )}
        </SheetContent>
      </Sheet>
    </div>
  )
}

interface FilterRailProps {
  filter: LibraryFilter
  onToggle: (key: keyof LibraryFilter, value: string) => void
  onTagChange: (tag: string) => void
  onProjectChange: (project: string) => void
  projects: Project[]
  styles: Style[]
}

// 左过滤栏：tag 搜索 + 类型/状态/风格单选 chip + 项目下拉。
function FilterRail({
  filter,
  onToggle,
  onTagChange,
  onProjectChange,
  projects,
  styles,
}: FilterRailProps) {
  return (
    <aside className="flex w-56 flex-shrink-0 flex-col gap-5 overflow-y-auto border-r border-line bg-bg-surface p-4">
      <div className="flex flex-col gap-1.5">
        <label
          htmlFor="lib-tag"
          className="text-[11px] font-semibold tracking-[0.08em] text-text-3"
        >
          标签搜索
        </label>
        <input
          id="lib-tag"
          type="search"
          value={filter.tag ?? ""}
          onChange={(e) => onTagChange(e.target.value)}
          placeholder="按标签筛选…"
          className="rounded-md border border-line bg-bg-base px-2.5 py-1.5 text-[12px] text-text-1 placeholder:text-text-3 focus-visible:outline-2 focus-visible:outline-amber"
        />
      </div>

      <FilterGroup label="类型">
        {TYPE_OPTIONS.map((opt) => (
          <FilterChip
            key={opt.value}
            active={filter.type === opt.value}
            disabled={opt.disabled}
            onClick={() => onToggle("type", opt.value)}
          >
            {opt.label}
            {opt.disabled && <span className="ml-1 text-text-3">· 二期</span>}
          </FilterChip>
        ))}
      </FilterGroup>

      <FilterGroup label="状态">
        {STATUS_OPTIONS.map((opt) => (
          <FilterChip
            key={opt.value}
            active={filter.status === opt.value}
            onClick={() => onToggle("status", opt.value)}
          >
            {opt.label}
          </FilterChip>
        ))}
      </FilterGroup>

      <FilterGroup label="风格">
        {styles.map((s) => (
          <FilterChip
            key={s.name}
            active={filter.style === s.name}
            onClick={() => onToggle("style", s.name)}
          >
            {s.name}
          </FilterChip>
        ))}
      </FilterGroup>

      <div className="flex flex-col gap-1.5">
        <label
          htmlFor="lib-project"
          className="text-[11px] font-semibold tracking-[0.08em] text-text-3"
        >
          项目
        </label>
        <select
          id="lib-project"
          value={filter.project ?? ""}
          onChange={(e) => onProjectChange(e.target.value)}
          className="rounded-md border border-line bg-bg-base px-2.5 py-1.5 text-[12px] text-text-1 focus-visible:outline-2 focus-visible:outline-amber"
        >
          <option value="">全部项目</option>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </div>
    </aside>
  )
}

function FilterGroup({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <span className="text-[11px] font-semibold tracking-[0.08em] text-text-3">
        {label}
      </span>
      <div className="flex flex-wrap gap-1.5">{children}</div>
    </div>
  )
}

function FilterChip({
  active,
  disabled,
  onClick,
  children,
}: {
  active: boolean
  disabled?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "rounded-full border px-2.5 py-1 text-[12px] transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        active
          ? "border-amber bg-amber/[0.12] text-amber"
          : "border-line text-text-2 hover:border-text-3 hover:text-text-1",
      )}
    >
      {children}
    </button>
  )
}

// 详情 Drawer 内容：媒体（图/视频/音频）+ KV + Prompt + 版本血缘。
function AssetDetailBody({ detail }: { detail: AssetDetail }) {
  const { asset, versions } = detail
  const nodes: LineageNode[] = [...versions]
    .sort((a, b) => a.version - b.version)
    .map((v) => ({
      key: v.id,
      label: `v${v.version}`,
      current: v.id === asset.id,
    }))

  return (
    <>
      <AssetMedia asset={asset} />

      <div className="flex flex-col gap-4 p-5">
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[12px]">
          <Kv label="类型" value={asset.type} />
          <Kv label="状态" value={assetStatusLabel(asset.status)} />
          <Kv label="风格" value={asset.style || "—"} />
          <Kv label="Provider·Model" value={`${asset.provider} · ${asset.model}`} />
          <Kv label="版本" value={`v${asset.version}`} />
        </dl>

        <section className="flex flex-col gap-1.5">
          <h4 className="text-[11px] font-semibold tracking-[0.08em] text-text-3">
            PROMPT
          </h4>
          <PromptBox prompt={asset.prompt} />
        </section>

        {nodes.length > 1 && (
          <section className="flex flex-col gap-1.5">
            <h4 className="text-[11px] font-semibold tracking-[0.08em] text-text-3">
              版本血缘
            </h4>
            <LineageTrail nodes={nodes} />
          </section>
        )}
      </div>
    </>
  )
}

// 媒体渲染：视频/音频用原生播放器（生成后端驱动，前端只播放），
// 其余（图片）走 AssetThumb（/content 302→签名 URL）。
function AssetMedia({ asset }: { asset: Asset }) {
  const src = `/api/assets/${asset.id}/content`
  if (asset.type === "video") {
    return <video controls src={src} className="w-full bg-black" aria-label={asset.prompt} />
  }
  if (asset.type === "audio") {
    return (
      <div className="p-5">
        <audio controls src={src} className="w-full" aria-label={asset.prompt} />
      </div>
    )
  }
  return <AssetThumb assetId={asset.id} alt={asset.prompt} className="aspect-square w-full rounded-none border-0" />
}

function Kv({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-text-3">{label}</dt>
      <dd className="text-right font-medium text-text-1">{value}</dd>
    </>
  )
}
