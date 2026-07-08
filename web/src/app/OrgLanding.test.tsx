import { describe, it, expect, vi, afterEach } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
  RouterProvider,
} from "@tanstack/react-router"

const apiJSON = vi.fn()
vi.mock("@/lib/apiClient", () => ({ apiJSON: (...a: unknown[]) => apiJSON(...a) }))

// sonner toast is a no-op in jsdom
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }))

import { OrgLanding } from "./OrgLanding"

afterEach(() => {
  vi.clearAllMocks()
})

function renderLanding() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const rootRoute = createRootRoute({ component: OrgLanding })
  const projects = createRoute({
    getParentRoute: () => rootRoute,
    path: "/orgs/$org/projects",
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([projects]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router as never} />
    </QueryClientProvider>,
  )
  return router
}

describe("OrgLanding", () => {
  it("lists the user's orgs and navigates by id (not the typed name) on click", async () => {
    apiJSON.mockImplementation((path: string) => {
      if (path === "/api/orgs")
        return Promise.resolve({
          items: [
            { id: "org_hex_123", name: "AcmeCo", role: "org_admin" },
            { id: "org_hex_456", name: "Beta", role: "viewer" },
          ],
        })
      throw new Error("unexpected " + path)
    })
    const router = renderLanding()

    expect(await screen.findByText("AcmeCo")).toBeInTheDocument()
    expect(screen.getByText("Beta")).toBeInTheDocument()

    await userEvent.click(screen.getByText("AcmeCo"))
    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/orgs/org_hex_123/projects"),
    )
  })

  it("auto-enters the sole org (no selection click) when the user has exactly one", async () => {
    apiJSON.mockImplementation((path: string) => {
      if (path === "/api/orgs")
        return Promise.resolve({
          items: [{ id: "org_only_1", name: "SoloCo", role: "org_admin" }],
        })
      throw new Error("unexpected " + path)
    })
    const router = renderLanding()

    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/orgs/org_only_1/projects"),
    )
  })

  it("shows an empty state when the user has no orgs", async () => {
    apiJSON.mockResolvedValue({ items: [] })
    renderLanding()
    expect(await screen.findByText(/还没有组织/)).toBeInTheDocument()
  })

  it("creates an org and navigates to its projects by returned id", async () => {
    apiJSON.mockImplementation((path: string, init?: RequestInit) => {
      if (path === "/api/orgs" && (!init || init.method !== "POST"))
        return Promise.resolve({ items: [] })
      if (path === "/api/orgs" && init?.method === "POST")
        return Promise.resolve({ id: "new_org_789", name: "Created" })
      throw new Error("unexpected " + path)
    })
    const router = renderLanding()

    await userEvent.type(await screen.findByLabelText("新建组织"), "Created")
    await userEvent.click(screen.getByRole("button", { name: /创建/ }))
    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/orgs/new_org_789/projects"),
    )
  })
})
