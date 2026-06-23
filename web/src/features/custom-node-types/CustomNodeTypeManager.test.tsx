import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "sonner"
import type { CustomNodeType, LlmParams } from "@/lib/types"
import { ApiError } from "@/lib/apiClient"

// --- mock api hooks -------------------------------------------------------
const createMutateAsync = vi.fn()
const updateMutateAsync = vi.fn()
const deleteMutateAsync = vi.fn()

const queryState = {
  value: {
    data: [] as CustomNodeType[],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  },
}

vi.mock("./api", () => ({
  useCustomNodeTypes: () => queryState.value,
  useCreateCustomNodeType: () => ({
    mutate: vi.fn(),
    mutateAsync: createMutateAsync,
    isPending: false,
  }),
  useUpdateCustomNodeType: () => ({
    mutate: vi.fn(),
    mutateAsync: updateMutateAsync,
    isPending: false,
  }),
  useDeleteCustomNodeType: () => ({
    mutate: vi.fn(),
    mutateAsync: deleteMutateAsync,
    isPending: false,
  }),
}))

import { CustomNodeTypeManager } from "./CustomNodeTypeManager"

const LLM_PARAMS: LlmParams = { userPrompt: "翻译：{{text}}", outputFormat: "text" }

const TYPE_A: CustomNodeType = {
  id: "cnt-1",
  orgId: "acme",
  slug: "translator",
  label: "翻译助手",
  color: "#7c93ff",
  kind: "llm",
  params: LLM_PARAMS,
}

const TYPE_B: CustomNodeType = {
  id: "cnt-2",
  orgId: "acme",
  slug: "summarizer",
  label: "摘要生成",
  color: "#22b8a6",
  kind: "llm",
  params: { userPrompt: "摘要：{{content}}" },
}

function renderManager() {
  return render(
    <>
      <CustomNodeTypeManager org="acme" />
      <Toaster />
    </>,
  )
}

beforeEach(() => {
  queryState.value = {
    data: [],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }
  createMutateAsync.mockResolvedValue(TYPE_A)
  updateMutateAsync.mockResolvedValue(TYPE_A)
  deleteMutateAsync.mockResolvedValue({ ok: true })
})

afterEach(() => {
  vi.clearAllMocks()
})

describe("CustomNodeTypeManager", () => {
  it("renders type rows from useCustomNodeTypes", () => {
    queryState.value = { data: [TYPE_A, TYPE_B], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()
    expect(screen.getByText("翻译助手")).toBeInTheDocument()
    expect(screen.getByText("摘要生成")).toBeInTheDocument()
  })

  it("shows empty state when there are no types", () => {
    renderManager()
    expect(screen.getByText(/暂无自定义节点类型/)).toBeInTheDocument()
  })

  it("clicking 新建类型 opens dialog with kind fixed to llm", async () => {
    const user = userEvent.setup()
    renderManager()
    await user.click(screen.getByRole("button", { name: /新建类型/ }))
    // 对话框应出现
    expect(screen.getByRole("dialog")).toBeInTheDocument()
    // kind 固定显示 llm（禁用 Input）
    const kindInput = screen.getByLabelText("kind")
    expect(kindInput).toHaveValue("llm")
    expect(kindInput).toBeDisabled()
  })

  it("submitting 新建 calls createMutation.mutateAsync with {label, color, kind:'llm', params}", async () => {
    const user = userEvent.setup()
    renderManager()
    await user.click(screen.getByRole("button", { name: /新建类型/ }))

    // 填写 label
    await user.clear(screen.getByLabelText(/名称/))
    await user.type(screen.getByLabelText(/名称/), "测试类型")

    // 填写必填 userPrompt（通过 LlmParamForm 的 label）
    const userPromptTa = screen.getByLabelText(/用户提示词/)
    await user.clear(userPromptTa)
    await user.type(userPromptTa, "请处理：{{input}}")

    // 提交
    await user.click(screen.getByRole("button", { name: /创建/ }))

    await waitFor(() => {
      expect(createMutateAsync).toHaveBeenCalledTimes(1)
    })

    const call = createMutateAsync.mock.calls[0][0]
    expect(call.label).toBe("测试类型")
    expect(call.kind).toBe("llm")
    expect(call.color).toBeTruthy()
    expect(call.params.userPrompt).toContain("input")
  })

  it("clicking 编辑 opens dialog prefilled with the target type", async () => {
    const user = userEvent.setup()
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    // 打开行菜单并点击编辑
    const editBtn = screen.getByRole("button", { name: /编辑/ })
    await user.click(editBtn)

    expect(screen.getByRole("dialog")).toBeInTheDocument()
    expect(screen.getByLabelText(/名称/)).toHaveValue("翻译助手")
  })

  it("clicking 删除 calls deleteMutation.mutateAsync", async () => {
    const user = userEvent.setup()
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    await user.click(screen.getByRole("button", { name: /删除/ }))
    // 确认对话框出现
    const confirmBtn = screen.getByRole("button", { name: /确认删除/ })
    await user.click(confirmBtn)

    await waitFor(() => {
      expect(deleteMutateAsync).toHaveBeenCalledWith("cnt-1")
    })
  })

  it("a 409 on delete surfaces an in-use error message", async () => {
    const user = userEvent.setup()
    deleteMutateAsync.mockRejectedValue(
      new ApiError(409, "该类型已被使用"),
    )
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    await user.click(screen.getByRole("button", { name: /删除/ }))
    await user.click(screen.getByRole("button", { name: /确认删除/ }))

    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument()
    })
    expect(screen.getByRole("alert").textContent).toMatch(/引用/)
  })
})
