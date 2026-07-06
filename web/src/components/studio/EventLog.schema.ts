import type { LogLine, StageId } from "@/lib/timeline"

// 分组小标题（emphasis S1–S4 → 中文阶段名）。
export const EMPHASIS_TITLE: Record<StageId, string> = {
  S1: "规划",
  S2: "剧本",
  S3: "分镜",
  S4: "素材",
}

export interface LogGroup {
  emphasis: StageId | "other"
  lines: LogLine[]
}

// 按 emphasis 分组，保持首次出现顺序；组内按 seq 升序。无 emphasis 归入 "other"。
export function groupByEmphasis(lines: LogLine[]): LogGroup[] {
  const order: (StageId | "other")[] = []
  const buckets = new Map<StageId | "other", LogLine[]>()
  for (const line of [...lines].sort((a, b) => a.seq - b.seq)) {
    const key: StageId | "other" = line.emphasis ?? "other"
    if (!buckets.has(key)) {
      buckets.set(key, [])
      order.push(key)
    }
    buckets.get(key)!.push(line)
  }
  return order.map((emphasis) => ({ emphasis, lines: buckets.get(emphasis)! }))
}

// 折叠态「最新动态」：最后一条（seq 最大）的原始文本 + 总条数。
// 注：展开分组内改用 friendlyLabel 降噪；折叠摘要保留 logFor 生成的已部分友好化文本（含 todo 类型等细节）。
export function latestSummary(lines: LogLine[]): { text: string; count: number } | null {
  if (lines.length === 0) return null
  const last = lines.reduce((a, b) => (b.seq > a.seq ? b : a))
  return { text: last.text, count: lines.length }
}
