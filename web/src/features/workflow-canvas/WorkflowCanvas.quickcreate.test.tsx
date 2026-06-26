import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "sonner"
import { WorkflowCanvas } from "./WorkflowCanvas"
import type { CustomNodeType, WorkflowNode } from "@/lib/types"

vi.mock("@/features/projects/workflowApi", () => ({
  useCreateWorkflow: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useUpdateWorkflow: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
}))

// 内置类型目录（4 个创作内置）。
vi.mock("@/features/builtin-node-types/api", () => ({
  useBuiltinNodeTypes: vi.fn(() => ({
    data: [
      { type: "script", label: "剧本", description: "" },
      { type: "storyboard", label: "分镜", description: "" },
    ],
  })),
}))

// org 注册表 typed 类型 + 创建 mutation。快建 chip → 对话框 → 提交走 create。
const createMutateAsync = vi.fn()
const typedTypes = { value: [] as CustomNodeType[] }
vi.mock("@/features/custom-node-types/api", () => ({
  useCustomNodeTypes: () => ({ data: typedTypes.value }),
  useCreateCustomNodeType: () => ({ mutateAsync: createMutateAsync, isPending: false }),
}))

// org 密钥 + 角色（http secret-bearing 守卫依赖；默认 admin、无密钥）。
vi.mock("@/features/org-secrets/api", () => ({
  useOrgSecrets: () => ({ data: [] }),
}))
vi.mock("@/app/rbac", () => ({
  useRole: () => ({ isAdmin: true, isLoading: false }),
}))

function renderCanvas(nodes: WorkflowNode[] = []) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <WorkflowCanvas
        workflowId="w1"
        projectId="p1"
        org="acme"
        workflowName="测试管线"
        nodes={nodes}
      />
      <Toaster />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  typedTypes.value = []
  createMutateAsync.mockResolvedValue({
    id: "cnt-new",
    orgId: "acme",
    slug: "weather",
    label: "天气查询",
    color: "#7c93ff",
    kind: "http",
    params: { method: "GET", url: "https://api.weather.com", headers: {} },
  })
})

afterEach(() => vi.clearAllMocks())

describe("WorkflowCanvas quick-create", () => {
  it("clicking 「+ HTTP 节点」 opens the typed-create dialog seeded with kind=http", async () => {
    const user = userEvent.setup()
    renderCanvas()
    await user.click(await screen.findByRole("button", { name: /HTTP 节点/ }))
    expect(await screen.findByRole("dialog")).toBeInTheDocument()
    expect(screen.getByLabelText("kind")).toHaveValue("http")
  })

  it("clicking 「+ LLM 节点」 seeds kind=llm", async () => {
    const user = userEvent.setup()
    renderCanvas()
    await user.click(await screen.findByRole("button", { name: /LLM 节点/ }))
    expect(screen.getByLabelText("kind")).toHaveValue("llm")
  })

  it("submitting the seeded dialog calls useCreateCustomNodeType.mutateAsync", async () => {
    const user = userEvent.setup()
    renderCanvas()
    await user.click(await screen.findByRole("button", { name: /HTTP 节点/ }))
    await user.type(screen.getByLabelText(/名称/), "天气查询")
    await user.type(screen.getByLabelText(/URL/), "https://api.weather.com/v1")
    await user.click(screen.getByRole("button", { name: /创建/ }))
    await waitFor(() => expect(createMutateAsync).toHaveBeenCalledTimes(1))
    const call = createMutateAsync.mock.calls[0][0]
    expect(call.kind).toBe("http")
    expect(call.label).toBe("天气查询")
  })

  it("surfaces a 409 slug collision in the dialog instead of dead-ending", async () => {
    const user = userEvent.setup()
    const { ApiError } = await import("@/lib/apiClient")
    createMutateAsync.mockRejectedValue(new ApiError(409, "slug conflict"))
    renderCanvas()
    await user.click(await screen.findByRole("button", { name: /HTTP 节点/ }))
    await user.type(screen.getByLabelText(/名称/), "天气查询")
    await user.type(screen.getByLabelText(/URL/), "https://api.weather.com/v1")
    await user.click(screen.getByRole("button", { name: /创建/ }))
    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument()
    })
    expect(screen.getByRole("alert").textContent).toMatch(/已被占用|slug/)
    // 对话框仍打开（未 dead-end）。
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })
})
