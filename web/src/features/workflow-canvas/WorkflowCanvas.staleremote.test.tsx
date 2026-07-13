import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { WorkflowCanvas } from "./WorkflowCanvas"

// 跨会话陈旧感知（读侧）：latestVersion 领先于编辑基线 version 时显示非破坏性 banner，
// 「重新加载」调 onReload（由路由层 bump remount key 实现）。

vi.mock("@/features/projects/workflowApi", () => ({
  useCreateWorkflow: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useUpdateWorkflow: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
}))

vi.mock("@/features/custom-node-types/api", () => ({
  useCustomNodeTypes: () => ({ data: [] }),
  useCreateCustomNodeType: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))

vi.mock("@/features/org-secrets/api", () => ({
  useOrgSecrets: () => ({ data: [] }),
}))

vi.mock("@/app/rbac", () => ({
  useRole: () => ({ role: "admin", isAdmin: true, canWrite: true, isLoading: false }),
}))

function renderCanvas(props: {
  version?: number
  latestVersion?: number
  onReload?: () => void
}) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <WorkflowCanvas
        workflowId="w1"
        projectId="p1"
        org="acme"
        workflowName="测试管线"
        nodes={[]}
        version={props.version}
        latestVersion={props.latestVersion}
        onReload={props.onReload}
      />
    </QueryClientProvider>,
  )
}

afterEach(() => vi.clearAllMocks())

describe("WorkflowCanvas 跨会话陈旧 banner", () => {
  it("latestVersion 领先于编辑基线 version 时显示 banner 且点「重新加载」调 onReload", async () => {
    const user = userEvent.setup()
    const onReload = vi.fn()
    renderCanvas({ version: 2, latestVersion: 3, onReload })
    expect(await screen.findByText(/已在其他会话中被修改/)).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: /重新加载/ }))
    expect(onReload).toHaveBeenCalledTimes(1)
  })

  it("latestVersion 等于编辑基线 version 时不显示 banner", () => {
    renderCanvas({ version: 2, latestVersion: 2, onReload: vi.fn() })
    expect(screen.queryByText(/已在其他会话中被修改/)).not.toBeInTheDocument()
  })

  it("latestVersion 为 undefined（加载中）时不显示 banner", () => {
    renderCanvas({ version: 2, latestVersion: undefined, onReload: vi.fn() })
    expect(screen.queryByText(/已在其他会话中被修改/)).not.toBeInTheDocument()
  })
})
