import { createFileRoute } from "@tanstack/react-router"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { OrgSecretManager } from "@/features/org-secrets/OrgSecretManager"

export const Route = createFileRoute("/_authed/orgs/$org/secrets")({
	beforeLoad: ({ params }) => requireOrgParam(params),
	component: OrgSecretsPage,
})

function OrgSecretsPage() {
	const { org } = Route.useParams()
	const role = useRole(org)
	return (
		<AdminGate role={role}>
			<OrgSecretManager org={org} />
		</AdminGate>
	)
}
