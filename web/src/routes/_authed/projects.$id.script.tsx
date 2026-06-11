import { createFileRoute } from "@tanstack/react-router"
import { useScript } from "@/features/workflow/api"
import { ScriptView } from "@/features/workflow/ScriptView"

// T10：剧本视图。GET /api/projects/{id}/script（裸 JSON，非 {items}）；zod 容错解析。
export const Route = createFileRoute("/_authed/projects/$id/script")({
  component: ScriptPage,
})

function ScriptPage() {
  const { id } = Route.useParams()
  const query = useScript(id)
  return (
    <ScriptView
      script={query.data}
      isLoading={query.isLoading}
      isError={query.isError}
    />
  )
}
