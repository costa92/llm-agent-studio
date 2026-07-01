import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { CustomNodeType, LlmParams } from "@/lib/types"

// 复用与 CustomNodeTypeManager.test 相同的 api/依赖 mock（面板与路由页共享 useCustomNodeTypeCrud）。
const queryState = {
  value: { data: [] as CustomNodeType[], isLoading: false, isError: false, refetch: vi.fn() },
}

vi.mock("./api", () => ({
  useCustomNodeTypes: () => queryState.value,
  useCreateCustomNodeType: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }),
  useUpdateCustomNodeType: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }),
  useDeleteCustomNodeType: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }),
}))
vi.mock("@/features/org-secrets/api", () => ({ useOrgSecrets: () => ({ data: [] }) }))
vi.mock("@/features/cost/api", () => ({ useOrgTextModels: () => ({ data: [] }) }))
vi.mock("@/app/rbac", () => ({ useRole: () => ({ isAdmin: true, isLoading: false }) }))

import { CustomNodeTypesPanel } from "./CustomNodeTypesPanel"

const LLM_PARAMS: LlmParams = { userPrompt: "翻译：{{text}}", outputFormat: "text" }
const TYPE_A: CustomNodeType = {
  id: "cnt-1", orgId: "acme", slug: "translator", label: "翻译助手",
  color: "#7c93ff", kind: "llm", params: LLM_PARAMS,
}

afterEach(() => {
  queryState.value = { data: [], isLoading: false, isError: false, refetch: vi.fn() }
})

describe("CustomNodeTypesPanel", () => {
  it("空态渲染工具条 hint + 新建按钮 + 空提示", () => {
    render(<CustomNodeTypesPanel org="acme" />)
    expect(screen.getByText(/组织自定义节点/)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "新建自定义节点" })).toBeInTheDocument()
    expect(screen.getByText(/暂无自定义节点类型/)).toBeInTheDocument()
  })

  it("有数据 → 渲染行 + 编辑/删除操作", () => {
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    render(<CustomNodeTypesPanel org="acme" />)
    expect(screen.getByText("翻译助手")).toBeInTheDocument()
    expect(screen.getByText("translator")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "编辑" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "删除" })).toBeInTheDocument()
  })

  it("点「新建自定义节点」打开创建对话框", async () => {
    const user = userEvent.setup()
    render(<CustomNodeTypesPanel org="acme" />)
    await user.click(screen.getByRole("button", { name: "新建自定义节点" }))
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })
})
