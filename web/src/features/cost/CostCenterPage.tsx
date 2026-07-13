import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Button } from "@/components/studio/Button"
import { StatCard } from "@/components/studio/StatCard"
import { BarRow } from "@/components/studio/BarRow"
import type {
  Aggregate,
  LedgerEntry,
  MemberAggregate,
  ProjectAggregate,
} from "@/lib/types"
import {
  RANGE_PRESETS,
  costRatio,
  formatCount,
  formatCurrency,
  isUnpriced,
} from "./format"
import { UnpricedBadge } from "./UnpricedBadge"

// per-project 条色轮换 agent 色（设计 token §per-agent 语义色）。
const BAR_COLORS = ["var(--asset)", "var(--script)", "var(--board)", "var(--review)"]

export interface CostCenterViewProps {
  aggregate: Aggregate | undefined
  projects: ProjectAggregate[] | undefined
  members: MemberAggregate[] | undefined
  generations: LedgerEntry[] | undefined
  // 生成明细「加载更多」（keyset 游标累积，同资产库）。
  hasNextPage: boolean
  isFetchingNextPage: boolean
  onLoadMore: () => void
  isLoading: boolean
  isError: boolean
  onRetry: () => void
  // 时间范围预设（受控）。
  rangeValue: string
  onRangeChange: (value: string) => void
  // 导出当前时间范围的生成明细为 CSV（宿主取全量分页 + 裁剪 + 触发下载）。
  onExport: () => void
  isExporting: boolean
}

// 成本中心（admin-only）：3 StatCard + 按项目 BarRow + 生成明细 DataTable + 时间范围下拉。
export function CostCenterView({
  aggregate,
  projects,
  members,
  generations,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
  isLoading,
  isError,
  onRetry,
  rangeValue,
  onRangeChange,
  onExport,
  isExporting,
}: CostCenterViewProps) {
  const activePreset =
    RANGE_PRESETS.find((p) => p.value === rangeValue) ?? RANGE_PRESETS[1]

  if (isError) {
    return (
      <div className="flex flex-col items-center gap-3 py-20 text-center">
        <p className="text-text-2">成本数据加载失败</p>
        <Button variant="ghost" onClick={onRetry}>
          重试
        </Button>
      </div>
    )
  }

  const maxMicros = Math.max(0, ...(projects ?? []).map((p) => p.costMicros))
  const maxMemberMicros = Math.max(0, ...(members ?? []).map((m) => m.costMicros))
  const hasData =
    aggregate != null && (aggregate.generations > 0 || (projects?.length ?? 0) > 0)
  // 有用量却计费 ¥0 的项目/成员/台账行 → 页面提示这部分未计入 ¥ 合计。
  const hasUnpriced =
    (projects ?? []).some((p) => isUnpriced(p)) ||
    (members ?? []).some((m) => m.unpriced) ||
    (generations ?? []).some((r) => isUnpriced(r))

  return (
    <div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="font-heading text-[22px] font-bold text-text-1">成本中心</h1>
        <div className="flex items-center gap-2">
          <Button variant="ghost" onClick={onExport} disabled={isExporting}>
            {isExporting ? "导出中…" : "导出 CSV"}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost">{activePreset.label} ▾</Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuRadioGroup value={rangeValue} onValueChange={onRangeChange}>
                {RANGE_PRESETS.map((p) => (
                  <DropdownMenuRadioItem key={p.value} value={p.value}>
                    {p.label}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      {isLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-24 rounded-xl" />
          ))}
        </div>
      ) : !hasData ? (
        <div className="flex flex-col items-center gap-3 py-20 text-center">
          <p className="text-text-1">暂无成本数据</p>
          <p className="text-[12.5px] text-text-3">该时间范围内还没有产生用量</p>
        </div>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <StatCard
              label={`${activePreset.label}成本`}
              value={formatCurrency(aggregate.costMicros)}
            />
            <StatCard label="生成次数" value={formatCount(aggregate.generations)} unit="次" />
            <StatCard label="Token 用量" value={formatCount(aggregate.tokens)} />
          </div>

          {hasUnpriced && (
            <p className="text-[12px] text-text-3">
              部分模型未配置定价（<span className="text-amber">未定价</span>
              ），其用量已记录但未计入上方 ¥ 成本合计。
            </p>
          )}

          {projects != null && projects.length > 0 && (
            <section className="rounded-xl border border-line bg-bg-surface p-[18px]">
              <h2 className="mb-3 text-[11.5px] font-semibold tracking-[0.08em] text-text-3">
                按项目成本
              </h2>
              {projects.map((p, i) => (
                <BarRow
                  key={p.projectId}
                  label={p.projectName || p.projectId}
                  ratio={costRatio(p.costMicros, maxMicros)}
                  value={isUnpriced(p) ? <UnpricedBadge /> : formatCurrency(p.costMicros)}
                  color={BAR_COLORS[i % BAR_COLORS.length]}
                />
              ))}
            </section>
          )}

          {members != null && members.length > 0 && (
            <section className="rounded-xl border border-line bg-bg-surface p-[18px]">
              <h2 className="mb-3 text-[11.5px] font-semibold tracking-[0.08em] text-text-3">
                按成员成本
              </h2>
              {members.map((m, i) => (
                <BarRow
                  key={m.userId || "__unattributed__"}
                  label={m.email || m.userId || "未归属（历史）"}
                  ratio={costRatio(m.costMicros, maxMemberMicros)}
                  value={m.unpriced ? <UnpricedBadge /> : formatCurrency(m.costMicros)}
                  color={BAR_COLORS[i % BAR_COLORS.length]}
                />
              ))}
            </section>
          )}

          <GenerationsTable
            rows={generations ?? []}
            hasNextPage={hasNextPage}
            isFetchingNextPage={isFetchingNextPage}
            onLoadMore={onLoadMore}
          />
        </>
      )}
    </div>
  )
}

