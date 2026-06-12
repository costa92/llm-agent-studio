import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { cn } from "@/lib/utils"
import { Skeleton } from "@/components/ui/skeleton"
import { Badge } from "@/components/studio/Badge"
import { Button } from "@/components/studio/Button"
import type { ProjectStatus, TaskRow } from "@/lib/types"
import { useTaskBoard } from "./api"
import { quickAction, statusLabel, statusVariant, taskBucket } from "./status"
import { formatRelative } from "./relativeTime"

// tab 定义：标签 + 对应 counts 键（全部不带桶过滤）。
const TABS: { label: string; bucket: string | null; countKey: string }[] = [
  { label: "全部", bucket: null, countKey: "all" },
  { label: "运行中", bucket: "运行中", countKey: "running" },
  { label: "待审核", bucket: "待审核", countKey: "review" },
  { label: "完成", bucket: "完成", countKey: "completed" },
  { label: "失败", bucket: "失败", countKey: "failed" },
]

// 5 格进度（与工作台 pip 同款：done=asset 琥珀实心 / idle=line 空框）。
// round(done/total*5)，bucket=完成强制 5，total=0 时 0 格。
function fillCount(row: TaskRow): number {
  if (taskBucket(row.status) === "完成") return 5
  if (row.progressTotal <= 0) return 0
  return Math.min(5, Math.round((row.progressDone / row.progressTotal) * 5))
}

function ProgressPips({ filled }: { filled: number }) {
  return (
    <div
      role="list"
      aria-label="项目进度"
      className="flex flex-wrap gap-[6px]"
    >
      {Array.from({ length: 5 }).map((_, i) => (
        <span
          key={i}
          role="listitem"
          data-slot="task-pip"
          data-filled={i < filled}
          className={cn(
            "h-3.5 w-3.5 rounded-[4px] border-[1.5px]",
            i < filled ? "border-asset bg-asset" : "border-line",
          )}
        />
      ))}
    </div>
  )
}

function pct(row: TaskRow): number {
  if (taskBucket(row.status) === "完成") return 100
  if (row.progressTotal <= 0) return 0
  return Math.round((row.progressDone / row.progressTotal) * 100)
}

export interface TaskCenterViewProps {
  rows: TaskRow[] | undefined
  counts: Record<string, number> | undefined
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  onAction: (row: TaskRow) => void
  /** 注入 now 便于确定性单测相对时间。 */
  now?: number
}

// 纯展示视图（无路由/Query 依赖），便于单测 loading/empty/error/tab 过滤。
export function TaskCenterView({
  rows,
  counts,
  isLoading,
  isError,
  onRetry,
  onAction,
  now,
}: TaskCenterViewProps) {
  const [activeTab, setActiveTab] = useState<string>("全部")

  const visible = (rows ?? []).filter((row) => {
    const tab = TABS.find((t) => t.label === activeTab)
    if (!tab || tab.bucket == null) return true
    return taskBucket(row.status) === tab.bucket
  })

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-5 p-6">
      <header className="flex flex-col gap-1">
        <h1 className="font-heading text-[22px] font-bold text-text-1">任务中心</h1>
        <p className="text-[12.5px] text-text-3">项目运行看板</p>
      </header>

      {/* tab 栏：全部/运行中/待审核/完成/失败，带 counts 数字。 */}
      <div role="tablist" aria-label="任务筛选" className="flex flex-wrap gap-2">
        {TABS.map((tab) => {
          const count = counts?.[tab.countKey]
          const active = activeTab === tab.label
          return (
            <button
              key={tab.label}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setActiveTab(tab.label)}
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-[12.5px] transition-colors",
                active
                  ? "border-amber/40 bg-amber/12 text-amber"
                  : "border-line text-text-2 hover:border-text-3 hover:text-text-1",
              )}
            >
              {tab.label}
              {count != null && count > 0 && (
                <span className="text-[11px] text-text-3">{count}</span>
              )}
            </button>
          )
        })}
      </div>

      {isLoading ? (
        <div className="flex flex-col gap-2.5">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-[58px] rounded-xl" />
          ))}
        </div>
      ) : isError ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-2">任务加载失败</p>
          <Button variant="ghost" onClick={onRetry}>
            重试
          </Button>
        </div>
      ) : visible.length > 0 ? (
        <div className="flex flex-col gap-2.5">
          {visible.map((row) => {
            const filled = fillCount(row)
            const action = quickAction(row, "")
            return (
              <div
                key={row.projectId}
                className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-line bg-bg-surface px-[18px] py-3.5"
              >
                <span className="font-heading text-[15px] font-medium text-text-1">
                  {row.name}
                </span>
                <Badge variant={statusVariant(row.status as ProjectStatus)}>
                  {statusLabel(row.status as ProjectStatus)}
                </Badge>

                {row.failed ? (
                  <span className="text-[12.5px] text-danger">
                    {row.failingAgent} 出错
                  </span>
                ) : (
                  <>
                    <ProgressPips filled={filled} />
                    <span className="text-[12px] text-text-3">{pct(row)}%</span>
                  </>
                )}

                <div className="ml-auto flex items-center gap-3">
                  {row.lastActivityAt && (
                    <span className="text-[11px] text-text-3">
                      {formatRelative(row.lastActivityAt, now)}
                    </span>
                  )}
                  <Button
                    variant={row.status === "review" ? "green" : "ghost"}
                    onClick={() => onAction(row)}
                  >
                    {action.label} →
                  </Button>
                </div>
              </div>
            )
          })}
        </div>
      ) : (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-1">暂无任务</p>
          <p className="text-[12.5px] text-text-3">还没有项目在运行</p>
        </div>
      )}
    </div>
  )
}

// 容器：拉取 task-board + 用 quickAction 把行内动作接到路由。
export function TaskCenterPage({ org }: { org: string }) {
  const navigate = useNavigate()
  const board = useTaskBoard(org)

  function handleAction(row: TaskRow): void {
    const action = quickAction(row, org)
    void navigate({
      to: action.to,
      params: action.params,
      ...(action.search ? { search: action.search } : {}),
    } as never)
  }

  return (
    <TaskCenterView
      rows={board.data?.items}
      counts={board.data?.counts}
      isLoading={board.isLoading}
      isError={board.isError}
      onRetry={() => void board.refetch()}
      onAction={handleAction}
    />
  )
}
