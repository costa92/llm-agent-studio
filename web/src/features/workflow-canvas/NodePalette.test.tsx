import { afterEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NodePalette } from "./NodePalette"
import { useBuiltinNodeTypes } from "@/features/builtin-node-types/api"

// 内置类型由 useBuiltinNodeTypes() 数据驱动；mock 返回 4 个创作内置项。
vi.mock("@/features/builtin-node-types/api", () => ({
  useBuiltinNodeTypes: vi.fn(() => ({
    data: [
      { type: "script", label: "剧本", description: "" },
      { type: "storyboard", label: "分镜", description: "" },
      { type: "asset", label: "资产", description: "" },
      { type: "review", label: "预审", description: "" },
    ],
  })),
}))
const mockBuiltins = vi.mocked(useBuiltinNodeTypes)
const DESC_BUILTINS = [
  { type: "script", label: "剧本", description: "把创意扩写成分镜脚本" },
  { type: "storyboard", label: "分镜", description: "拆解脚本为逐镜画面" },
  { type: "asset", label: "资产", description: "为每镜生成图片资产" },
]
const withBuiltins = (data: typeof DESC_BUILTINS) =>
  ({ data }) as unknown as ReturnType<typeof useBuiltinNodeTypes>

function renderPalette(props: Partial<React.ComponentProps<typeof NodePalette>> = {}) {
  return render(
    <NodePalette
      onStandardPipeline={vi.fn()}
      {...props}
    />,
  )
}

describe("NodePalette quick-create chips", () => {
  it("renders the 3 quick-create chips when onQuickCreate is provided", () => {
    renderPalette({ onQuickCreate: vi.fn() })
    expect(screen.getByRole("button", { name: /LLM 节点/ })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /HTTP 节点/ })).toBeInTheDocument()
    // 脚本节点 chip 必须明确是 Starlark 变换，区别于「剧本」内置。
    expect(screen.getByRole("button", { name: /脚本节点.*Starlark/ })).toBeInTheDocument()
  })

  it("does not render quick-create chips when onQuickCreate is omitted", () => {
    renderPalette()
    expect(screen.queryByRole("button", { name: /LLM 节点/ })).not.toBeInTheDocument()
  })

  it("clicking 「+ LLM 节点」 calls onQuickCreate('llm')", async () => {
    const onQuickCreate = vi.fn()
    const user = userEvent.setup()
    renderPalette({ onQuickCreate })
    await user.click(screen.getByRole("button", { name: /LLM 节点/ }))
    expect(onQuickCreate).toHaveBeenCalledWith("llm")
  })

  it("clicking 「+ HTTP 节点」 calls onQuickCreate('http')", async () => {
    const onQuickCreate = vi.fn()
    const user = userEvent.setup()
    renderPalette({ onQuickCreate })
    await user.click(screen.getByRole("button", { name: /HTTP 节点/ }))
    expect(onQuickCreate).toHaveBeenCalledWith("http")
  })

  it("clicking the Starlark script chip calls onQuickCreate('script')", async () => {
    const onQuickCreate = vi.fn()
    const user = userEvent.setup()
    renderPalette({ onQuickCreate })
    await user.click(screen.getByRole("button", { name: /脚本节点.*Starlark/ }))
    expect(onQuickCreate).toHaveBeenCalledWith("script")
  })

  it("distinguishes runnable typed chips from the annotation '+ 自定义类型' affordance", () => {
    renderPalette({ onQuickCreate: vi.fn(), onAddCustomType: vi.fn() })
    // 注释类（不可运行，无 typeId）入口仍在，但与可运行类型分组/标签区分。
    expect(screen.getByRole("button", { name: /自定义类型/ })).toBeInTheDocument()
    expect(screen.getByText(/可运行类型/)).toBeInTheDocument()
  })
})

// PR-5：搜索框 + 系统/自定义分组 + 一句话职责（description）。
describe("NodePalette search + grouping (PR-5)", () => {
  afterEach(() => {
    mockBuiltins.mockReturnValue(withBuiltins(DESC_BUILTINS))
  })

  function renderWithDesc(props: Partial<React.ComponentProps<typeof NodePalette>> = {}) {
    mockBuiltins.mockReturnValue(withBuiltins(DESC_BUILTINS))
    return render(<NodePalette onStandardPipeline={vi.fn()} {...props} />)
  }

  it("渲染「系统节点」分组标题 + 每项一句话职责（description）", () => {
    renderWithDesc()
    expect(screen.getByText("系统节点")).toBeInTheDocument()
    expect(screen.getByText("把创意扩写成分镜脚本")).toBeInTheDocument()
  })

  it("有自定义类型时渲染「用户自定义节点」分组", () => {
    renderWithDesc({
      customTypes: [{ type: "custom:translate", label: "翻译", color: "#7c93ff", typeId: "t1" }],
    })
    expect(screen.getByText("用户自定义节点")).toBeInTheDocument()
    expect(screen.getByText("翻译")).toBeInTheDocument()
    expect(screen.getByLabelText("typed")).toBeInTheDocument()
  })

  it("无自定义类型 → 不渲染「用户自定义节点」分组", () => {
    renderWithDesc()
    expect(screen.queryByText("用户自定义节点")).toBeNull()
  })

  it("搜索框过滤系统节点（按 label / 职责）", async () => {
    const user = userEvent.setup()
    renderWithDesc()
    // 「拆解」只在 storyboard 的职责描述里（拆解脚本为逐镜画面），唯一命中分镜。
    await user.type(screen.getByLabelText("搜索节点"), "拆解")
    expect(screen.getByText("分镜")).toBeInTheDocument()
    expect(screen.queryByText("剧本")).toBeNull()
    expect(screen.queryByText("资产")).toBeNull()
  })

  it("搜索按职责描述命中（description 文本）", async () => {
    const user = userEvent.setup()
    renderWithDesc()
    await user.type(screen.getByLabelText("搜索节点"), "图片资产")
    expect(screen.getByText("资产")).toBeInTheDocument()
    expect(screen.queryByText("剧本")).toBeNull()
  })

  it("搜索无匹配 → 提示「无匹配系统节点」", async () => {
    const user = userEvent.setup()
    renderWithDesc()
    await user.type(screen.getByLabelText("搜索节点"), "zzz不存在")
    expect(screen.getByText("无匹配系统节点")).toBeInTheDocument()
  })
})

// PR-6：节点管理入口。
describe("NodePalette 节点管理入口 (PR-6)", () => {
  it("提供 onOpenManager 时渲染「节点管理」按钮并回调", async () => {
    const onOpenManager = vi.fn()
    const user = userEvent.setup()
    renderPalette({ onOpenManager })
    await user.click(screen.getByRole("button", { name: /节点管理/ }))
    expect(onOpenManager).toHaveBeenCalledTimes(1)
  })

  it("未提供 onOpenManager → 不渲染「节点管理」按钮", () => {
    renderPalette()
    expect(screen.queryByRole("button", { name: /节点管理/ })).toBeNull()
  })
})