// 生成明细台账：时间 / 项目 / provider·model / 类型 / 用量 / 金额（金额右对齐 mono）
// + 「加载更多」（keyset 游标，next_cursor 空即到底，样式同资产库）。
function GenerationsTable({
  rows,
  hasNextPage,
  isFetchingNextPage,
  onLoadMore,
}: {
  rows: LedgerEntry[]
  hasNextPage: boolean
  isFetchingNextPage: boolean
  onLoadMore: () => void
}) {
  return (
    <section className="rounded-xl border border-line bg-bg-surface p-[18px]">
      <h2 className="mb-3 text-[11.5px] font-semibold tracking-[0.08em] text-text-3">
        生成明细
      </h2>
      {rows.length === 0 ? (
        <p className="py-6 text-center text-[12.5px] text-text-3">暂无生成记录</p>
      ) : (
        <Table className="min-w-[640px]">
          <TableHeader>
            <TableRow>
              <TableHead>时间</TableHead>
              <TableHead>项目</TableHead>
              <TableHead>Provider·Model</TableHead>
              <TableHead>类型</TableHead>
              <TableHead className="text-right">用量</TableHead>
              <TableHead className="text-right">金额</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((r) => (
              <TableRow key={r.id}>
                <TableCell className="font-mono text-text-2">
                  {formatTime(r.createdAt)}
                </TableCell>
                <TableCell className="text-text-1">
                  {r.projectName || r.projectId}
                </TableCell>
                <TableCell className="text-text-2">
                  {r.provider} · {r.model}
                </TableCell>
                <TableCell className="text-text-2">{r.kind}</TableCell>
                <TableCell className="text-right font-mono text-text-2">
                  {r.imageCount > 0 ? `${r.imageCount} 图` : formatCount(r.tokens)}
                </TableCell>
                <TableCell className="text-right font-mono text-text-1">
                  {isUnpriced(r) ? <UnpricedBadge /> : formatCurrency(r.costMicros)}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {hasNextPage && (
        <div className="mt-4 flex justify-center">
          <Button variant="ghost" onClick={onLoadMore} disabled={isFetchingNextPage}>
            {isFetchingNextPage ? "加载中…" : "加载更多"}
          </Button>
        </div>
      )}
    </section>
  )
}

// LedgerEntry.createdAt 是 RFC3339（Go time.Time JSON）。展示成本地短格式；解析失败回退原串。
function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  })
}
