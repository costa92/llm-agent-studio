import {
  useInfiniteQuery,
  type InfiniteData,
  type UseInfiniteQueryResult,
} from "@tanstack/react-query"
import { apiJSON } from "@/lib/apiClient"
import type { AuditRecord, ListEnvelope } from "@/lib/types"

// 审计流水：GET /api/orgs/{org}/audit-log?limit=&cursor= → {items, next_cursor}（admin, auditLogHandler）。
// useInfiniteQuery 按 next_cursor 累积：getNextPageParam 空串 → 停（同生成明细 useGenerations）。
export function useAuditLog(
  org: string,
  limit = 50,
): UseInfiniteQueryResult<InfiniteData<ListEnvelope<AuditRecord>>, Error> {
  return useInfiniteQuery({
    queryKey: ["audit-log", org, limit],
    queryFn: ({ pageParam }) =>
      apiJSON<ListEnvelope<AuditRecord>>(
        `/api/orgs/${org}/audit-log?limit=${limit}${
          pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""
        }`,
      ),
    initialPageParam: "" as string,
    getNextPageParam: (last) => (last.next_cursor ? last.next_cursor : undefined),
    enabled: org !== "",
  })
}
