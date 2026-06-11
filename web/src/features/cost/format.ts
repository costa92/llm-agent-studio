// 成本展示工具：costMicros（百万分之一货币单位的整数）换算为可读货币，
// 时间范围预设转 RFC3339 from/to（后端 parseTimeRange 读 ?from/?to，畸形 → 400）。
// 来源：cost/store.go Aggregate.CostMicros (int64) + m2handlers.go parseTimeRange。

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
