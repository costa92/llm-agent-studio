import type { ReactNode } from "react"
import { Skeleton } from "@/components/ui/skeleton"
import { Button } from "@/components/studio/Button"

export interface CrudWorkspacePageProps {
  title: string
  headerActions?: ReactNode
  sidebar: ReactNode
  isLoading: boolean
  loadingSkeleton?: ReactNode
  isError: boolean
  onRetry?: () => void
  errorHint?: string
  isEmpty: boolean
  emptyState?: ReactNode
  children: ReactNode
}

// 工作区外壳（全高双列）：左 sidebar 常驻 + 右列(独立滚动) header(标题+动作槽) + 状态切换。
// 与 CrudResourcePage(居中单列)不同：error/loading/empty 只换右列内容区，sidebar 跨四态常驻。
// markup 逐字搬自资产库 LibraryView，保证零视觉回归（p-6 / overscroll-contain / py-20 等不可漂移）。
export function CrudWorkspacePage({
  title,
  headerActions,
  sidebar,
  isLoading,
  loadingSkeleton,
  isError,
  onRetry,
  errorHint = "加载失败",
  isEmpty,
  emptyState,
  children,
}: CrudWorkspacePageProps) {
  return (
    <div className="flex h-full">
      {sidebar}

      <div className="flex min-w-0 flex-1 flex-col p-6 overflow-y-auto overscroll-contain">
        <header className="mb-5 flex items-center justify-between gap-3">
          <h1 className="font-heading text-[22px] font-bold text-text-1">{title}</h1>
          {headerActions != null && <div className="flex items-center gap-3">{headerActions}</div>}
        </header>

        {/* loading 先判：复刻 LibraryView 原顺序；四态由调用方约定互斥，顺序仅影响巧合并发态。 */}
        {isLoading ? (
          loadingSkeleton != null ? (
            loadingSkeleton
          ) : (
            <div className="flex flex-col gap-3">
              {Array.from({ length: 3 }).map((_, i) => (
                <Skeleton key={i} className="h-10 rounded-lg" />
              ))}
            </div>
          )
        ) : isError ? (
          <div className="flex flex-col items-center gap-3 py-20 text-center">
            <p className="text-text-2">{errorHint}</p>
            {onRetry && (
              <Button variant="ghost" onClick={onRetry}>
                重试
              </Button>
            )}
          </div>
        ) : isEmpty ? (
          emptyState != null ? (
            emptyState
          ) : (
            <p className="py-8 text-center text-[13px] text-text-3">暂无数据。</p>
          )
        ) : (
          children
        )}
      </div>
    </div>
  )
}
