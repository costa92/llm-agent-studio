import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import type { PlanCost } from "@/features/workflow/api"
import { todoTypeLabel } from "./nodeColor"
import { RunCostSummary } from "./RunCostSummary"

// usePlanCost mock：组件唯一数据源。每用例按需覆盖返回值。
const usePlanCost = vi.fn()
vi.mock("@/features/workflow/api", () => ({
  usePlanCost: (...args: unknown[]) => usePlanCost(...args) as unknown,
}))

afterEach(() => vi.clearAllMocks())

const COST: PlanCost = {
  generations: 3,
  tokens: 900,
  imageCount: 2,
  costMicros: 11_600_000, // ¥11.60
  kindCounts: { chat: 1, image: 2 },
  todos: [
    {
      todoId: "t-script",
      todoType: "script",
      kind: "chat",
      provider: "openai",
      model: "gpt-4o",
      generations: 1,
      tokens: 800,
      imageCount: 0,
      costMicros: 1_600_000,
    },
    {
      todoId: "t-board",
      todoType: "storyboard",
      kind: "image",
      provider: "fake",
      model: "img-1",
      generations: 2,
      tokens: 100,
      imageCount: 2,
      costMicros: 10_000_000,
    },
  ],
}

function renderCost() {
  return render(<RunCostSummary projectId="p1" planId="run1" />)
}

describe("RunCostSummary", () => {
  it("renders run totals: currency, tokens and per-kind counts", () => {
    usePlanCost.mockReturnValue({ data: COST, isLoading: false, isError: false })
    renderCost()
    expect(screen.getByText("¥11.60")).toBeInTheDocument()
    expect(screen.getByText("900")).toBeInTheDocument()
    expect(screen.getByText("chat 1 · image 2")).toBeInTheDocument()
    // 分解默认折叠：节点行不可见。
    expect(screen.queryByText("剧本")).toBeNull()
  })

  it("expands the per-node breakdown on click", async () => {
    usePlanCost.mockReturnValue({ data: COST, isLoading: false, isError: false })
    renderCost()
    await userEvent.click(screen.getByRole("button", { name: /按节点分解 \(2\)/ }))
    expect(screen.getByText("剧本")).toBeInTheDocument()
    expect(screen.getByText("分镜")).toBeInTheDocument()
    expect(screen.getByText("¥1.60")).toBeInTheDocument()
    expect(screen.getByText("¥10.00")).toBeInTheDocument()
    expect(screen.getByText("800 tok")).toBeInTheDocument()
    expect(screen.getByText(/image · img-1/)).toBeInTheDocument()
  })

  it("shows the empty state (not zeros) when the run has no ledger rows", () => {
    usePlanCost.mockReturnValue({
      data: { generations: 0, tokens: 0, imageCount: 0, costMicros: 0, kindCounts: {}, todos: [] },
      isLoading: false,
      isError: false,
    })
    renderCost()
    expect(screen.getByText("本次运行暂无用量记录")).toBeInTheDocument()
    expect(screen.queryByText("¥0.00")).toBeNull()
  })

  it("shows loading / unavailable states without crashing", () => {
    usePlanCost.mockReturnValue({ data: undefined, isLoading: true, isError: false })
    const { unmount } = renderCost()
    expect(screen.getByText("成本加载中…")).toBeInTheDocument()
    unmount()
    usePlanCost.mockReturnValue({ data: undefined, isLoading: false, isError: true })
    renderCost()
    expect(screen.getByText("成本数据暂不可用")).toBeInTheDocument()
  })
})

describe("todoTypeLabel", () => {
  it("maps builtin types to Chinese, strips custom: prefix, passes unknown through", () => {
    expect(todoTypeLabel("script")).toBe("剧本")
    expect(todoTypeLabel("storyboard")).toBe("分镜")
    expect(todoTypeLabel("custom:my-node")).toBe("my-node")
    expect(todoTypeLabel("llm")).toBe("llm")
  })
})
