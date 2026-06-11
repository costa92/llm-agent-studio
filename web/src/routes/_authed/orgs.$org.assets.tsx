import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { z } from "zod"
import { LibraryView } from "@/features/library/LibraryPage"
import { useLibrary } from "@/features/library/api"
import { useAsset } from "@/features/review/api"
import { useProjects, usePromptStyles } from "@/features/projects/api"
import { flattenPages } from "@/features/library/keyset"
import type { LibraryFilter } from "@/features/library/filter"

// T12：资产库（过滤 + keyset 分页 + 版本血缘）。
// typed search params 持有过滤态（type/status/style/project/tag）+ ?asset= 控制详情 Drawer。
const librarySearchSchema = z.object({
  type: z.string().optional(),
  status: z.string().optional(),
  style: z.string().optional(),
  project: z.string().optional(),
  tag: z.string().optional(),
  asset: z.string().optional(),
})

export const Route = createFileRoute("/_authed/orgs/$org/assets")({
  validateSearch: librarySearchSchema,
  component: AssetsPage,
})

function AssetsPage() {
  const { org } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate()

  const filter: LibraryFilter = {
    type: search.type,
    status: search.status,
    style: search.style,
    project: search.project,
    tag: search.tag,
  }
  const selectedId = search.asset ?? null

  const library = useLibrary(org, filter)
  const projects = useProjects(org)
  const styles = usePromptStyles()
  const detail = useAsset(selectedId ?? "")

  const assets = flattenPages(library.data?.pages)

  // 更新过滤态（保留当前选中资产）。
  function setFilter(next: LibraryFilter): void {
    void navigate({
      to: "/orgs/$org/assets",
      params: { org },
      search: { ...next, asset: search.asset },
    })
  }

  // 更新 ?asset=（null = 关闭 Drawer，保留过滤态）。
  function selectAsset(id: string | null): void {
    void navigate({
      to: "/orgs/$org/assets",
      params: { org },
      search: { ...filter, asset: id ?? undefined },
    })
  }

  return (
    <LibraryView
      assets={assets}
      isLoading={library.isLoading}
      isError={library.isError}
      onRetry={() => void library.refetch()}
      hasNextPage={library.hasNextPage}
      isFetchingNextPage={library.isFetchingNextPage}
      onLoadMore={() => void library.fetchNextPage()}
      filter={filter}
      onFilterChange={setFilter}
      projects={projects.data ?? []}
      styles={styles.data ?? []}
      selectedId={selectedId}
      onSelect={selectAsset}
      detail={detail.data}
      detailLoading={selectedId != null && detail.isLoading}
    />
  )
}
