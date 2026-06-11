import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { routeTree } from "@/routeTree.gen"
import { AuthProvider } from "@/app/auth"

function renderRoute(path: string) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  const queryClient = new QueryClient()

  return render(
    <AuthProvider>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </AuthProvider>,
  )
}

describe("root not found handling", () => {
  it("renders the org landing for empty org paths", async () => {
    renderRoute("/orgs//projects")

    expect(await screen.findByText("进入组织")).toBeInTheDocument()
    expect(screen.queryByText("Not Found")).not.toBeInTheDocument()
  })
})
