import { createFileRoute } from "@tanstack/react-router"
import { useShots } from "@/features/workflow/api"
import { StoryboardView } from "@/features/workflow/StoryboardView"

// T10：分镜栅格。GET /api/projects/{id}/shots → {items}。
export const Route = createFileRoute("/_authed/projects/$id/storyboard")({
  component: StoryboardPage,
})

function StoryboardPage() {
  const { id } = Route.useParams()
  const query = useShots(id)
  return (
    <StoryboardView
      shots={query.data}
      isLoading={query.isLoading}
      isError={query.isError}
    />
  )
}
