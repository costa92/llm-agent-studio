import { createFileRoute } from "@tanstack/react-router"
import { useRole } from "@/app/rbac"
import { requireOrgParam } from "@/app/org"
import { AdminGate } from "@/features/cost/AdminGate"
import { StorageConfigView } from "@/features/storage/StorageConfigPage"

export const Route = createFileRoute("/_authed/orgs/$org/storage-config")({
	beforeLoad: ({ params }) => requireOrgParam(params),
	component: StorageConfigPage,
})

function StorageConfigPage() {
	const { org } = Route.useParams()
	const role = useRole(org)
	return (
		<AdminGate role={role}>
			<StorageConfigView org={org} />
		</AdminGate>
	)
}
