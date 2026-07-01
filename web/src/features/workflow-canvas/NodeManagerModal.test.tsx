import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"

// 系统 tab 的内置目录 + 自定义 tab 的 CRUD 面板依赖，全部 mock 掉。
vi.mock("@/features/builtin-node-types/api", () => ({
  useBuiltinNodeTypes: () => ({
    data: [{ type: "script", label: "剧本", description: "把创意扩写成分镜脚本" }],
  }),
}))
vi.mock("@/features/custom-node-types/api", () => ({
  useCustomNodeTypes: () => ({ data: [], isLoading: false, isError: false, refetch: vi.fn() }),
  useCreateCustomNodeType: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }),
  useUpdateCustomNodeType: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }),
  useDeleteCustomNodeType: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }),
}))
vi.mock("@/features/org-secrets/api", () => ({ useOrgSecrets: () => ({ data: [] }) }))
vi.mock("@/features/cost/api", () => ({ useOrgTextModels: () => ({ data: [] }) }))
vi.mock("@/app/rbac", () => ({ useRole: () => ({ isAdmin: true, isLoading: false }) }))

import { NodeManagerModal } from "./NodeManagerModal"

describe("NodeManagerModal", () => {
  it("closed → 不渲染内容", () => {
    render(<NodeManagerModal open={false} onOpenChange={vi.fn()} org="acme" />)
    expect(screen.queryByText("节点管理")).toBeNull()
  })

  it("open → 默认「系统节点」tab 显示内置只读目录", () => {
    render(<NodeManagerModal open onOpenChange={vi.fn()} org="acme" />)
    expect(screen.getByText("节点管理")).toBeInTheDocument()
    expect(screen.getByRole("tab", { name: "系统节点" })).toHaveAttribute("aria-selected", "true")
    expect(screen.getByText("把创意扩写成分镜脚本")).toBeInTheDocument()
    expect(screen.getByText("内置")).toBeInTheDocument()
  })

  it("切到「用户自定义节点」tab → 显示自定义 CRUD 面板", async () => {
    const user = userEvent.setup()
    render(<NodeManagerModal open onOpenChange={vi.fn()} org="acme" />)
    await user.click(screen.getByRole("tab", { name: "用户自定义节点" }))
    expect(screen.getByRole("tab", { name: "用户自定义节点" })).toHaveAttribute("aria-selected", "true")
    expect(screen.getByText(/组织自定义节点/)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "新建自定义节点" })).toBeInTheDocument()
  })

  it("initialTab='custom' → 直接落在自定义 tab", () => {
    render(<NodeManagerModal open onOpenChange={vi.fn()} org="acme" initialTab="custom" />)
    expect(screen.getByRole("tab", { name: "用户自定义节点" })).toHaveAttribute("aria-selected", "true")
  })
})
