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

  // ─── 成功 toast ──────────────────────────────────────────────────────────────

  it("shows 已创建 toast after successful create", async () => {
    const user = userEvent.setup()
    renderManager()
    await user.click(screen.getByRole("button", { name: /新建类型/ }))

    await user.clear(screen.getByLabelText(/名称/))
    await user.type(screen.getByLabelText(/名称/), "测试类型")
    await user.clear(screen.getByLabelText(/用户提示词/))
    await user.type(screen.getByLabelText(/用户提示词/), "请处理：{{input}}")
    await user.click(screen.getByRole("button", { name: /创建/ }))

    await waitFor(() => {
      expect(screen.getByText(/已创建/)).toBeInTheDocument()
    })
  })

  it("shows 已更新 toast after successful update", async () => {
    const user = userEvent.setup()
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    await user.click(screen.getByRole("button", { name: /编辑/ }))
    await user.clear(screen.getByLabelText(/名称/))
    await user.type(screen.getByLabelText(/名称/), "翻译助手v2")
    await user.click(screen.getByRole("button", { name: /保存/ }))

    await waitFor(() => {
      expect(screen.getByText(/已更新/)).toBeInTheDocument()
    })
  })

  it("shows 已删除 toast after successful delete", async () => {
    const user = userEvent.setup()
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    await user.click(screen.getByRole("button", { name: /删除/ }))
    await user.click(screen.getByRole("button", { name: /确认删除/ }))

    await waitFor(() => {
      expect(screen.getByText(/已删除/)).toBeInTheDocument()
    })
  })

  // ─── 409 错误 ─────────────────────────────────────────────────────────────────

  it("a 409 on delete toasts the in-use error message", async () => {
    const user = userEvent.setup()
    deleteMutateAsync.mockRejectedValue(new ApiError(409, "该类型已被使用"))
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    await user.click(screen.getByRole("button", { name: /删除/ }))
    await user.click(screen.getByRole("button", { name: /确认删除/ }))

    // useCrudResource.confirmDelete 把 delete 错误 toast.error()（而非内联 alert）。
    // 匹配 toast 中「请先移除引用」子串，避免与页面描述文字（"移除所有引用节点"）冲突。
    await waitFor(() => {
      expect(screen.getByText(/请先移除引用/)).toBeInTheDocument()
    })
  })

  it("a 409 on create surfaces the 名称或slug 已被占用 error in the dialog", async () => {
    const user = userEvent.setup()
    createMutateAsync.mockRejectedValue(new ApiError(409, "slug conflict"))
    renderManager()

    await user.click(screen.getByRole("button", { name: /新建类型/ }))
    await user.clear(screen.getByLabelText(/名称/))
    await user.type(screen.getByLabelText(/名称/), "冲突类型")
    await user.clear(screen.getByLabelText(/用户提示词/))
    await user.type(screen.getByLabelText(/用户提示词/), "提示词")
    await user.click(screen.getByRole("button", { name: /创建/ }))

    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument()
    })
    expect(screen.getByRole("alert").textContent).toMatch(/slug 已被占用/)
  })

  it("a 409 on update surfaces the 名称或slug 已被占用 error in the dialog", async () => {
    const user = userEvent.setup()
    updateMutateAsync.mockRejectedValue(new ApiError(409, "slug conflict"))
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    await user.click(screen.getByRole("button", { name: /编辑/ }))
    await user.clear(screen.getByLabelText(/名称/))
    await user.type(screen.getByLabelText(/名称/), "冲突名称")
    await user.click(screen.getByRole("button", { name: /保存/ }))

    await waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument()
    })
    expect(screen.getByRole("alert").textContent).toMatch(/slug 已被占用/)
  })

  // ─── 对话框重新挂载（key prop 防陈旧状态）────────────────────────────────────

  it("editing type A then type B prefills B (not A) — key remount", async () => {
    const user = userEvent.setup()
    queryState.value = { data: [TYPE_A, TYPE_B], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    // 编辑 A
    const [editA] = screen.getAllByRole("button", { name: /编辑/ })
    await user.click(editA)
    expect(screen.getByLabelText(/名称/)).toHaveValue("翻译助手")
    // 关闭
    await user.click(screen.getByRole("button", { name: /取消/ }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())

    // 编辑 B
    const editBtns = screen.getAllByRole("button", { name: /编辑/ })
    await user.click(editBtns[1])
    // 应预填 B 的 label，而非陈旧的 A
    await waitFor(() => {
      expect(screen.getByLabelText(/名称/)).toHaveValue("摘要生成")
    })
  })

  it("opening create dialog after an edit shows an empty form", async () => {
    const user = userEvent.setup()
    queryState.value = { data: [TYPE_A], isLoading: false, isError: false, refetch: vi.fn() }
    renderManager()

    // 编辑 A（预填 label）
    await user.click(screen.getByRole("button", { name: /编辑/ }))
    expect(screen.getByLabelText(/名称/)).toHaveValue("翻译助手")
    // 关闭
    await user.click(screen.getByRole("button", { name: /取消/ }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())

    // 打开新建对话框
    await user.click(screen.getByRole("button", { name: /新建类型/ }))
    // label 应为空（而非上次编辑的「翻译助手」）
    await waitFor(() => {
      expect(screen.getByLabelText(/名称/)).toHaveValue("")
    })
  })
})
