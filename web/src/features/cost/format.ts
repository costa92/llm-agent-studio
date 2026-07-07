// 成本展示工具：costMicros（百万分之一货币单位的整数）换算为可读货币，
// 时间范围预设转 RFC3339 from/to（后端 parseTimeRange 读 ?from/?to，畸形 → 400）。
// 来源：cost/store.go Aggregate.CostMicros (int64) + m2handlers.go parseTimeRange。

import type { LedgerEntry } from "@/lib/types"

// costMicros = 货币 × 1_000_000。后端以 micros 存（避免浮点累计误差），
// 前端除以 1e6 还原。默认人民币（¥），保留两位小数。
const MICROS_PER_UNIT = 1_000_000

export function formatCurrency(costMicros: number, symbol = "¥"): string {
  const units = costMicros / MICROS_PER_UNIT
  return `${symbol}${units.toLocaleString("zh-CN", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`
}

// 大数字加千分位（生成次数 / token 用量）。
export function formatCount(n: number): string {
  return n.toLocaleString("zh-CN")
}

// 时间范围预设 —— UI 下拉项。value 是相对当前的"近 N 天"；all = 不限（不带 from/to）。
export interface RangePreset {
  value: string
  label: string
  days: number | null
}

export const RANGE_PRESETS: RangePreset[] = [
  { value: "7d", label: "近 7 天", days: 7 },
  { value: "30d", label: "近 30 天", days: 30 },
  { value: "90d", label: "近 90 天", days: 90 },
  { value: "all", label: "全部时间", days: null },
]

// 把预设转成后端 from/to（RFC3339）。days=null → 不限范围（两者皆 undefined）。
// to = now，from = now - days。注入 now 便于测试稳定。
export interface TimeRange {
  from?: string
  to?: string
}

export function rangeToParams(preset: RangePreset, now: Date = new Date()): TimeRange {
  if (preset.days == null) return {}
  const from = new Date(now.getTime() - preset.days * 24 * 60 * 60 * 1000)
  return { from: from.toISOString(), to: now.toISOString() }
}

// 按 costMicros 占比计算条形比例（BarRow ratio 0..1）。最大值为分母；全 0 → 0。
export function costRatio(costMicros: number, maxMicros: number): number {
  if (maxMicros <= 0) return 0
  return costMicros / maxMicros
}

// 有真实用量（token / 图数 / 生成次数任一 > 0）却计费为 ¥0：说明该
// (provider×model) 没有 pricing 定价行（RecordPriced 记 cost_micros=0），真实花费被
// 漏计。据此在成本页 / 运行汇总标「未定价」，避免静默读成免费而误导抬头成本。
export function isUnpriced(m: {
  costMicros: number
  tokens: number
  imageCount?: number
  generations?: number
}): boolean {
  return (
    m.costMicros === 0 &&
    (m.tokens > 0 || (m.imageCount ?? 0) > 0 || (m.generations ?? 0) > 0)
  )
}

// 生成明细端点不读 from/to（全 org 全量 keyset）；导出时前端按活动范围的 createdAt
// 裁剪到 [from, to)。range 为空（全部时间）→ 原样返回。createdAt 解析失败不丢弃。
export function filterLedgerByRange(
  rows: LedgerEntry[],
  range: TimeRange,
): LedgerEntry[] {
  const fromMs = range.from ? Date.parse(range.from) : undefined
  const toMs = range.to ? Date.parse(range.to) : undefined
  if (fromMs === undefined && toMs === undefined) return rows
  return rows.filter((r) => {
    const t = Date.parse(r.createdAt)
    if (Number.isNaN(t)) return true
    if (fromMs !== undefined && t < fromMs) return false
    if (toMs !== undefined && t >= toMs) return false
    return true
  })
}

// CSV 单元格转义：含逗号 / 引号 / 换行 → 整体包双引号并把内部引号翻倍（RFC 4180）。
function csvCell(v: string): string {
  return /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v
}

// 生成明细 → CSV 字符串（无依赖，手工拼接 + 转义）。列同台账：
// 时间 / 项目 / Provider·Model / 类型 / 用量 / 金额(¥)。空行集 → 仅表头。
// 金额：未定价（有用量却计费 0）标「未定价」，其余为货币单位数（两位小数，
// 不加千分位以免逗号破坏 CSV）。时间用 createdAt 原始 RFC3339（无歧义，随表格排序）。
export function ledgerToCSV(rows: LedgerEntry[]): string {
  const header = ["时间", "项目", "Provider·Model", "类型", "用量", "金额(¥)"]
  const lines = [header.join(",")]
  for (const r of rows) {
    const usage = r.imageCount > 0 ? `${r.imageCount} 图` : `${r.tokens} tok`
    const amount = isUnpriced(r)
      ? "未定价"
      : (r.costMicros / MICROS_PER_UNIT).toFixed(2)
    lines.push(
      [
        r.createdAt,
        r.projectName || r.projectId,
        `${r.provider} · ${r.model}`,
        r.kind,
        usage,
        amount,
      ]
        .map(csvCell)
        .join(","),
    )
  }
  return lines.join("\n")
}
