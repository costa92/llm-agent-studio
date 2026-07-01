import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@testing-library/react"
import { BuiltinNodeCatalog } from "./BuiltinNodeCatalog"
import { useBuiltinNodeTypes } from "@/features/builtin-node-types/api"

vi.mock("@/features/builtin-node-types/api", () => ({
  useBuiltinNodeTypes: vi.fn(),
}))
const mockBuiltins = vi.mocked(useBuiltinNodeTypes)
const asResult = (data: unknown) => ({ data }) as unknown as ReturnType<typeof useBuiltinNodeTypes>

describe("BuiltinNodeCatalog", () => {
  it("渲染每个内置节点一行：label + type + 职责 + 只读「内置」标记", () => {
    mockBuiltins.mockReturnValue(
      asResult([
        { type: "script", label: "剧本", description: "把创意扩写成分镜脚本" },
        { type: "storyboard", label: "分镜", description: "拆解脚本为逐镜画面" },
      ]),
    )
    render(<BuiltinNodeCatalog />)
    expect(screen.getByText("剧本")).toBeInTheDocument()
    expect(screen.getByText("把创意扩写成分镜脚本")).toBeInTheDocument()
    expect(screen.getByText("storyboard")).toBeInTheDocument()
    // 只读：每行一个「内置」锁标记，无编辑/删除按钮。
    expect(screen.getAllByText("内置")).toHaveLength(2)
    expect(screen.queryByRole("button", { name: /编辑|删除/ })).toBeNull()
  })

  it("空目录 → 显示空态", () => {
    mockBuiltins.mockReturnValue(asResult([]))
    render(<BuiltinNodeCatalog />)
    expect(screen.getByText("暂无系统节点。")).toBeInTheDocument()
  })
})
