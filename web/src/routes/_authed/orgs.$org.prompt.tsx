import { createFileRoute } from "@tanstack/react-router"
import { PromptListPage } from "@/features/prompt/PromptListPage"
import { requireOrgParam } from "@/app/org"

// T14：Prompt Management List（auth-only，管理提示词列表 + 风格附加）。
export const Route = createFileRoute("/_authed/orgs/$org/prompt")({
  beforeLoad: ({ params }) => requireOrgParam(params),
  component: PromptPage,
})

function PromptPage() {
  const { org } = Route.useParams()
  return <PromptListPage org={org} />
}
