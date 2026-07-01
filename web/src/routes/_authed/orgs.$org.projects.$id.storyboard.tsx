import { createFileRoute } from "@tanstack/react-router"
import { useShots, useProjectAssets } from "@/features/workflow/api"
import { imageAssetIdByShotId } from "@/features/workflow/storyboardAssets"
import { StoryboardView } from "@/features/workflow/StoryboardView"

// T10：分镜栅格。GET /api/projects/{id}/shots → {items}。
export const Route = createFileRoute("/_authed/orgs/$org/projects/$id/storyboard")({
  component: StoryboardPage,
})

function StoryboardPage() {
  const { id } = Route.useParams()
  const query = useShots(id)
  // 资产可渐进加载：isLoading 仅门控 shots 查询，插图由 AssetThumb 自持加载态。
  const assets = useProjectAssets(id)
  const illustrationByShotId = imageAssetIdByShotId(assets.data ?? [])
  return (
    <StoryboardView
      shots={query.data}
      isLoading={query.isLoading}
      isError={query.isError}
      illustrationByShotId={illustrationByShotId}
    />
  )
}
