import {
  useInfiniteQuery,
  type UseInfiniteQueryResult,
  type InfiniteData,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { LibraryFilter } from "./filter"
import { buildLibraryQuery } from "./filter"
import { nextCursorParam, type LibraryPage } from "./keyset"

// 每页拉取条数（libraryHandler limit；后端 store.go 默认 50、上限 100）。
const PAGE_LIMIT = 24

// 资产库：GET /api/orgs/{org}/assets?project=&type=&status=&style=&tag=&limit=&cursor=
//   → {items, next_cursor}（viewer+，keyset 分页，cursor=最后一个 asset id）。
// useInfiniteQuery 按 next_cursor 累积：getNextPageParam 空串 → 停。
// 过滤态进 queryKey —— 过滤变更 = 不同 key = 重置累积（不与旧页串接）。
export function useLibrary(
  org: string,
  filter: LibraryFilter,
): UseInfiniteQueryResult<InfiniteData<LibraryPage>, Error> {
  return useInfiniteQuery({
    queryKey: ["library", org, filter],
    queryFn: ({ pageParam }) => {
      const base = buildLibraryQuery(filter, PAGE_LIMIT)
      const qs = pageParam ? `${base}&cursor=${encodeURIComponent(pageParam)}` : base
      return apiJSON<LibraryPage>(`/api/orgs/${org}/assets?${qs}`)
    },
    initialPageParam: "" as string,
    getNextPageParam: nextCursorParam,
    enabled: org !== "",
  })
}
