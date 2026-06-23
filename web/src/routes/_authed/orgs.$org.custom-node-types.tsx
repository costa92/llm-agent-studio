import { createFileRoute } from "@tanstack/react-router"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { CustomNodeTypeManager } from "@/features/custom-node-types/CustomNodeTypeManager"

export const Route = createFileRoute("/_authed/orgs/$org/custom-node-types")({
	beforeLoad: ({ params }) => requireOrgParam(params),
	component: CustomNodeTypePage,
})

function CustomNodeTypePage() {
	const { org } = Route.useParams()
	const role = useRole(org)
	return (
		<AdminGate role={role}>
			<CustomNodeTypeManager org={org} />
		</AdminGate>
	)
}
