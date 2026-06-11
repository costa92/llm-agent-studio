import type { Asset, ListEnvelope } from "@/lib/types"

// 资产库 keyset 分页累积（libraryHandler：cursor=最后一个 asset id，next_cursor 为空即到底）。
// 后端语义（已核实 store.go:225-281）：
//   - 每页按 a.id ASC 取 limit 条；
//   - 返回 next_cursor = 末条 id（仅当本页满 limit），否则空串。
// 故下一页参数 = 上一页 next_cursor；空串 → 无下一页（停止累积）。

// useInfiniteQuery 的页类型 = 后端列表信封。
export type LibraryPage = ListEnvelope<Asset>

// getNextPageParam：空 next_cursor → undefined（TanStack 据此判定 hasNextPage=false）。
// 入参是 useInfiniteQuery 透传的最后一页信封。
export function nextCursorParam(lastPage: LibraryPage): string | undefined {
  return lastPage.next_cursor ? lastPage.next_cursor : undefined
}

// 把多页信封串接成单个有序资产数组（去重防御：后端 keyset 严格递增本不会重，
// 但重连/缓存边界保守按 id 去重，避免网格出现重复 key）。
export function flattenPages(pages: LibraryPage[] | undefined): Asset[] {
  if (!pages) return []
  const seen = new Set<string>()
  const out: Asset[] = []
  for (const page of pages) {
    for (const asset of page.items) {
      if (seen.has(asset.id)) continue
      seen.add(asset.id)
      out.push(asset)
    }
  }
  return out
}
