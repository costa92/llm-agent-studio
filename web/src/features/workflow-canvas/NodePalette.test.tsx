import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NodePalette } from "./NodePalette"

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
