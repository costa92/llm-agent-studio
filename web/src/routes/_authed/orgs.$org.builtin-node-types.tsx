import { createFileRoute } from "@tanstack/react-router"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { BuiltinNodeTypeList } from "@/features/builtin-node-types/BuiltinNodeTypeList"

export const Route = createFileRoute("/_authed/orgs/$org/builtin-node-types")({
	beforeLoad: ({ params }) => requireOrgParam(params),
	component: BuiltinNodeTypePage,
})

function BuiltinNodeTypePage() {
	const { org } = Route.useParams()
	const role = useRole(org)
	return (
		<AdminGate role={role}>
			<BuiltinNodeTypeList />
		</AdminGate>
	)
}
