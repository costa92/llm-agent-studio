import { createFileRoute, Outlet } from "@tanstack/react-router"

export const Route = createFileRoute("/_authed/orgs/$org/projects/$id")({
  component: () => <Outlet />,
})
