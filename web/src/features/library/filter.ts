// 资产库过滤态 + query-string 构造。
// 后端 libraryHandler（已核实 m2handlers.go:166-177）只解析这些 query 参数：
//   project / type / status / style / tag / limit / cursor
// —— 不得发后端不认的过滤项。空值不下发（store.go 对空字符串过滤项跳过 add）。

import type { Asset } from "@/lib/types"

// typed search params 持有的过滤态（路由 ?type=&status=&style=&project=&tag=）。
// 全部可选；缺省 = 不过滤。
export interface LibraryFilter {
  type?: string
  status?: string
  style?: string
  project?: string
  tag?: string
}

// 把过滤态拼成 libraryHandler 的 query string（含可选 limit）。
// 仅下发非空字段；cursor 由 useInfiniteQuery 在 queryFn 里另行追加。
export function buildLibraryQuery(filter: LibraryFilter, limit?: number): string {
  const params = new URLSearchParams()
  if (filter.project) params.set("project", filter.project)
  if (filter.type) params.set("type", filter.type)
  if (filter.status) params.set("status", filter.status)
  if (filter.style) params.set("style", filter.style)
  if (filter.tag) params.set("tag", filter.tag)
  if (limit != null) params.set("limit", String(limit))
  return params.toString()
}

// 资产类型选项（store.go a.type）。图片一期可筛；视频/音频二期（生成后端驱动）。
export interface TypeOption {
  value: Asset["type"]
  label: string
  disabled?: boolean
}

export const TYPE_OPTIONS: TypeOption[] = [
  { value: "image", label: "图片" },
  { value: "video", label: "视频", disabled: true },
  { value: "audio", label: "音频", disabled: true },
]

// 状态过滤选项（asset.status：accepted/pending_acceptance/rejected —— 见 review.go / worker.go）。
export interface StatusOption {
  value: string
  label: string
}

export const STATUS_OPTIONS: StatusOption[] = [
  { value: "accepted", label: "已采纳" },
  { value: "pending_acceptance", label: "待审核" },
  { value: "rejected", label: "已退回" },
]
