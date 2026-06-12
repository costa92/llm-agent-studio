// 相对时间文案（任务行 lastActivityAt）。lib/ 现无同类 helper，故就近新增。
// now 可注入便于确定性单测；缺省取 Date.now()。
// 阈值：<1分钟→刚刚；<1小时→N分钟前；<24小时→N小时前；<30天→N天前；否则回退本地日期。
export function formatRelative(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return iso

  const diffMs = now - t
  if (diffMs < 0) return "刚刚"

  const minute = 60 * 1000
  const hour = 60 * minute
  const day = 24 * hour

  if (diffMs < minute) return "刚刚"
  if (diffMs < hour) return `${Math.floor(diffMs / minute)}分钟前`
  if (diffMs < day) return `${Math.floor(diffMs / hour)}小时前`
  if (diffMs < 30 * day) return `${Math.floor(diffMs / day)}天前`

  return new Date(t).toLocaleDateString("zh-CN")
}
